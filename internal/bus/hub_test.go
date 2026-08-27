package bus

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// testPeer connects an in-memory conn to h and completes the HELLO.
func testPeer(t *testing.T, h *Hub, name string) (client net.Conn, lines chan string) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	if _, err := client.Write([]byte(Hello(name) + "\n")); err != nil {
		t.Fatal(err)
	}
	lines = make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(client)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	// The welcome notice confirms the hub registered this peer.
	if l := recvLine(t, lines); !strings.Contains(l, "welcome aboard") {
		t.Fatalf("expected welcome, got %q", l)
	}
	return client, lines
}

func recvLine(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case l, ok := <-ch:
		if !ok {
			t.Fatal("connection closed early")
		}
		return l
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for line")
		return ""
	}
}

// drainNotices reads lines until a non-notice arrives.
func recvMessage(t *testing.T, ch chan string) string {
	t.Helper()
	for {
		l := recvLine(t, ch)
		if !IsNotice(l) {
			return l
		}
	}
}

func TestThreePeersRelay(t *testing.T) {
	sink := make(chan string, 8)
	h := NewHub("host", func(line string) { sink <- line })

	a, aLines := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")
	_, cLines := testPeer(t, h, "carol")

	if _, err := a.Write([]byte("hi from alice\n")); err != nil {
		t.Fatal(err)
	}

	want := Message("alice", "hi from alice")
	if l := recvMessage(t, bLines); l != want {
		t.Fatalf("bob got %q, want %q", l, want)
	}
	if l := recvMessage(t, cLines); l != want {
		t.Fatalf("carol got %q, want %q", l, want)
	}
	// alice must NOT receive her own message back.
	select {
	case l := <-aLines:
		if !IsNotice(l) {
			t.Fatalf("alice received %q, want nothing", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
	// host sink saw it too.
	if l := recvLine(t, sink); l != want {
		t.Fatalf("host sink got %q, want %q", l, want)
	}
}

func TestSameNameNotEchoed(t *testing.T) {
	h := NewHub("host", nil)
	// Persistent receiver and one-shot sender share the name "codex".
	_, rxLines := testPeer(t, h, "codex")
	tx, _ := testPeer(t, h, "codex")
	_, otherLines := testPeer(t, h, "other")

	if _, err := tx.Write([]byte("STARTED t1\n")); err != nil {
		t.Fatal(err)
	}
	if l := recvMessage(t, otherLines); l != Message("codex", "STARTED t1") {
		t.Fatalf("other got %q", l)
	}
	select {
	case l := <-rxLines:
		if !IsNotice(l) {
			t.Fatalf("codex receiver got its own send back: %q", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHostBroadcast(t *testing.T) {
	h := NewHub("host", nil)
	_, aLines := testPeer(t, h, "alice")
	h.Broadcast("all aboard")
	if l := recvMessage(t, aLines); l != Message("host", "all aboard") {
		t.Fatalf("alice got %q", l)
	}
}

func TestJoinLeaveNotices(t *testing.T) {
	h := NewHub("host", nil)
	_, aLines := testPeer(t, h, "alice")
	b, _ := testPeer(t, h, "bob")

	l := recvLine(t, aLines)
	if !IsNotice(l) || !strings.Contains(l, "bob") {
		t.Fatalf("expected bob join notice, got %q", l)
	}
	b.Close()
	l = recvLine(t, aLines)
	if !IsNotice(l) || !strings.Contains(l, "bob") {
		t.Fatalf("expected bob leave notice, got %q", l)
	}
}

func TestBadHelloRejected(t *testing.T) {
	h := NewHub("host", nil)
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { h.Serve(server); close(done) }()
	client.Write([]byte("not a hello\n"))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not close bad-hello conn")
	}
}
