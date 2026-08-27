package bus

import (
	"bufio"
	"crypto/ed25519"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// keyedPeer performs the full client handshake: keyed HELLO, sign the
// hub's challenge, expect the welcome. Returns the conn and line chan.
func keyedPeer(t *testing.T, h *Hub, name string, key ed25519.PrivateKey, oneshot bool) (net.Conn, chan string, error) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write([]byte(HelloKeyed(name, oneshot, PubOf(key)) + "\n")); err != nil {
		return nil, nil, err
	}
	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, err
	}
	nonce, ok := ParseChallenge(strings.TrimSpace(line))
	if !ok {
		t.Fatalf("expected challenge, got %q", line)
	}
	if _, err := client.Write([]byte(SigLine(SignChallenge(key, nonce, name)) + "\n")); err != nil {
		return nil, nil, err
	}
	welcome, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, err // connection closed before welcome
	}
	if !strings.Contains(welcome, "welcome aboard") {
		return nil, nil, fmt.Errorf("refused: %s", strings.TrimSpace(welcome))
	}
	client.SetDeadline(time.Time{})
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(br)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	return client, lines, nil
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	k, err := LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TOFU: the first keyed rider binds its name to its key for the life
// of the bus; a different key — or no key — can no longer take it.
func TestTofuBindsFirstKeyAndRefusesOthers(t *testing.T) {
	h := NewHub("host", nil)
	key := newKey(t)
	_, riderLines, err := keyedPeer(t, h, "codex-luna", key, false)
	if err != nil {
		t.Fatal(err)
	}

	// An attacker with the ticket but a DIFFERENT key: refused, and the
	// legitimate rider is NOT displaced.
	attacker := newKey(t)
	_, _, err = keyedPeer(t, h, "codex-luna", attacker, false)
	if err == nil {
		t.Fatal("wrong-key join was accepted for a bound name")
	}
	select {
	case l, open := <-riderLines:
		if !open {
			t.Fatal("legitimate rider was displaced by a refused attacker")
		}
		_ = l // notices are fine
	case <-time.After(100 * time.Millisecond):
	}

	// Unkeyed legacy join under a bound name: refused.
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(3 * time.Second))
	client.Write([]byte(Hello("codex-luna") + "\n"))
	br := bufio.NewReader(client)
	l, err := br.ReadString('\n')
	if err == nil && strings.Contains(l, "welcome aboard") {
		t.Fatalf("unkeyed join accepted for a key-bound name: %q", l)
	}
}

func TestKeyedReconnectWithSameKeySupersedes(t *testing.T) {
	h := NewHub("host", nil)
	key := newKey(t)
	_, oldLines, err := keyedPeer(t, h, "codex-luna", key, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := keyedPeer(t, h, "codex-luna", key, false); err != nil {
		t.Fatalf("same-key reconnect refused: %v", err)
	}
	// The old connection is displaced as before.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, open := <-oldLines:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("stale connection not displaced by keyed reconnect")
		}
	}
}

// An operator-side one-shot send under a bound name must prove the key
// too — the incident that motivated all of this was an unauthenticated
// oneshot under a rider's name (PR #4 review, P1).
func TestOneshotUnderBoundNameNeedsTheKey(t *testing.T) {
	h := NewHub("host", nil)
	key := newKey(t)
	if _, _, err := keyedPeer(t, h, "codex-luna", key, false); err != nil {
		t.Fatal(err)
	}

	// Right key: oneshot accepted.
	if _, _, err := keyedPeer(t, h, "codex-luna", key, true); err != nil {
		t.Fatalf("keyed oneshot under own name refused: %v", err)
	}

	// No key: refused.
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(3 * time.Second))
	client.Write([]byte(HelloOneshot("codex-luna") + "\n"))
	br := bufio.NewReader(client)
	if l, err := br.ReadString('\n'); err == nil && strings.Contains(l, "welcome aboard") {
		t.Fatalf("unkeyed oneshot accepted under a bound name: %q", l)
	}
}

func TestWrongSignatureRefused(t *testing.T) {
	h := NewHub("host", nil)
	key := newKey(t)
	other := newKey(t)

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(3 * time.Second))
	// Present key's public half but sign with a different key.
	client.Write([]byte(HelloKeyed("liar", false, PubOf(key)) + "\n"))
	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	nonce, ok := ParseChallenge(strings.TrimSpace(line))
	if !ok {
		t.Fatalf("expected challenge, got %q", line)
	}
	client.Write([]byte(SigLine(SignChallenge(other, nonce, "liar")) + "\n"))
	if l, err := br.ReadString('\n'); err == nil && strings.Contains(l, "welcome aboard") {
		t.Fatalf("forged signature accepted: %q", l)
	}
}

// Names never claimed by a key keep working unkeyed — the identity
// layer is opt-in per name, not a breaking change for the whole bus.
func TestUnboundNamesStillWorkUnkeyed(t *testing.T) {
	h := NewHub("host", nil)
	key := newKey(t)
	if _, _, err := keyedPeer(t, h, "keyed-rider", key, false); err != nil {
		t.Fatal(err)
	}
	// A different, never-bound name joins unkeyed as always.
	_, lines := testPeer(t, h, "legacy-rider")
	a, _ := testPeer(t, h, "alice")
	a.Write([]byte("hello legacy\n"))
	if l := recvMessage(t, lines); l != Message("alice", "hello legacy") {
		t.Fatalf("legacy rider got %q", l)
	}
}

// ClientHello is the one client-side entry point for the handshake:
// keyed hellos answer the challenge, unkeyed ones do not expect one.
func TestClientHelloHandshake(t *testing.T) {
	h := NewHub("host", nil)
	key := newKey(t)

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(5 * time.Second))
	sc := bufio.NewScanner(client)
	if err := ClientHello(client, sc, "codex-luna", false, key); err != nil {
		t.Fatal(err)
	}
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome aboard") {
		t.Fatalf("after handshake got %q", sc.Text())
	}

	// A keyed oneshot under the now-bound name.
	c2, s2 := net.Pipe()
	t.Cleanup(func() { c2.Close() })
	go h.Serve(s2)
	c2.SetDeadline(time.Now().Add(5 * time.Second))
	sc2 := bufio.NewScanner(c2)
	if err := ClientHello(c2, sc2, "codex-luna", true, key); err != nil {
		t.Fatal(err)
	}
	if !sc2.Scan() || !strings.Contains(sc2.Text(), "welcome aboard") {
		t.Fatalf("keyed oneshot got %q", sc2.Text())
	}

	// Unkeyed ClientHello on an unbound name: plain flow, welcome next.
	c3, s3 := net.Pipe()
	t.Cleanup(func() { c3.Close() })
	go h.Serve(s3)
	c3.SetDeadline(time.Now().Add(5 * time.Second))
	sc3 := bufio.NewScanner(c3)
	if err := ClientHello(c3, sc3, "legacy", false, nil); err != nil {
		t.Fatal(err)
	}
	if !sc3.Scan() || !strings.Contains(sc3.Text(), "welcome aboard") {
		t.Fatalf("legacy hello got %q", sc3.Text())
	}
}
