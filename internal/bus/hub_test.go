package bus

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
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
	h := NewHub("host", func(line string) bool { sink <- line; return true })

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
	h := NewHub("host", func(line string) bool { sink <- line; return true })

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
	h := NewHub("host", func(line string) bool { sink <- line; return true })

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

// TestTaskNoticeHookBroadcastsTransition: when the hook recognizes an
// addressed payload as a task transition, every participant gets the
// notice on the feed — while the payload itself still reaches only its
// target. Notices ride the notice path, so they can never wake a rider.
func TestTaskNoticeHookBroadcastsTransition(t *testing.T) {
	h := NewHub("host", nil)
	hostNotices := make(chan string, 16)
	h.OnNotice = func(line string) { hostNotices <- line }
	h.TaskNotice = func(from, to, payload string) (string, bool) {
		if payload == "A2A-TASK snap" {
			return "task 01a0448d: working (" + to + " → " + from + ")", true
		}
		return "", false
	}

	rider, _ := testPeer(t, h, "codex-luna")
	_, aliceLines := testPeer(t, h, "alice")
	_, carolLines := testPeer(t, h, "carol")

	if _, err := rider.Write([]byte(Addressed("alice", "A2A-TASK snap") + "\n")); err != nil {
		t.Fatal(err)
	}

	// Carol (uninvolved driver) sees the notice — and only the notice.
	l := recvLine(t, carolLines)
	if !IsNotice(l) || !strings.Contains(l, "task 01a0448d: working (alice → codex-luna)") {
		t.Fatalf("carol got %q, want the transition notice", l)
	}
	select {
	case l := <-carolLines:
		if !IsNotice(l) {
			t.Fatalf("carol also received %q; the payload must stay addressed", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
	// Alice still receives the payload itself.
	if l := recvMessage(t, aliceLines); l != Message("codex-luna", "A2A-TASK snap") {
		t.Fatalf("alice got %q, want the snapshot payload", l)
	}
	// The host's human view sees it too.
	for {
		n := recvLine(t, hostNotices)
		if strings.Contains(n, "task 01a0448d") {
			break
		}
	}
}

func TestTaskNoticeHookIgnoresPlainAddressedLines(t *testing.T) {
	h := NewHub("host", nil)
	h.TaskNotice = func(from, to, payload string) (string, bool) { return "", false }

	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")
	_, cLines := testPeer(t, h, "carol")

	if _, err := a.Write([]byte(Addressed("bob", "psst just for you") + "\n")); err != nil {
		t.Fatal(err)
	}
	if l := recvMessage(t, bLines); l != Message("alice", "psst just for you") {
		t.Fatalf("bob got %q", l)
	}
	select {
	case l := <-cLines:
		if !IsNotice(l) || strings.Contains(l, "psst") {
			t.Fatalf("carol saw %q for a non-task addressed line", l)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

// recvEnvelope reads lines until an enveloped message arrives,
// returning its sender, envelope id, and payload.
func recvEnvelope(t *testing.T, ch chan string) (from, id, payload string) {
	t.Helper()
	l := recvMessage(t, ch)
	f, body, ok := ParseMessage(l)
	if !ok {
		t.Fatalf("not a message: %q", l)
	}
	id, p, ok := ParseEnvelope(body)
	if !ok {
		t.Fatalf("addressed delivery not enveloped: %q", l)
	}
	return f, id, p
}

// The ACK contract end to end: an addressed line arrives enveloped,
// stays pending until the receiver ACKs, and the ACK clears it.
func TestAddressedDeliveryRequiresAck(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 200 * time.Millisecond
	a, _ := testPeer(t, h, "alice")
	bob, bLines := testPeer(t, h, "bob")

	if _, err := a.Write([]byte(Addressed("bob", "A2A-MSG {}") + "\n")); err != nil {
		t.Fatal(err)
	}
	from, id, payload := recvEnvelope(t, bLines)
	if from != "alice" || payload != "A2A-MSG {}" || id == "" {
		t.Fatalf("got from=%q id=%q payload=%q", from, id, payload)
	}
	if n := h.Spool.Pending("bob"); n != 1 {
		t.Fatalf("entry not retained pre-ACK (pending=%d)", n)
	}

	if _, err := bob.Write([]byte(Ack(id) + "\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.Spool.Pending("bob") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("ACK did not clear the entry (pending=%d)", h.Spool.Pending("bob"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	// And no redelivery after the ACK.
	select {
	case l := <-bLines:
		if !IsNotice(l) {
			t.Fatalf("redelivered after ACK: %q", l)
		}
	case <-time.After(3 * h.RetryInterval):
	}
}

func TestUnackedEnvelopeIsRedelivered(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 150 * time.Millisecond
	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")

	a.Write([]byte(Addressed("bob", "important") + "\n"))
	_, id1, _ := recvEnvelope(t, bLines)
	// No ACK: the same entry must come again.
	_, id2, payload := recvEnvelope(t, bLines)
	if id2 != id1 || payload != "important" {
		t.Fatalf("redelivery mismatch: id1=%q id2=%q payload=%q", id1, id2, payload)
	}
}

func TestCrashBeforeAckRedeliversOnRejoin(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 150 * time.Millisecond
	a, _ := testPeer(t, h, "alice")
	bob, bLines := testPeer(t, h, "bob")

	a.Write([]byte(Addressed("bob", "survive me") + "\n"))
	_, id1, _ := recvEnvelope(t, bLines)
	bob.Close() // crash without ACK

	_, bLines2 := testPeer(t, h, "bob")
	_, id2, payload := recvEnvelope(t, bLines2)
	if id2 != id1 || payload != "survive me" {
		t.Fatalf("rejoin redelivery mismatch: %q %q", id2, payload)
	}
}

// Host-addressed lines get the same durable-first contract as remote
// riders: spooled before the sink sees them (enveloped), forgotten
// only on AckLocal, and redeliverable via DrainLocal. Previously they
// bypassed the spool entirely — a host crash before the wake worker
// persisted lost the task with no copy anywhere. (PR #18 review, P1.)
func TestHostAddressedIsDurableUntilLocalAck(t *testing.T) {
	sunk := make(chan string, 8)
	h := NewHub("host", func(line string) bool { sunk <- line; return true })
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	a, _ := testPeer(t, h, "alice")

	if _, err := a.Write([]byte(Addressed("host", "A2A-MSG for-the-host") + "\n")); err != nil {
		t.Fatal(err)
	}
	var id string
	select {
	case l := <-sunk:
		from, body, _ := ParseMessage(l)
		var ok bool
		id, _, ok = ParseEnvelope(body)
		if !ok || from != "alice" {
			t.Fatalf("host sink got unenveloped %q", l)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host sink never received the line")
	}
	if n := h.Spool.Pending("host"); n != 1 {
		t.Fatalf("host entry not retained pre-ack (pending=%d)", n)
	}
	h.AckLocal(id)
	if n := h.Spool.Pending("host"); n != 0 {
		t.Fatalf("AckLocal did not clear the entry (pending=%d)", n)
	}
}

func TestDrainLocalRedeliversUnacked(t *testing.T) {
	var accept atomic.Bool
	sunk := make(chan string, 8)
	h := NewHub("host", func(line string) bool { sunk <- line; return accept.Load() })
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	a, _ := testPeer(t, h, "alice")

	a.Write([]byte(Addressed("host", "survive the host crash") + "\n"))
	<-sunk // first attempt, refused (accept=false); entry must remain
	if n := h.Spool.Pending("host"); n != 1 {
		t.Fatalf("refused delivery cleared the entry (pending=%d)", n)
	}

	// "Restart": DrainLocal re-offers; this time the sink accepts.
	accept.Store(true)
	h.DrainLocal()
	select {
	case l := <-sunk:
		_, body, _ := ParseMessage(l)
		if _, p, ok := ParseEnvelope(body); !ok || p != "survive the host crash" {
			t.Fatalf("redelivery mangled: %q", l)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DrainLocal did not redeliver")
	}
}

// The Gate 3 contract: an addressed line to a rider that is not
// connected is durably spooled — with a visible notice, never a silent
// drop — and flushed in order when that name joins.
func TestAddressedLineToAbsentRiderIsSpooled(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")

	if _, err := a.Write([]byte(Addressed("away-rider", "A2A-MSG later-task") + "\n")); err != nil {
		t.Fatal(err)
	}

	// The bus says so instead of staying silent.
	l := recvLine(t, bLines)
	if !IsNotice(l) || !strings.Contains(l, "away-rider") || !strings.Contains(l, "spooled") {
		t.Fatalf("bob got %q, want a spooled-for-away-rider notice", l)
	}

	// The rider joins and receives the spooled line before anything else.
	_, rLines := testPeer(t, h, "away-rider")
	from, _, payload := recvEnvelope(t, rLines)
	if from != "alice" || payload != "A2A-MSG later-task" {
		t.Fatalf("rejoined rider got %q from %q, want the spooled line", payload, from)
	}
}

func TestSpoolFlushPreservesOrderAcrossSenders(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	a, _ := testPeer(t, h, "alice")
	b, _ := testPeer(t, h, "bob")

	if _, err := a.Write([]byte(Addressed("away-rider", "first") + "\n")); err != nil {
		t.Fatal(err)
	}
	// Wait until the first line is durably spooled before sending the
	// second from another conn — otherwise their order is a data race.
	waitPending(t, h.Spool, "away-rider", 1)
	if _, err := b.Write([]byte(Addressed("away-rider", "second") + "\n")); err != nil {
		t.Fatal(err)
	}
	waitPending(t, h.Spool, "away-rider", 2)

	_, rLines := testPeer(t, h, "away-rider")
	if from, _, p := recvEnvelope(t, rLines); from != "alice" || p != "first" {
		t.Fatalf("got %q from %q, want alice's line first", p, from)
	}
	if from, _, p := recvEnvelope(t, rLines); from != "bob" || p != "second" {
		t.Fatalf("got %q from %q, want bob's line second", p, from)
	}
}

func TestNoSpoolMeansNoticeStillNamesTheLoss(t *testing.T) {
	// Without a spool configured the line is lost — the bus must still
	// say so rather than pretend delivery happened.
	h := NewHub("host", nil)
	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")

	if _, err := a.Write([]byte(Addressed("ghost", "hello?") + "\n")); err != nil {
		t.Fatal(err)
	}
	l := recvLine(t, bLines)
	if !IsNotice(l) || !strings.Contains(l, "ghost") {
		t.Fatalf("bob got %q, want a nobody-holds-that-name notice", l)
	}
}

// Broadcast (non-addressed) lines are the observability feed and are
// never spooled: ADR 0003 makes addressed delivery the reliable path.
func TestBroadcastLinesAreNotSpooled(t *testing.T) {
	spool := NewFileSpool(t.TempDir(), time.Hour)
	h := NewHub("host", nil)
	h.Spool = spool
	a, _ := testPeer(t, h, "alice")
	_, witnessLines := testPeer(t, h, "witness")

	if _, err := a.Write([]byte("hello everyone\n")); err != nil {
		t.Fatal(err)
	}
	// The witness receiving the line proves delivery ran; the relay loop
	// holds the hub lock, so a join after this observably orders after
	// the broadcast — without this the late rider can legitimately
	// register first and receive it.
	if l := recvMessage(t, witnessLines); l != Message("alice", "hello everyone") {
		t.Fatalf("witness got %q", l)
	}
	_, rLines := testPeer(t, h, "late-rider")
	select {
	case l := <-rLines:
		if !IsNotice(l) {
			t.Fatalf("late rider received %q, want nothing but notices", l)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// The spool handoff must be atomic with joins: if the absence check,
// the spool add, and the join-time drain are not serialized, a line
// sent while the rider is joining can land in the spool AFTER the
// drain ran — stranded until the next reconnect. (PR #14 review.)
// This drives the interleaving repeatedly; under the racy
// implementation a run of 30 reliably strands at least one line.
func TestConcurrentSendAndJoinNeverStrandsALine(t *testing.T) {
	for i := 0; i < 30; i++ {
		h := NewHub("host", nil)
		h.Spool = NewFileSpool(t.TempDir(), time.Hour)
		a, _ := testPeer(t, h, "alice")

		go a.Write([]byte(Addressed("flappy", "payload") + "\n"))
		_, rLines := testPeer(t, h, "flappy")

		// The line must reach flappy — live or via catch-up. If it went
		// to a spool nobody pumps, nothing will ever deliver it.
		deadline := time.After(3 * time.Second)
		for {
			var l string
			select {
			case l = <-rLines:
			case <-deadline:
				t.Fatalf("iteration %d: line stranded (pending=%d)", i, h.Spool.Pending("flappy"))
			}
			if !IsNotice(l) {
				from, body, _ := ParseMessage(l)
				_, p, ok := ParseEnvelope(body)
				if !ok || from != "alice" || p != "payload" {
					t.Fatalf("flappy got %q", l)
				}
				break
			}
		}
	}
}

// A rider returning to more catch-up than one outbox holds must lose
// NOTHING: entries leave the spool only once accepted for delivery,
// the overflow stays spooled, and the connection survives. (PR #15
// review, P1: Drain deleted everything first, the outbox overflowed,
// and the excess was gone forever.)
func TestCatchUpBeyondOutboxLosesNothing(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	total := outboxDepth + 200
	for i := 0; i < total; i++ {
		if _, err := h.Spool.Add("returning", fmt.Sprintf("[a] m%04d", i)); err != nil {
			t.Fatal(err)
		}
	}

	h.RetryInterval = 2 * time.Second
	rider, rLines := testPeer(t, h, "returning")

	// The rider ACKs as it reads; redelivered duplicates (possible
	// with at-least-once) are dropped by id. Every line must arrive on
	// THIS connection, in order, with no reconnect, and the spool must
	// empty as the ACKs land — that is the whole Gate 3 contract.
	received := 0
	seen := map[string]bool{}
	overall := time.After(60 * time.Second)
	for received < total {
		select {
		case l, ok := <-rLines:
			if !ok {
				t.Fatalf("connection closed during catch-up after %d lines", received)
			}
			if IsNotice(l) {
				continue
			}
			_, body, _ := ParseMessage(l)
			id, p, ok := ParseEnvelope(body)
			if !ok {
				t.Fatalf("unenveloped delivery: %q", l)
			}
			if _, err := rider.Write([]byte(Ack(id) + "\n")); err != nil {
				t.Fatal(err)
			}
			if seen[id] {
				continue // redelivery; already counted
			}
			seen[id] = true
			want := fmt.Sprintf("m%04d", received)
			if p != want {
				t.Fatalf("line %d out of order: got %q want %q", received, p, want)
			}
			received++
		case <-overall:
			t.Fatalf("catch-up stalled at %d/%d (pending=%d) — remainder stranded",
				received, total, h.Spool.Pending("returning"))
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for h.Spool.Pending("returning") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("all lines acked yet %d still pending", h.Spool.Pending("returning"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// While a backlog exists, a live addressed line must queue BEHIND it:
// otherwise newer tasks execute before older ones. (PR #17 review, P1.)
func TestLiveLineWaitsBehindBacklog(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	total := outboxDepth + 100
	for i := 0; i < total; i++ {
		if _, err := h.Spool.Add("returning", fmt.Sprintf("[a] m%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	h.RetryInterval = 2 * time.Second
	sender, _ := testPeer(t, h, "alice")
	rider, rLines := testPeer(t, h, "returning")

	// A live line sent while the backlog is still draining.
	if _, err := sender.Write([]byte(Addressed("returning", "the live one") + "\n")); err != nil {
		t.Fatal(err)
	}

	received := 0
	sawLive := false
	seen := map[string]bool{}
	overall := time.After(60 * time.Second)
	for received < total {
		select {
		case l, ok := <-rLines:
			if !ok {
				t.Fatal("connection closed mid catch-up")
			}
			if IsNotice(l) {
				continue
			}
			from, body, _ := ParseMessage(l)
			id, p, ok := ParseEnvelope(body)
			if !ok {
				t.Fatalf("unenveloped delivery: %q", l)
			}
			rider.Write([]byte(Ack(id) + "\n"))
			if seen[id] {
				continue
			}
			seen[id] = true
			if from == "alice" && p == "the live one" {
				sawLive = true
				continue
			}
			if sawLive {
				t.Fatalf("live line overtook backlog: %q arrived after it", p)
			}
			received++
		case <-overall:
			t.Fatalf("stalled at %d/%d", received, total)
		}
	}
}

func waitPending(t *testing.T, s Spooler, rider string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for s.Pending(rider) < n {
		if time.Now().After(deadline) {
			t.Fatalf("spool never reached %d pending for %s", n, rider)
		}
		time.Sleep(10 * time.Millisecond)
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
	h := NewHub("hostname", func(line string) bool { sink <- line; return true })

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

// A displaced peer that stopped reading must not stall its
// replacement: the notify-and-close of the stale conn is best-effort
// and must happen off the join path, or the new connection's read loop
// is blocked behind a 5s write deadline. (PR #9 review.)
func TestStalledDisplacedPeerDoesNotBlockReplacement(t *testing.T) {
	h := NewHub("host", nil)

	// Stale codex stops reading entirely: raw pipe, HELLO, read only
	// the welcome, then nothing — its buffer is full for any write.
	staleClient, staleServer := net.Pipe()
	t.Cleanup(func() { staleClient.Close() })
	go h.Serve(staleServer)
	staleClient.Write([]byte(Hello("codex") + "\n"))
	br := bufio.NewReader(staleClient)
	br.ReadString('\n') // welcome; after this, no more reads

	_, obsLines := testPeer(t, h, "observer")
	fresh, _ := testPeer(t, h, "codex") // displaces the stalled conn

	// The fresh codex speaks immediately; the observer must hear it
	// fast — not after the stale write deadline expires.
	start := time.Now()
	if _, err := fresh.Write([]byte("alive and talking\n")); err != nil {
		t.Fatal(err)
	}
	if l := recvMessage(t, obsLines); l != Message("codex", "alive and talking") {
		t.Fatalf("observer got %q", l)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("fresh join's traffic delayed %v by the stalled displaced peer", d)
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

// Liveness (issue #8, item 2): a rider that goes quiet past the
// threshold is flagged on the feed — deaf becomes a visible state —
// and its return is announced. Heartbeats (PING) count as life.
func TestQuietRiderIsFlaggedAndRecoveryAnnounced(t *testing.T) {
	h := NewHub("host", nil)
	h.QuietAfter = 300 * time.Millisecond
	quiet, _ := testPeer(t, h, "quiet-rider")
	_, obsLines := testPeer(t, h, "observer")

	// Wait past the threshold: the feed must name the silence.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l := <-obsLines:
			if IsNotice(l) && strings.Contains(l, "quiet-rider") && strings.Contains(l, "unresponsive") {
				goto flagged
			}
		case <-deadline:
			t.Fatal("quiet rider never flagged on the feed")
		}
	}
flagged:
	// Any line from the rider (a heartbeat counts) announces recovery.
	if _, err := quiet.Write([]byte(Ping() + "\n")); err != nil {
		t.Fatal(err)
	}
	deadline = time.After(5 * time.Second)
	for {
		select {
		case l := <-obsLines:
			if IsNotice(l) && strings.Contains(l, "quiet-rider") && strings.Contains(l, "responsive again") {
				return
			}
		case <-deadline:
			t.Fatal("recovery never announced")
		}
	}
}

func TestHeartbeatingRiderIsNeverFlagged(t *testing.T) {
	h := NewHub("host", nil)
	h.QuietAfter = 300 * time.Millisecond
	lively, _ := testPeer(t, h, "lively")
	_, obsLines := testPeer(t, h, "observer")

	stop := time.After(1 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if _, err := lively.Write([]byte(Ping() + "\n")); err != nil {
				t.Fatal(err)
			}
		case l := <-obsLines:
			// The silent observer itself may be flagged — correct; only
			// the heartbeating rider must never be.
			if IsNotice(l) && strings.Contains(l, "unresponsive") && strings.Contains(l, "lively") {
				t.Fatalf("heartbeating rider flagged: %q", l)
			}
		case <-stop:
			return
		}
	}
}

func TestPingIsConsumedNotRelayed(t *testing.T) {
	h := NewHub("host", nil)
	a, _ := testPeer(t, h, "alice")
	_, bLines := testPeer(t, h, "bob")
	a.Write([]byte(Ping() + "\n"))
	a.Write([]byte("real message\n"))
	if l := recvMessage(t, bLines); l != Message("alice", "real message") {
		t.Fatalf("bob got %q — a PING leaked onto the feed", l)
	}
}
