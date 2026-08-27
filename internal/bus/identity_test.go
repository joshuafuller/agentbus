package bus

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadOrCreateKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !PubOf(k1).Equal(PubOf(k2)) {
		t.Fatal("second load produced a different key")
	}
	fi, err := os.Stat(filepath.Join(dir, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode %o, want 0600", perm)
	}
}

// The handshake transcript binds the signature to THIS connection
// (fresh hub nonce) and THIS name — a captured signature or signed
// card must not be replayable on a later join (PR #4 review, P1).
func TestSignVerifyChallenge(t *testing.T) {
	dir := t.TempDir()
	k, _ := LoadOrCreateKey(dir)
	sig := SignChallenge(k, "nonce-abc", "codex-luna")
	if !VerifyChallenge(PubOf(k), "nonce-abc", "codex-luna", sig) {
		t.Fatal("valid signature rejected")
	}
	if VerifyChallenge(PubOf(k), "nonce-abc", "impostor", sig) {
		t.Fatal("signature accepted for a different name")
	}
	if VerifyChallenge(PubOf(k), "nonce-xyz", "codex-luna", sig) {
		t.Fatal("signature replayed under a different nonce")
	}
	other, _ := LoadOrCreateKey(t.TempDir())
	if VerifyChallenge(PubOf(other), "nonce-abc", "codex-luna", sig) {
		t.Fatal("signature accepted for a different key")
	}
}

func TestHelloKeyedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	k, _ := LoadOrCreateKey(dir)
	line := HelloKeyed("codex-luna", false, PubOf(k))
	name, oneshot, pub, ok := ParseHelloKeyed(line)
	if !ok || name != "codex-luna" || oneshot || pub == nil {
		t.Fatalf("round trip failed: %q -> %q %v %v", line, name, oneshot, ok)
	}
	if !pub.Equal(PubOf(k)) {
		t.Fatal("public key mangled in transit")
	}
	// Oneshot senders carry keys too: an operator-side send under a
	// bound name must prove the key like any join (PR #4 review, P1).
	oline := HelloKeyed("codex-luna", true, PubOf(k))
	_, oneshot, _, ok = ParseHelloKeyed(oline)
	if !ok || !oneshot {
		t.Fatalf("oneshot keyed hello failed: %q", oline)
	}
	// Legacy unkeyed hellos still parse as keyless.
	name, _, pub, ok = ParseHelloKeyed(Hello("plain-rider"))
	if !ok || name != "plain-rider" || pub != nil {
		t.Fatalf("legacy hello mishandled: %q %v", name, pub)
	}
}

func TestChallengeLineRoundTrip(t *testing.T) {
	nonce, ok := ParseChallenge(Challenge("n0nce-123"))
	if !ok || nonce != "n0nce-123" {
		t.Fatalf("challenge round trip: %q %v", nonce, ok)
	}
	if _, ok := ParseChallenge("* welcome aboard"); ok {
		t.Fatal("notice parsed as challenge")
	}
	dir := t.TempDir()
	k, _ := LoadOrCreateKey(dir)
	sig := SignChallenge(k, "n", "x")
	got, ok := ParseSig(SigLine(sig))
	if !ok || string(got) != string(sig) {
		t.Fatal("sig line round trip failed")
	}
}

// Two concurrent first-joins racing keygen must converge on ONE key —
// a lost race that overwrites the persisted key while the hub binds
// the other would brick the name for the bus's lifetime on the next
// reconnect. (PR #20 review, P2.)
func TestConcurrentKeygenConverges(t *testing.T) {
	dir := t.TempDir()
	keys := make(chan string, 16)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, err := LoadOrCreateKey(dir)
			if err != nil {
				t.Error(err)
				return
			}
			keys <- string(PubOf(k))
		}()
	}
	wg.Wait()
	close(keys)
	first := ""
	for k := range keys {
		if first == "" {
			first = k
		} else if k != first {
			t.Fatal("concurrent keygen produced divergent keys")
		}
	}
}

// Only file-absence means "no key": a permission or IO error must be
// surfaced, not silently degrade to unauthenticated. (PR #20 review.)
func TestLoadKeyIfExistsPropagatesRealErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, err := LoadKeyIfExists(dir); err == nil {
		t.Fatal("permission error silently treated as no-key")
	}
	// A genuinely missing key stays the nil/nil case.
	if k, err := LoadKeyIfExists(t.TempDir()); err != nil || k != nil {
		t.Fatalf("absent key: k=%v err=%v", k, err)
	}
}

func TestParseHelloKeyedEnforcesValidName(t *testing.T) {
	k := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	for _, bad := range []string{"../evil", "a/b", "name;rm", "a\tb"} {
		line := HelloKeyed(bad, false, PubOf(k))
		if _, _, _, ok := ParseHelloKeyed(line); ok {
			t.Fatalf("unsafe name %q accepted", bad)
		}
	}
}
