package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
	"github.com/tailscale/tailcat"
)

// The ticket must survive a host restart (#34): the identity saved to
// the state dir reloads into the exact same ConnBlob, so every issued
// boarding pass and every rider's saved ticket stays valid across an
// in-place upgrade.
func TestHostRestartKeepsTicket(t *testing.T) {
	dir := t.TempDir()

	pk := tailcat.NewPrivateKey()
	pk.Public.RegionID = 17
	if err := saveHostIdentity(dir, pk); err != nil {
		t.Fatal(err)
	}

	got, err := loadHostIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("identity not found after save")
	}
	if !got.Private.Equal(pk.Private) {
		t.Fatal("private key changed across save/load")
	}
	if got.Public.ConnBlob() != pk.Public.ConnBlob() {
		t.Fatalf("ticket changed across restart:\n old %s\n new %s",
			pk.Public.ConnBlob(), got.Public.ConnBlob())
	}

	fi, err := os.Stat(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode %v, want 0600", fi.Mode().Perm())
	}
}

// A missing state dir is a fresh host, not an error.
func TestLoadHostIdentityAbsent(t *testing.T) {
	pk, err := loadHostIdentity(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("absent identity must not error: %v", err)
	}
	if pk != nil {
		t.Fatal("phantom identity loaded from an empty dir")
	}
}

// --new-ticket is the rotation valve: it wipes the persisted identity
// and the TOFU bindings so the next start mints a fresh ticket with a
// clean trust table.
func TestNewTicketResetsHostState(t *testing.T) {
	dir := t.TempDir()
	if err := saveHostIdentity(dir, tailcat.NewPrivateKey()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tofu.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := resetHostState(dir); err != nil {
		t.Fatal(err)
	}
	if pk, _ := loadHostIdentity(dir); pk != nil {
		t.Fatal("identity survived --new-ticket")
	}
	if _, err := os.Stat(filepath.Join(dir, "tofu.json")); !os.IsNotExist(err) {
		t.Fatal("TOFU bindings survived --new-ticket")
	}
}

// A line spooled before the restart is delivered after it (#34 AC):
// the durable spool bridges the restart gap.
func TestSpoolSurvivesHostRestart(t *testing.T) {
	spoolDir := t.TempDir()

	h1 := bus.NewHub("host", nil)
	h1.Spool = bus.NewFileSpool(spoolDir, time.Hour)
	if err := runSendConn(requesterConn(t, h1), "alice", "bob", "TASK t1 survive the restart", nil); err != nil {
		t.Fatalf("send --to before restart: %v", err)
	}
	if h1.Spool.Pending("bob") == 0 {
		t.Fatal("nothing spooled for bob before the restart")
	}

	// The restarted hub opens the same spool dir.
	h2 := bus.NewHub("host", nil)
	h2.Spool = bus.NewFileSpool(spoolDir, time.Hour)
	h2.RetryInterval = 200 * time.Millisecond

	bobGot := chatRider(t, h2, "bob")
	want := "[alice] TASK t1 survive the restart"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lines := bobGot(); len(lines) > 0 {
			if lines[0] != want {
				t.Fatalf("bob got %v, want [%q]", lines, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("spooled line lost across the restart; bob got %v", bobGot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Refusals and displacements are the two session ends a reconnect loop
// must NOT retry: retrying a displaced join would displace the
// displacer, forever.
func TestPermanentNoticeClassification(t *testing.T) {
	permanent := []string{
		"* refused — alice is key-bound; connect with its key",
		"* refused — signature does not prove the presented key",
		"* refused — alice is bound to a different key",
		"* displaced — another connection joined as alice and took this name",
	}
	for _, line := range permanent {
		if _, ok := permanentNotice(line); !ok {
			t.Errorf("notice not classified permanent: %q", line)
		}
	}
	transient := []string{
		"* welcome aboard, alice — 3 on the bus",
		"* bob hopped on the bus",
		"* refused an unkeyed connection as bob: name is key-bound", // feed gossip about SOMEONE ELSE
		"* bob joined, displacing an existing connection under that name",
		"[bob] refused — not a notice at all",
	}
	for _, line := range transient {
		if reason, ok := permanentNotice(line); ok {
			t.Errorf("notice wrongly classified permanent (%q): %q", reason, line)
		}
	}
}
