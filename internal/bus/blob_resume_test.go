package bus

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Per-frame ACKs mean the hub never redelivers a frame the receiver
// already ACKed — so after a receiver restart, the fsync'd partial
// file is the ONLY copy of that progress. A new receiver must resume
// it: replay the bytes into the hash and continue at the right seq.
func TestBlobReceiverResumesPartialAfterRestart(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("resume-me "), 12000) // 120KB
	frames := BlobFrames("resume-id", "big.bin", content, 16<<10)
	header, chunks := frames[0], frames[1:]

	// First life: header + the first 3 chunks land, then the process dies.
	r1 := NewBlobReceiver(dir, 0, func(string) {})
	if _, ok := r1.Offer("alice", header); !ok {
		t.Fatal("header refused")
	}
	for i := 0; i < 3; i++ {
		if _, ok := r1.Offer("alice", chunks[i]); !ok {
			t.Fatalf("chunk %d refused", i+1)
		}
	}

	// Second life: a fresh receiver over the same dir. The sender's hub
	// redelivers the header (unACKed retransmit is normal) and the
	// REMAINING chunks — the ACKed first three never come again.
	var note string
	r2 := NewBlobReceiver(dir, 0, func(l string) { note = l })
	if _, ok := r2.Offer("alice", header); !ok {
		t.Fatal("header refused on resume")
	}
	for i := 3; i < len(chunks); i++ {
		if _, ok := r2.Offer("alice", chunks[i]); !ok {
			t.Fatalf("chunk %d refused after resume", i+1)
		}
	}
	if note == "" || !strings.Contains(note, "FILE") {
		t.Fatalf("transfer did not complete after resume: %q", note)
	}
	path := note[strings.LastIndex(note, " ")+1:]
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("resumed blob corrupted")
	}
}

// A torn tail (crash mid-write before the fsync) is trimmed to whole
// chunks on resume, and the retransmitted chunk fills the gap.
func TestBlobReceiverResumeTrimsTornTail(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("torn-tail "), 8000) // 80KB
	frames := BlobFrames("torn-id", "torn.bin", content, 16<<10)
	header, chunks := frames[0], frames[1:]

	r1 := NewBlobReceiver(dir, 0, func(string) {})
	if _, ok := r1.Offer("alice", header); !ok {
		t.Fatal("header refused")
	}
	for i := 0; i < 2; i++ {
		if _, ok := r1.Offer("alice", chunks[i]); !ok {
			t.Fatalf("chunk %d refused", i+1)
		}
	}
	// Crash mid-third-chunk: 100 stray bytes past the chunk boundary.
	part := filepath.Join(dir, ".partial", "torn-id")
	f, err := os.OpenFile(part, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("X"), 100)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var note string
	r2 := NewBlobReceiver(dir, 0, func(l string) { note = l })
	if _, ok := r2.Offer("alice", header); !ok {
		t.Fatal("header refused on resume")
	}
	for i := 2; i < len(chunks); i++ {
		if _, ok := r2.Offer("alice", chunks[i]); !ok {
			t.Fatalf("chunk %d refused after torn resume", i+1)
		}
	}
	if !strings.Contains(note, "FILE") {
		t.Fatalf("transfer did not complete after torn resume: %q", note)
	}
	path := note[strings.LastIndex(note, " ")+1:]
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("torn-resume blob corrupted")
	}
}
