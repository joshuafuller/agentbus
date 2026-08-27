package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("compute sha256 of go.mod"))
	tk := a2a.NewSubmittedTask(msg, msg)

	if err := Save(dir, tk); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != tk.ID || got.ContextID != tk.ContextID {
		t.Fatalf("got ID=%q ContextID=%q, want %q %q", got.ID, got.ContextID, tk.ID, tk.ContextID)
	}
	if got.Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("got state %q, want SUBMITTED", got.Status.State)
	}
	if len(got.History) != 1 || got.History[0].Parts[0].Text() != "compute sha256 of go.mod" {
		t.Fatalf("history not preserved: %+v", got.History)
	}
}

func TestSaveIsPrivate(t *testing.T) {
	dir := t.TempDir()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	tk := a2a.NewSubmittedTask(msg, msg)
	if err := Save(dir, tk); err != nil {
		t.Fatal(err)
	}
	// Task content is bus traffic; the file must not be world-readable.
	fi, err := os.Stat(filepath.Join(dir, string(tk.ID)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("task file mode %o, want 0600", perm)
	}
}

func TestLoadMissingTask(t *testing.T) {
	if _, err := Load(t.TempDir(), "no-such-task"); err == nil {
		t.Fatal("Load of missing task returned nil error")
	}
}

func TestSaveRejectsPathTraversalID(t *testing.T) {
	dir := t.TempDir()
	tk := &a2a.Task{ID: "../evil", ContextID: "c"}
	if err := Save(dir, tk); err == nil {
		t.Fatal("Save accepted a path-traversal task ID")
	}
	if _, err := Load(dir, "../evil"); err == nil {
		t.Fatal("Load accepted a path-traversal task ID")
	}
}
