package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpoolAddDrainPreservesOrder(t *testing.T) {
	s := NewFileSpool(t.TempDir(), time.Hour)
	for _, l := range []string{"[a] first", "[b] second", "[a] third"} {
		if err := s.Add("codex-luna", l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Drain("codex-luna")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"[a] first", "[b] second", "[a] third"}
	if len(got) != len(want) {
		t.Fatalf("drained %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("drained %v, want %v", got, want)
		}
	}
	// Drain consumes: a second drain is empty.
	if again, _ := s.Drain("codex-luna"); len(again) != 0 {
		t.Fatalf("second drain returned %v, want empty", again)
	}
}

func TestSpoolIsPerRider(t *testing.T) {
	s := NewFileSpool(t.TempDir(), time.Hour)
	s.Add("alpha", "[x] for alpha")
	s.Add("beta", "[x] for beta")
	got, _ := s.Drain("alpha")
	if len(got) != 1 || got[0] != "[x] for alpha" {
		t.Fatalf("alpha drained %v", got)
	}
	if left, _ := s.Drain("beta"); len(left) != 1 {
		t.Fatalf("beta's spool disturbed: %v", left)
	}
}

func TestSpoolSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	NewFileSpool(dir, time.Hour).Add("r", "[x] persisted")
	got, err := NewFileSpool(dir, time.Hour).Drain("r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "[x] persisted" {
		t.Fatalf("after restart drained %v", got)
	}
}

func TestSpoolExpiresOldEntriesAtDrain(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSpool(dir, 50*time.Millisecond)
	s.Add("r", "[x] stale")
	time.Sleep(80 * time.Millisecond)
	s.Add("r", "[x] fresh")
	got, err := s.Drain("r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "[x] fresh" {
		t.Fatalf("drained %v, want only the fresh entry", got)
	}
}

func TestSpoolFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSpool(dir, time.Hour)
	if err := s.Add("r", "[x] secret task"); err != nil {
		t.Fatal(err)
	}
	riderDir := filepath.Join(dir, "r")
	fi, err := os.Stat(riderDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("spool dir mode %o, want 0700", perm)
	}
	entries, _ := os.ReadDir(riderDir)
	if len(entries) == 0 {
		t.Fatal("no spool file written")
	}
	efi, _ := os.Stat(filepath.Join(riderDir, entries[0].Name()))
	if perm := efi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("spool file mode %o, want 0600", perm)
	}
}

func TestSpoolRejectsUnsafeRiderName(t *testing.T) {
	s := NewFileSpool(t.TempDir(), time.Hour)
	if err := s.Add("../evil", "[x] escape"); err == nil {
		t.Fatal("Add accepted a path-traversal rider name")
	}
}

func TestSpoolPendingCount(t *testing.T) {
	s := NewFileSpool(t.TempDir(), time.Hour)
	s.Add("r", "[x] one")
	s.Add("r", "[x] two")
	if n := s.Pending("r"); n != 2 {
		t.Fatalf("Pending = %d, want 2", n)
	}
	if n := s.Pending("nobody"); n != 0 {
		t.Fatalf("Pending for unknown = %d, want 0", n)
	}
}
