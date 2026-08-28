package bus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Blob transfer (issue #2): the transfer is split from the
// notification. Bytes move as BLOB frames the receiving join writes
// to a content-addressed spool; the agent's context gets ONE short
// FILE line, never the payload.

func TestBlobHeaderRoundTrip(t *testing.T) {
	h := BlobHeader{ID: "x7", Name: "report.tar.gz", Size: 4200, Total: 3,
		Sum: strings.Repeat("ab", 32)}
	line := h.Encode()
	got, ok := ParseBlobHeader(line)
	if !ok {
		t.Fatalf("header did not parse: %q", line)
	}
	if got != h {
		t.Fatalf("round trip mangled header: %+v != %+v", got, h)
	}
}

func TestBlobHeaderRejectsUnsafeName(t *testing.T) {
	// The name becomes part of a filesystem path on the receiver: no
	// separators, no traversal, no spaces that would break the line.
	for _, bad := range []string{"../../etc/cron.d/x", "a/b", "a b", "", strings.Repeat("n", 300)} {
		h := BlobHeader{ID: "x", Name: bad, Size: 1, Total: 1, Sum: strings.Repeat("ab", 32)}
		if _, ok := ParseBlobHeader(h.Encode()); ok {
			t.Fatalf("unsafe blob name %q accepted", bad)
		}
	}
}

func TestBlobHeaderRejectsUnsafeID(t *testing.T) {
	for _, bad := range []string{"../../outside", "id/../x", "id with spaces", strings.Repeat("a", 65)} {
		h := BlobHeader{ID: bad, Name: "f.bin", Size: 1, Total: 1, Sum: strings.Repeat("ab", 32)}
		if _, ok := ParseBlobHeader(h.Encode()); ok {
			t.Fatalf("unsafe blob ID %q accepted", bad)
		}
	}
}

func TestBlobHeaderRejectsInvalidChecksum(t *testing.T) {
	h := BlobHeader{ID: "id", Name: "f.bin", Size: 1, Total: 1, Sum: strings.Repeat("g", 64)}
	if _, ok := ParseBlobHeader(h.Encode()); ok {
		t.Fatal("non-hex blob checksum accepted")
	}
}

func TestBlobChunkRoundTrip(t *testing.T) {
	raw := []byte("some binary\x00bytes\nwith newline")
	line := BlobChunk("x7", 2, raw)
	id, seq, data, ok := ParseBlobChunk(line)
	if !ok || id != "x7" || seq != 2 || !bytes.Equal(data, raw) {
		t.Fatalf("chunk round trip failed: ok=%v id=%q seq=%d", ok, id, seq)
	}
}

func TestBlobChunkEmptyRoundTrip(t *testing.T) {
	line := BlobChunk("x7", 1, nil)
	id, seq, data, ok := ParseBlobChunk(line)
	if !ok || id != "x7" || seq != 1 || len(data) != 0 {
		t.Fatalf("empty chunk did not parse: ok=%v id=%q seq=%d data=%x", ok, id, seq, data)
	}
}

func TestBlobChunkRejectsUnsafeID(t *testing.T) {
	line := BlobChunk("../../outside", 1, []byte("data"))
	if _, _, _, ok := ParseBlobChunk(line); ok {
		t.Fatal("unsafe blob chunk ID accepted")
	}
}

// The receiver: frames in, one file plus one notification line out.
func TestBlobReceiverWritesContentAddressedFileAndNotifiesOnce(t *testing.T) {
	dir := t.TempDir()
	var notes []string
	r := NewBlobReceiver(dir, 0, func(line string) { notes = append(notes, line) })

	payload := bytes.Repeat([]byte("agentbus"), 1000)
	sum := sha256.Sum256(payload)
	frames := BlobFrames("claude-main", "report.bin", payload, 1024)
	for _, f := range frames {
		if consumed, ok := r.Offer("claude-main", f); !consumed || !ok {
			t.Fatalf("receiver refused frame %q...", f[:min(40, len(f))])
		}
	}

	hash := hex.EncodeToString(sum[:])
	want := filepath.Join(dir, hash+"-report.bin")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("blob file not written: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("blob content corrupted in transfer")
	}
	if len(notes) != 1 {
		t.Fatalf("want exactly one notification, got %d: %v", len(notes), notes)
	}
	n := notes[0]
	for _, part := range []string{"FILE", hash, "report.bin", want} {
		if !strings.Contains(n, part) {
			t.Fatalf("notification %q missing %q", n, part)
		}
	}
}

func TestBlobReceiverKeepsFullDigestPathsDistinct(t *testing.T) {
	type candidate struct {
		payload []byte
		digest  string
	}
	first := candidate{
		payload: []byte("collision-candidate-758"),
		digest:  "58bc101607d656d7d4a68734ca62a287d9cf67d8c328d3cdef6bf939fa9811e0",
	}
	second := candidate{
		payload: []byte("collision-candidate-110114"),
		digest:  "58bc1016f193e7adc22f52e1072782877f8bc0a8fa36ba606e34ba5b42a4b7c6",
	}

	dir := t.TempDir()
	r := NewBlobReceiver(dir, 0, func(string) {})
	for i, c := range []candidate{first, second} {
		id := fmt.Sprintf("collision-%d", i)
		for _, frame := range BlobFrames(id, "same.bin", c.payload, len(c.payload)) {
			if consumed, ok := r.Offer("sender", frame); !consumed || !ok {
				t.Fatalf("receiver refused collision candidate %d", i)
			}
		}
	}
	for _, c := range []candidate{first, second} {
		path := filepath.Join(dir, c.digest+"-same.bin")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("full-digest blob path %s missing: %v", path, err)
		}
		if !bytes.Equal(got, c.payload) {
			t.Fatalf("blob at %s was overwritten", path)
		}
	}
}

func TestBlobReceiverCompletionIsConsumableAfterPublish(t *testing.T) {
	r := NewBlobReceiver(t.TempDir(), 0, func(string) {})
	frames := BlobFrames("transfer-1", "f.bin", []byte("data"), 4)
	if r.TakeCompleted("transfer-1") {
		t.Fatal("unpublished transfer reported complete")
	}
	for _, frame := range frames {
		if consumed, ok := r.Offer("sender", frame); !consumed || !ok {
			t.Fatalf("receiver refused frame %q", frame)
		}
	}
	if !r.TakeCompleted("transfer-1") {
		t.Fatal("published transfer did not report complete")
	}
	if r.TakeCompleted("transfer-1") {
		t.Fatal("completion signal was not consumed")
	}
}

func TestBlobReceiverToleratesRedeliveredChunk(t *testing.T) {
	r := NewBlobReceiver(t.TempDir(), 0, func(string) {})
	frames := BlobFrames("transfer-dup", "f.bin", []byte("abcdefgh"), 4)
	for _, frame := range frames[:2] {
		if consumed, ok := r.Offer("sender", frame); !consumed || !ok {
			t.Fatalf("receiver refused initial frame %q", frame)
		}
	}
	if consumed, ok := r.Offer("sender", frames[1]); !consumed || !ok {
		t.Fatal("receiver refused redelivered chunk")
	}
	if !r.TakeDuplicate("transfer-dup") {
		t.Fatal("redelivered chunk was not identified as a duplicate")
	}
	if consumed, ok := r.Offer("sender", frames[2]); !consumed || !ok {
		t.Fatal("receiver failed to complete after redelivered chunk")
	}
	if !r.TakeCompleted("transfer-dup") {
		t.Fatal("completed transfer was not reported")
	}
}

func TestBlobReceiverToleratesRedeliveredHeader(t *testing.T) {
	r := NewBlobReceiver(t.TempDir(), 0, func(string) {})
	frames := BlobFrames("header-dup", "f.bin", []byte("abcdefgh"), 4)
	for _, frame := range frames[:2] {
		if consumed, ok := r.Offer("sender", frame); !consumed || !ok {
			t.Fatalf("receiver refused initial frame %q", frame)
		}
	}
	if consumed, ok := r.Offer("sender", frames[0]); !consumed || !ok {
		t.Fatal("receiver refused redelivered header")
	}
	if consumed, ok := r.Offer("sender", frames[2]); !consumed || !ok {
		t.Fatal("receiver failed to complete after redelivered header")
	}
	if !r.TakeCompleted("header-dup") {
		t.Fatal("completed transfer was not reported")
	}
}

func TestBlobReceiverReportsTerminalRejection(t *testing.T) {
	r := NewBlobReceiver(t.TempDir(), 1, func(string) {})
	header := BlobFrames("transfer-reject", "f.bin", []byte("too big"), 4)[0]
	if consumed, ok := r.Offer("sender", header); !consumed || ok {
		t.Fatal("over-cap header was not refused")
	}
	if !r.TakeRejected("transfer-reject") {
		t.Fatal("terminal rejection was not reported")
	}
	if r.TakeRejected("transfer-reject") {
		t.Fatal("terminal rejection was not consumed")
	}
	if consumed, ok := r.Offer("sender", BlobChunk("transfer-reject", 1, []byte("x"))); !consumed || ok {
		t.Fatal("chunk after terminal rejection was not swallowed")
	}
	if !r.TakeRejected("transfer-reject") {
		t.Fatal("refused chunk was not reported as terminal")
	}
}

func TestBlobReceiverCleansUpPublishFailure(t *testing.T) {
	dir := t.TempDir()
	var receipts []string
	r := NewBlobReceiver(dir, 0, func(string) {})
	r.Reply = func(_, line string) { receipts = append(receipts, line) }
	payload := []byte("data")
	sum := sha256.Sum256(payload)
	final := filepath.Join(dir, hex.EncodeToString(sum[:])+"-f.bin")
	if err := os.Mkdir(final, 0o700); err != nil {
		t.Fatal(err)
	}
	frames := BlobFrames("publish-fail", "f.bin", payload, 4)
	if consumed, ok := r.Offer("sender", frames[0]); !consumed || !ok {
		t.Fatal("receiver refused header")
	}
	if consumed, ok := r.Offer("sender", frames[1]); !consumed || ok {
		t.Fatal("publish failure was not refused")
	}
	if _, ok := r.open["publish-fail"]; ok {
		t.Fatal("publish failure left transfer state open")
	}
	if _, err := os.Stat(filepath.Join(dir, ".partial", "publish-fail")); !os.IsNotExist(err) {
		t.Fatalf("publish failure left partial file: %v", err)
	}
	if len(receipts) != 1 || receipts[0] != "BLOB ERR publish-fail publish-error" {
		t.Fatalf("publish failure receipt = %v", receipts)
	}
}

func TestBlobReceiverRejectsCorruptTransfer(t *testing.T) {
	dir := t.TempDir()
	var notes []string
	r := NewBlobReceiver(dir, 0, func(line string) { notes = append(notes, line) })

	frames := BlobFrames("x", "f.bin", []byte("hello world"), 4)
	// Corrupt one data chunk (frames[0] is the header).
	id, seq, data, _ := ParseBlobChunk(frames[1])
	data[0] ^= 0xff
	frames[1] = BlobChunk(id, seq, data)
	for _, f := range frames {
		r.Offer("x", f)
	}
	_ = frames

	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() {
			t.Fatalf("corrupt blob landed on disk as %s", e.Name())
		}
	}
	if _, ok := r.open["x"]; ok {
		t.Fatal("corrupt transfer remained in the open table")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "corrupt") {
		t.Fatalf("driver never told about the corrupt transfer: %v", notes)
	}
}

func TestBlobReceiverEnforcesSizeCap(t *testing.T) {
	dir := t.TempDir()
	var notes []string
	r := NewBlobReceiver(dir, 16, func(line string) { notes = append(notes, line) }) // 16-byte cap

	frames := BlobFrames("x", "big.bin", bytes.Repeat([]byte("y"), 64), 8)
	accepted := true
	for _, f := range frames {
		_, ok := r.Offer("x", f)
		accepted = ok && accepted
	}
	if accepted {
		t.Fatal("over-cap transfer fully accepted")
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() {
			t.Fatalf("over-cap blob landed on disk as %s", e.Name())
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "cap") {
		t.Fatalf("driver never told about the refused transfer: %v", notes)
	}
}

// A non-blob payload is not the receiver's business.
func TestBlobReceiverIgnoresOrdinaryPayloads(t *testing.T) {
	r := NewBlobReceiver(t.TempDir(), 0, func(string) {})
	if consumed, _ := r.Offer("x", "just a chat line"); consumed {
		t.Fatal("ordinary payload swallowed by the blob receiver")
	}
	if consumed, _ := r.Offer("x", BlobChunk("id", 1, []byte("d"))); !consumed {
		t.Fatal("blob frame not recognized as consumable")
	}
}
