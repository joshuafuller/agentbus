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

func TestAddressedLineReachesOnlyTarget(t *testing.T) {
	sink := make(chan string, 8)
	h := NewHub("host", func(line string) { sink <- line })

	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")
	_, cLines := testPeer(t, h, "carol")

	if _, err := a.Write([]byte(Addressed("bob", "A2A-MSG {}") + "\n")); err != nil {
		t.Fatal(err)
	}

	if l := recvMessage(t, bLines); l != Message("alice", "A2A-MSG {}") {
		t.Fatalf("bob got %q", l)
	}
	// carol and the host sink must see nothing.
	select {
	case l := <-cLines:
		if !IsNotice(l) {
			t.Fatalf("carol received addressed line: %q", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case l := <-sink:
		t.Fatalf("host sink received addressed line: %q", l)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAddressedLineToHostReachesSinkOnly(t *testing.T) {
	sink := make(chan string, 8)
	h := NewHub("host", func(line string) { sink <- line })

	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")

	if _, err := a.Write([]byte(Addressed("host", "A2A-TASK {}") + "\n")); err != nil {
		t.Fatal(err)
	}

	if l := recvLine(t, sink); l != Message("alice", "A2A-TASK {}") {
		t.Fatalf("host sink got %q", l)
	}
	select {
	case l := <-bLines:
		if !IsNotice(l) {
			t.Fatalf("bob received line addressed to host: %q", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestOneshotSenderNotEchoedToSameName(t *testing.T) {
	h := NewHub("host", nil)
	// Persistent rider and a one-shot sender share the name "codex".
	_, rxLines := testPeer(t, h, "codex")
	tx := oneshotPeer(t, h, "codex")
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
			t.Fatalf("codex rider got its own send back: %q", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

// oneshotPeer connects a write-only sender to h.
func oneshotPeer(t *testing.T, h *Hub, name string) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	if _, err := client.Write([]byte(HelloOneshot(name) + "\n")); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(client)
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome") {
		t.Fatalf("oneshot got no welcome: %q", sc.Text())
	}
	return client
}

func TestSameNameJoinSupersedes(t *testing.T) {
	h := NewHub("host", nil)
	old, oldLines := testPeer(t, h, "codex")
	_ = old
	_, freshLines := testPeer(t, h, "codex") // supersedes old
	tx := oneshotPeer(t, h, "tester")

	// Old connection is closed by the hub.
	deadline := time.After(2 * time.Second)
	for closed := false; !closed; {
		select {
		case _, ok := <-oldLines:
			if !ok {
				closed = true
			}
		case <-deadline:
			t.Fatal("stale same-name rider was not closed")
		}
	}

	if _, err := tx.Write([]byte("TASK x\n")); err != nil {
		t.Fatal(err)
	}
	if l := recvMessage(t, freshLines); l != Message("tester", "TASK x") {
		t.Fatalf("fresh rider got %q", l)
	}
}

func TestOneshotReceivesNoRelays(t *testing.T) {
	h := NewHub("host", nil)
	tx := oneshotPeer(t, h, "tester")
	a, _ := testPeer(t, h, "alice")
	if _, err := a.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	tx.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _ := tx.Read(buf); n > 0 && !IsNotice(strings.TrimSpace(string(buf[:n]))) {
		t.Fatalf("oneshot received relay: %q", buf[:n])
	}
}

// TestSinkSameNameFilter covers the sink delivery path (distinct from the
// peer loop): the host's own sink must not receive messages sent under the
// host's own name, but must receive messages from any other name.
func TestSinkSameNameFilter(t *testing.T) {
	sink := make(chan string, 8)
	h := NewHub("hostname", func(line string) { sink <- line })

	// A oneshot sender under the host's own name: sink must NOT receive it.
	same := oneshotPeer(t, h, "hostname")
	if _, err := same.Write([]byte("from myself\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case l := <-sink:
		t.Fatalf("sink received a same-name message: %q", l)
	case <-time.After(150 * time.Millisecond):
	}

	// A oneshot sender under a different name: sink MUST receive it.
	other := oneshotPeer(t, h, "somebody")
	if _, err := other.Write([]byte("from somebody\n")); err != nil {
		t.Fatal(err)
	}
	if l := recvLine(t, sink); l != Message("somebody", "from somebody") {
		t.Fatalf("sink got %q, want somebody's message", l)
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

// TestDisplacementIsAnnouncedNotDisguised guards the concealment half of
// the same-name takeover: because names are unauthenticated, a join that
// displaces an existing connection may be a different party taking the
// name. The bus must say a displacement happened rather than report it as
// a routine "reconnected", which reads identically to a rider's host
// waking from sleep. Prevention (a returning rider proving itself with a
// key) is issue #6; this test only guards visibility.
func TestDisplacementIsAnnouncedNotDisguised(t *testing.T) {
	h := NewHub("host", nil)
	_, watcherLines := testPeer(t, h, "watcher")
	_, _ = testPeer(t, h, "codex")
	drainNotice(t, watcherLines, "codex") // codex hopped on

	_, _ = testPeer(t, h, "codex") // displaces the first codex

	l := recvLine(t, watcherLines)
	if !IsNotice(l) {
		t.Fatalf("watcher got %q, want a notice", l)
	}
	if !strings.Contains(l, "displacing") {
		t.Fatalf("displacement not announced: %q", l)
	}
	if strings.Contains(l, "reconnected") {
		t.Fatalf("displacement still disguised as a reconnect: %q", l)
	}
}

// TestDisplacedPeerIsToldWhy guards that the evicted connection learns the
// cause before the hub closes it. Without this its operator sees only EOF
// in the rider log, which is indistinguishable from a network drop.
func TestDisplacedPeerIsToldWhy(t *testing.T) {
	h := NewHub("host", nil)
	_, oldLines := testPeer(t, h, "codex")
	_, _ = testPeer(t, h, "codex") // displaces it

	deadline := time.After(2 * time.Second)
	for {
		select {
		case l, ok := <-oldLines:
			if !ok {
				t.Fatal("displaced peer was closed without being told why")
			}
			if IsNotice(l) && strings.Contains(l, "displaced") {
				return // told before close
			}
		case <-deadline:
			t.Fatal("timed out waiting for the displacement notice")
		}
	}
}

// drainNotice consumes notices until one mentioning want is seen, so a
// test can position itself past unrelated join chatter.
func drainNotice(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("channel closed waiting for a notice mentioning %q", want)
			}
			if IsNotice(l) && strings.Contains(l, want) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a notice mentioning %q", want)
		}
	}
}
