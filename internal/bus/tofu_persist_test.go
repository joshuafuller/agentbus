package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// TOFU bindings must survive a host restart (#34): with a persistent
// ticket, a restart is an upgrade, not a trust reset — a rebooted hub
// that forgot its bindings would let any key claim a known rider name.
func TestHostRestartKeepsTOFUBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tofu.json")

	h1 := NewHub("host", nil)
	if err := h1.PersistBindings(path); err != nil {
		t.Fatal(err)
	}
	alice := newKey(t)
	conn, _, err := keyedPeer(t, h1, "alice", alice, false)
	if err != nil {
		t.Fatalf("first keyed join: %v", err)
	}
	conn.Close()

	// The restarted hub loads the same file.
	h2 := NewHub("host", nil)
	if err := h2.PersistBindings(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := keyedPeer(t, h2, "alice", alice, false); err != nil {
		t.Fatalf("alice's own key refused after restart: %v", err)
	}
	mallory := newKey(t)
	if _, _, err := keyedPeer(t, h2, "alice", mallory, false); err == nil {
		t.Fatal("an imposter key claimed alice's name after restart")
	}
}

// The bindings file holds public keys only, but names on it map the
// fleet: keep it private like every other state file.
func TestBindingsFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tofu.json")
	h := NewHub("host", nil)
	if err := h.PersistBindings(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := keyedPeer(t, h, "alice", newKey(t), false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no bindings file written after a bind: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("bindings file mode %v, want 0600", fi.Mode().Perm())
	}
}

// A corrupt bindings file must refuse to arm persistence loudly, not
// silently start with an empty trust table.
func TestCorruptBindingsFileRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tofu.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHub("host", nil)
	if err := h.PersistBindings(path); err == nil {
		t.Fatal("corrupt bindings file accepted silently")
	}
}
