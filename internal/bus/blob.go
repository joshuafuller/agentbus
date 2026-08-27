package bus

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Blob transfer (issue #2): files move between riders as BLOB frames,
// out of the agent's context. The receiving join reassembles them into
// a content-addressed spool and hands the agent ONE short FILE line —
// context cost is the same for 2KB and 2GB. The transfer is the frames;
// the notification is the product.

// defaultBlobCap bounds what a ticket holder may write to a rider's
// disk. A ticket already grants a lot; unbounded disk writes would
// grant more (issue #2 policy note).
const defaultBlobCap = 64 << 20

// blobChunkSize is the raw bytes per chunk frame: base64 of 128KB is
// ~171KB, comfortably under the hub's 256KB line cap.
const blobChunkSize = 128 << 10

// BlobHeader announces a transfer: everything the receiver needs to
// preallocate judgement — name, size, chunk count, and the checksum
// the reassembled bytes must match.
type BlobHeader struct {
	ID    string
	Name  string
	Size  int64
	Total int
	Sum   string // sha256 hex of the whole blob
}

func validBlobID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') &&
			(c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// Encode formats the header frame.
func (h BlobHeader) Encode() string {
	return fmt.Sprintf("BLOB H %s %s %d %d %s", h.ID, h.Name, h.Size, h.Total, h.Sum)
}

// ParseBlobHeader extracts a header frame. The name is validated with
// the same rule as rider names — it becomes part of a filesystem path
// on the receiver, so separators, traversal, and spaces are refused at
// the wire, not at write time.
func ParseBlobHeader(line string) (BlobHeader, bool) {
	f := strings.Fields(line)
	if len(f) != 7 || f[0] != "BLOB" || f[1] != "H" {
		return BlobHeader{}, false
	}
	size, err1 := strconv.ParseInt(f[4], 10, 64)
	total, err2 := strconv.Atoi(f[5])
	if err1 != nil || err2 != nil || size < 0 || total < 1 {
		return BlobHeader{}, false
	}
	if !validBlobID(f[2]) {
		return BlobHeader{}, false
	}
	if !ValidName(f[3]) {
		return BlobHeader{}, false
	}
	if len(f[6]) != sha256.Size*2 {
		return BlobHeader{}, false
	}
	if _, err := hex.DecodeString(f[6]); err != nil {
		return BlobHeader{}, false
	}
	return BlobHeader{ID: f[2], Name: f[3], Size: size, Total: total, Sum: f[6]}, true
}

// BlobChunk formats one data frame.
func BlobChunk(id string, seq int, data []byte) string {
	return fmt.Sprintf("BLOB C %s %d %s", id, seq, base64.StdEncoding.EncodeToString(data))
}

// ParseBlobChunk extracts one data frame.
func ParseBlobChunk(line string) (id string, seq int, data []byte, ok bool) {
	f := strings.Fields(line)
	if len(f) != 5 || f[0] != "BLOB" || f[1] != "C" {
		return "", 0, nil, false
	}
	seq, err := strconv.Atoi(f[3])
	if err != nil || seq < 1 {
		return "", 0, nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(f[4])
	if err != nil {
		return "", 0, nil, false
	}
	return f[2], seq, raw, true
}

// BlobFrames chunks a payload into a complete frame sequence (header
// first). Small payloads only — the put command streams from disk
// instead of materializing the blob; this helper exists for tests and
// callers that already hold the bytes.
func BlobFrames(id, name string, payload []byte, chunk int) []string {
	if chunk <= 0 {
		chunk = blobChunkSize
	}
	sum := sha256.Sum256(payload)
	total := (len(payload) + chunk - 1) / chunk
	if total == 0 {
		total = 1
	}
	frames := []string{BlobHeader{ID: id, Name: name, Size: int64(len(payload)),
		Total: total, Sum: hex.EncodeToString(sum[:])}.Encode()}
	for i := 0; i < total; i++ {
		end := min((i+1)*chunk, len(payload))
		frames = append(frames, BlobChunk(id, i+1, payload[i*chunk:end]))
	}
	return frames
}

// BlobReceiver reassembles incoming frames into dir. One receiver per
// join; not safe for concurrent use (the join loop is one goroutine).
type BlobReceiver struct {
	dir  string
	cap  int64
	note func(string) // the one line the agent sees per transfer
	// Reply, if set, sends a delivery receipt addressed back to the
	// sender: "BLOB OK <id>" on success, "BLOB ERR <id> <why>" on
	// refusal or corruption. It lets `put` block until the bytes have
	// actually landed instead of racing the connection close.
	Reply func(to, line string)
	open  map[string]*blobXfer
}

// BlobReceipt formats a delivery receipt (receiver → sender).
func BlobReceipt(id string, ok bool, why string) string {
	if ok {
		return "BLOB OK " + id
	}
	return "BLOB ERR " + id + " " + why
}

// ParseBlobReceipt extracts a receipt. ok is the delivery verdict.
func ParseBlobReceipt(line string) (id string, ok bool, why string, isReceipt bool) {
	f := strings.SplitN(line, " ", 4)
	if len(f) < 3 || f[0] != "BLOB" || (f[1] != "OK" && f[1] != "ERR") {
		return "", false, "", false
	}
	if f[1] == "OK" {
		return f[2], true, "", true
	}
	if len(f) == 4 {
		why = f[3]
	}
	return f[2], false, why, true
}

type blobXfer struct {
	hdr     BlobHeader
	from    string
	file    *os.File
	sum     hash.Hash
	got     int64
	next    int
	refused bool
}

// NewBlobReceiver returns a receiver spooling into dir. maxBytes <= 0
// means the default cap.
func NewBlobReceiver(dir string, maxBytes int64, note func(string)) *BlobReceiver {
	if maxBytes <= 0 {
		maxBytes = defaultBlobCap
	}
	return &BlobReceiver{dir: dir, cap: maxBytes, note: note, open: map[string]*blobXfer{}}
}

// Offer inspects one payload. consumed reports whether it was a blob
// frame (the caller must not deliver it to the agent); ok reports
// whether it was accepted. A refused or corrupt transfer produces
// exactly one notification line.
func (r *BlobReceiver) Offer(from, payload string) (consumed, ok bool) {
	if h, isHdr := ParseBlobHeader(payload); isHdr {
		return true, r.start(from, h)
	}
	id, seq, data, isChunk := ParseBlobChunk(payload)
	if !isChunk {
		return false, false
	}
	return true, r.chunk(id, seq, data)
}

func (r *BlobReceiver) start(from string, h BlobHeader) bool {
	x := &blobXfer{hdr: h, from: from, sum: sha256.New(), next: 1}
	r.open[h.ID] = x
	if h.Size > r.cap {
		x.refused = true
		r.note(fmt.Sprintf("refused a %d-byte file %s from %s — over the %d-byte blob cap", h.Size, h.Name, from, r.cap))
		r.receipt(x, false, "over-cap")
		return false
	}
	part := filepath.Join(r.dir, ".partial")
	if err := os.MkdirAll(part, 0o700); err != nil {
		x.refused = true
		r.note(fmt.Sprintf("could not spool %s from %s: %v", h.Name, from, err))
		r.receipt(x, false, "spool-error")
		return false
	}
	f, err := os.OpenFile(filepath.Join(part, h.ID), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		x.refused = true
		r.note(fmt.Sprintf("could not spool %s from %s: %v", h.Name, from, err))
		r.receipt(x, false, "spool-error")
		return false
	}
	x.file = f
	return true
}

func (r *BlobReceiver) chunk(id string, seq int, data []byte) bool {
	x := r.open[id]
	if x == nil {
		return false // chunk for a transfer we never saw the header of
	}
	if x.refused {
		return false // already reported; swallow the rest quietly
	}
	if seq != x.next || x.got+int64(len(data)) > r.cap {
		return r.abort(x, "corrupt")
	}
	x.next++
	x.got += int64(len(data))
	x.sum.Write(data)
	if _, err := x.file.Write(data); err != nil {
		return r.abort(x, err.Error())
	}
	if x.next <= x.hdr.Total {
		return true
	}
	return r.finish(x)
}

// abort discards a transfer's partial file and tells the driver once.
func (r *BlobReceiver) abort(x *blobXfer, why string) bool {
	x.refused = true
	x.file.Close()
	os.Remove(filepath.Join(r.dir, ".partial", x.hdr.ID))
	r.note(fmt.Sprintf("discarded a corrupt transfer of %s from %s (%s)", x.hdr.Name, x.from, why))
	r.receipt(x, false, why)
	return false
}

// finish verifies the checksum and publishes the content-addressed
// file. Only a blob whose bytes match its announced sum ever leaves
// the partial directory.
func (r *BlobReceiver) finish(x *blobXfer) bool {
	got := hex.EncodeToString(x.sum.Sum(nil))
	if got != x.hdr.Sum || x.got != x.hdr.Size {
		return r.abort(x, "corrupt")
	}
	if err := x.file.Sync(); err != nil {
		return r.abort(x, err.Error())
	}
	if err := x.file.Close(); err != nil {
		return r.abort(x, err.Error())
	}
	final := filepath.Join(r.dir, got[:8]+"-"+x.hdr.Name)
	if err := os.Rename(filepath.Join(r.dir, ".partial", x.hdr.ID), final); err != nil {
		x.refused = true
		r.note(fmt.Sprintf("could not publish %s from %s: %v", x.hdr.Name, x.from, err))
		return false
	}
	delete(r.open, x.hdr.ID)
	r.note(fmt.Sprintf("[%s] FILE %s %s %dB → %s", x.from, got[:8], x.hdr.Name, x.got, final))
	r.receipt(x, true, "")
	return true
}

// receipt sends a delivery verdict back to the sender, if wired.
func (r *BlobReceiver) receipt(x *blobXfer, ok bool, why string) {
	if r.Reply != nil {
		r.Reply(x.from, BlobReceipt(x.hdr.ID, ok, why))
	}
}
