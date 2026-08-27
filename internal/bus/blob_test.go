package bus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

func TestBlobChunkRoundTrip(t *testing.T) {
	raw := []byte("some binary\x00bytes\nwith newline")
	line := BlobChunk("x7", 2, raw)
	id, seq, data, ok := ParseBlobChunk(line)
	if !ok || id != "x7" || seq != 2 || !bytes.Equal(data, raw) {
		t.Fatalf("chunk round trip failed: ok=%v id=%q seq=%d", ok, id, seq)
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
	want := filepath.Join(dir, hash[:8]+"-report.bin")
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
	for _, part := range []string{"FILE", hash[:8], "report.bin", want} {
		if !strings.Contains(n, part) {
			t.Fatalf("notification %q missing %q", n, part)
		}
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
