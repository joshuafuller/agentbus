package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// drainAll collects every line the spool will hand over.
func drainAll(t *testing.T, s *FileSpool, rider string) []string {
	t.Helper()
	var lines []string
	if _, _, err := s.Drain(rider, func(l string) bool { lines = append(lines, l); return true }); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestSpoolAddDrainPreservesOrder(t *testing.T) {
	s := NewFileSpool(t.TempDir(), time.Hour)
	for _, l := range []string{"[a] first", "[b] second", "[a] third"} {
		if err := s.Add("codex-luna", l); err != nil {
			t.Fatal(err)
		}
	}
	got := drainAll(t, s, "codex-luna")
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
	if again := drainAll(t, s, "codex-luna"); len(again) != 0 {
		t.Fatalf("second drain returned %v, want empty", again)
	}
}

func TestSpoolIsPerRider(t *testing.T) {
	s := NewFileSpool(t.TempDir(), time.Hour)
	s.Add("alpha", "[x] for alpha")
	s.Add("beta", "[x] for beta")
	got := drainAll(t, s, "alpha")
	if len(got) != 1 || got[0] != "[x] for alpha" {
		t.Fatalf("alpha drained %v", got)
	}
	if left := drainAll(t, s, "beta"); len(left) != 1 {
		t.Fatalf("beta's spool disturbed: %v", left)
	}
}

func TestSpoolSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	NewFileSpool(dir, time.Hour).Add("r", "[x] persisted")
	got := drainAll(t, NewFileSpool(dir, time.Hour), "r")
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
	got := drainAll(t, s, "r")
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

// TTL must not depend on the rider ever coming back: entries for
// misspelled or abandoned names would otherwise sit on disk forever,
// and a peer could fill the filesystem by addressing unused names.
// (PR #14 review.) SweepExpired walks every rider dir.
func TestSweepExpiredClearsAbandonedNames(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSpool(dir, 50*time.Millisecond)
	s.Add("typo-rider", "[x] never collected")
	s.Add("gone-rider", "[x] also stale")
	time.Sleep(80 * time.Millisecond)
	s.Add("live-rider", "[x] fresh")

	if err := s.SweepExpired(); err != nil {
		t.Fatal(err)
	}
	if n := s.Pending("typo-rider"); n != 0 {
		t.Fatalf("typo-rider still has %d entries after sweep", n)
	}
	if n := s.Pending("gone-rider"); n != 0 {
		t.Fatalf("gone-rider still has %d entries after sweep", n)
	}
	if n := s.Pending("live-rider"); n != 1 {
		t.Fatalf("live-rider's fresh entry was swept (pending=%d)", n)
	}
}

// A long-lived host must keep sweeping: startup-only enforcement lets
// stale entries for never-returning names accumulate until the next
// restart, which may be never. (PR #15 review.)
func TestSweepEveryExpiresWhileRunning(t *testing.T) {
	s := NewFileSpool(t.TempDir(), 40*time.Millisecond)
	stop := s.SweepEvery(25*time.Millisecond, nil)
	defer stop()

	s.Add("abandoned", "[x] never collected")
	deadline := time.Now().Add(2 * time.Second)
	for s.Pending("abandoned") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("entry outlived its TTL with the sweeper running (pending=%d)", s.Pending("abandoned"))
		}
		time.Sleep(20 * time.Millisecond)
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
