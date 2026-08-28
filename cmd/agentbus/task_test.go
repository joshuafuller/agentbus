package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
	"github.com/joshuafuller/agentbus/internal/task"
)

// startRider joins hub as a wired rider whose runner echoes the prompt.
// runner == nil means a deaf rider: joined, welcomed, never executing.
// Enveloped deliveries (spool-enabled hubs) are ACKed and deduped the
// way the real join loop does it.
func startRider(t *testing.T, h *bus.Hub, name string, runner func(string) (string, error)) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	if _, err := client.Write([]byte(bus.Hello(name) + "\n")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	sendLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		client.Write([]byte(line + "\n"))
	}
	r := &task.Rider{
		Dir:    t.TempDir(),
		Runner: runner,
		Send:   sendLine,
		Acked:  func(id string) { sendLine(bus.Ack(id)) },
	}
	sc := bufio.NewScanner(client)
	// Consume the welcome before returning: only then is the rider
	// registered, and an addressed line can reach it. (An addressed
	// line sent before registration reaches nobody — the requester
	// sees it as a task that is never acknowledged.)
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome aboard") {
		t.Fatalf("no welcome for rider %s: %q", name, sc.Text())
	}
	go func() {
		seen := bus.NewDedup(256)
		for sc.Scan() {
			from, body, ok := bus.ParseMessage(sc.Text())
			if !ok || runner == nil {
				continue // notices, or a deaf rider
			}
			if id, payload, isEnv := bus.ParseEnvelope(body); isEnv {
				if _, isTask := task.DecodeMessage(payload); isTask {
					r.HandleEnveloped(from, id, payload) // rider owns task dedup+ack
					continue
				}
				if seen.Seen(id) {
					sendLine(bus.Ack(id))
					continue
				}
				sendLine(bus.Ack(id))
				continue
			}
			r.Handle(from, body)
		}
	}()
}

func requesterConn(t *testing.T, h *bus.Hub) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	return client
}

// driverLine rewrites task payloads into readable lines for a driver's
// terminal and inbox; chat and anything unparseable pass through as-is.
func TestDriverLineRendersTaskSnapshot(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	tk := a2a.NewSubmittedTask(msg, msg)
	tk.Status = a2a.TaskStatus{State: a2a.TaskStateCompleted,
		Message: a2a.NewMessageForTask(a2a.MessageRoleAgent, tk, a2a.NewTextPart("174719"))}
	raw := bus.Message("codex-luna", task.EncodeTask(tk))

	got := driverLine(raw)
	if strings.Contains(got, "{") {
		t.Fatalf("driver still sees JSON: %q", got)
	}
	for _, want := range []string{"[codex-luna]", "completed", "174719"} {
		if !strings.Contains(got, want) {
			t.Fatalf("driver line %q missing %q", got, want)
		}
	}
}

func TestDriverLinePassesChatThrough(t *testing.T) {
	for _, raw := range []string{bus.Message("alice", "hey there"), "not a message line"} {
		if got := driverLine(raw); got != raw {
			t.Fatalf("driverLine changed %q to %q", raw, got)
		}
	}
}

// The host participates like anyone else: with --on-msg it is a rider
// (tasks addressed to it run through the lifecycle), without it a
// driver (task payloads render readable). Previously a host rider's
// wake command got raw A2A-MSG JSON and no snapshot ever returned —
// the requester always timed out. (PR #11 review.)
func TestHostSinkRoutesTasksToRider(t *testing.T) {
	ran := make(chan string, 1)
	delivered := make(chan string, 4)
	r := &task.Rider{
		Dir:    t.TempDir(),
		Runner: func(prompt string) (string, error) { ran <- prompt; return "done", nil },
		Send:   func(string) {},
	}
	sink := func(line string) bool { delivered <- line; return true }
	route := hostSink(r, sink, nil, bus.NewDedup(64), nil)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("host task"))
	route(bus.Message("alice", task.EncodeMessage(msg)))

	select {
	case p := <-ran:
		if p != "host task" {
			t.Fatalf("runner got %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task addressed to the host never reached its rider")
	}
	select {
	case l := <-delivered:
		t.Fatalf("task request also leaked to the host sink: %q", l)
	case <-time.After(100 * time.Millisecond):
	}

	// Chat still flows to the sink.
	route(bus.Message("alice", "hello host"))
	if l := <-delivered; l != bus.Message("alice", "hello host") {
		t.Fatalf("chat mangled: %q", l)
	}
}

func TestHostSinkRendersForDriverHost(t *testing.T) {
	delivered := make(chan string, 2)
	route := hostSink(nil, func(line string) bool { delivered <- line; return true }, nil, bus.NewDedup(64), nil)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	tk := a2a.NewSubmittedTask(msg, msg)
	route(bus.Message("rider", task.EncodeTask(tk)))
	l := <-delivered
	if strings.Contains(l, "{") {
		t.Fatalf("driver host still sees JSON: %q", l)
	}
	if !strings.Contains(l, "submitted") {
		t.Fatalf("driver host line %q missing state", l)
	}
}

func exerciseHostSinkStoresBlob(t *testing.T, rider *task.Rider) {
	t.Helper()
	dir := t.TempDir()
	blobs := bus.NewBlobReceiver(dir, 0, func(string) {})
	receipts := make(chan string, 1)
	blobs.Reply = func(to, line string) { receipts <- bus.Message(to, line) }
	var acked []string
	delivered := make(chan string, 8)
	route := hostSink(rider, func(line string) bool {
		delivered <- line
		return true
	}, func(id string) { acked = append(acked, id) }, bus.NewDedup(64), blobs)

	content := []byte("host-local blob")
	frames := bus.BlobFrames("hostblob", "artifact.bin", content, 4)
	for i, frame := range frames {
		if !route(bus.Message("alice", bus.Envelope(fmt.Sprintf("env-%d", i), frame))) {
			t.Fatalf("host rejected blob frame %d", i)
		}
	}
	select {
	case line := <-delivered:
		t.Fatalf("blob frame leaked to host sink: %q", line)
	default:
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for _, entry := range entries {
		if !entry.IsDir() {
			got, err = os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("host blob content = %q, want %q", got, content)
	}
	if len(acked) != len(frames) {
		t.Fatalf("acked %d blob envelopes, want %d", len(acked), len(frames))
	}
	select {
	case receipt := <-receipts:
		if receipt != bus.Message("alice", "BLOB OK hostblob") {
			t.Fatalf("receipt = %q", receipt)
		}
	default:
		t.Fatal("host did not route a blob receipt")
	}
}

func TestHostSinkStoresAddressedBlob(t *testing.T) {
	exerciseHostSinkStoresBlob(t, nil)
}

func TestHostRiderStoresAddressedBlob(t *testing.T) {
	exerciseHostSinkStoresBlob(t, &task.Rider{
		Dir:    t.TempDir(),
		Runner: func(string) (string, error) { return "", nil },
	})
}

// Per-frame ACK contract: durably written frames ACK immediately (the
// old hold-until-complete policy deadlocked against pump pacing), but
// the FINAL frame's ACK is withheld while the FILE notice is refused —
// its redelivery is the retry vehicle that keeps the published bytes
// discoverable. No success receipt goes out either.
func TestHostSinkWithholdsFinalAckWhenBlobNoticeRejected(t *testing.T) {
	blobs := bus.NewBlobReceiver(t.TempDir(), 0, func(string) {})
	blobs.Notify = func(string) bool { return false }
	receipts := make(chan string, 1)
	blobs.Reply = func(to, line string) { receipts <- bus.Message(to, line) }
	var acked []string
	route := hostSink(nil, func(string) bool { return true }, func(id string) {
		acked = append(acked, id)
	}, bus.NewDedup(64), blobs)

	frames := bus.BlobFrames("rejected-note", "artifact.bin", []byte("data"), 4)
	for i, frame := range frames {
		if !route(bus.Message("alice", bus.Envelope(fmt.Sprintf("env-%d", i), frame))) {
			t.Fatalf("host rejected frame %d before notice delivery", i)
		}
	}
	finalEnv := fmt.Sprintf("env-%d", len(frames)-1)
	for _, id := range acked {
		if id == finalEnv {
			t.Fatalf("final frame %s acked while the FILE notice is refused", finalEnv)
		}
	}
	select {
	case receipt := <-receipts:
		t.Fatalf("sent success receipt after FILE notice refusal: %q", receipt)
	default:
	}

	// Once the agent accepts the notice, the redelivered final frame
	// completes the transfer: final ACK plus the success receipt.
	blobs.Notify = func(string) bool { return true }
	if !route(bus.Message("alice", bus.Envelope(finalEnv, frames[len(frames)-1]))) {
		t.Fatal("redelivered final frame refused after notice acceptance")
	}
	found := false
	for _, id := range acked {
		if id == finalEnv {
			found = true
		}
	}
	if !found {
		t.Fatal("final frame not acked after the notice was accepted")
	}
	select {
	case <-receipts:
	default:
		t.Fatal("no success receipt after completion")
	}
}

func TestExecRunnerCapturesStdoutAsResult(t *testing.T) {
	runner := execRunner(`printf 'got:%s' "$AGENTBUS_MSG"`)
	out, err := runner("hi there")
	if err != nil {
		t.Fatal(err)
	}
	if out != "got:hi there" {
		t.Fatalf("runner returned %q", out)
	}
}

func TestExecRunnerReportsFailure(t *testing.T) {
	runner := execRunner(`echo "boom" >&2; exit 3`)
	if _, err := runner("x"); err == nil {
		t.Fatal("runner swallowed a failing wake command")
	}
}

func TestTaskRoundTripOverHub(t *testing.T) {
	h := bus.NewHub("host", nil)
	startRider(t, h, "worker", func(prompt string) (string, error) {
		return "echo: " + prompt, nil
	})

	var out strings.Builder
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "ping", 5*time.Second, nil, &out)
	if code != 0 {
		t.Fatalf("exit code %d, want 0; output:\n%s", code, out.String())
	}
	for _, want := range []string{"submitted", "working", "completed", "echo: ping"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTaskFailureExitsNonzero(t *testing.T) {
	h := bus.NewHub("host", nil)
	startRider(t, h, "worker", func(string) (string, error) {
		return "", &net.AddrError{Err: "wake command exited 1"}
	})

	var out strings.Builder
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "x", 5*time.Second, nil, &out)
	if code != 1 {
		t.Fatalf("exit code %d, want 1; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "failed") {
		t.Fatalf("output missing failure state:\n%s", out.String())
	}
}

// A lost bus connection must be reported as what it is, not disguised
// as a timeout: "task still submitted after 5s" hides the real failure
// when the stream ended early. (PR #11 review.)
func TestTaskReportsStreamEndNotTimeout(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go func() {
		br := bufio.NewReader(server)
		br.ReadString('\n') // HELLO
		server.Write([]byte(bus.Notice("welcome aboard, alice — 2 on the bus") + "\n"))
		req, _ := br.ReadString('\n') // the task request
		_, payload, _ := bus.ParseAddressed(strings.TrimSpace(req))
		msg, _ := task.DecodeMessage(payload)
		tk := a2a.NewSubmittedTask(msg, msg)
		// One SUBMITTED snapshot, then the bus vanishes mid-task.
		server.Write([]byte(bus.Message("worker", task.EncodeTask(tk)) + "\n"))
		server.Close()
	}()

	var out strings.Builder
	code := runTaskConn(client, "alice", "worker", "x", 5*time.Second, nil, &out)
	if code != 2 {
		t.Fatalf("exit %d, want 2; out=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "stream ended") && !strings.Contains(out.String(), "connection") {
		t.Fatalf("output hides the stream loss behind timeout wording: %q", out.String())
	}
}

// The whole ACK loop, end to end over a spool-enabled hub exactly as
// production runs it: enveloped deliveries both directions, both sides
// ACKing, and both spools empty when the dust settles.
func TestTaskRoundTripOverSpooledHub(t *testing.T) {
	h := bus.NewHub("host", nil)
	h.Spool = bus.NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 300 * time.Millisecond
	startRider(t, h, "worker", func(prompt string) (string, error) {
		return "echo: " + prompt, nil
	})

	var out strings.Builder
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "ping", 10*time.Second, nil, &out)
	if code != 0 {
		t.Fatalf("exit code %d, want 0; output:\n%s", code, out.String())
	}
	for _, want := range []string{"submitted", "working", "completed", "echo: ping"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	// Every envelope in both directions was ACKed: nothing pending.
	deadline := time.Now().Add(5 * time.Second)
	for h.Spool.Pending("worker")+h.Spool.Pending("alice") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("unacked leftovers: worker=%d alice=%d",
				h.Spool.Pending("worker"), h.Spool.Pending("alice"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A finished requester must leave the bus. runTask exits via os.Exit,
// which skips defers — if runTaskConn does not close the connection
// itself, the hub keeps a ghost peer under the requester's name that
// swallows every line addressed to it (results written to the ghost
// instead of the spool; observed live: bob's completed snapshot
// vanished into a dead tunnel).
func TestRunTaskConnClosesConnWhenDone(t *testing.T) {
	h := bus.NewHub("host", nil)
	startRider(t, h, "worker", nil) // deaf: runTaskConn returns on timeout

	var out strings.Builder
	runTaskConn(requesterConn(t, h), "alice", "worker", "x", 500*time.Millisecond, nil, &out)

	deadline := time.Now().Add(2 * time.Second)
	for {
		gone := true
		for _, p := range h.Peers() {
			if p == "alice" {
				gone = false
			}
		}
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alice is still on the bus after runTaskConn returned: %v", h.Peers())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The acceptance test of the whole slice: a deaf rider must be visibly
// deaf. The rider is joined and welcomed but never executes; the
// requester must come back saying the task was never acknowledged,
// not hang and not claim success.
func TestDeafRiderIsVisiblyDeaf(t *testing.T) {
	h := bus.NewHub("host", nil)
	startRider(t, h, "worker", nil) // deaf

	var out strings.Builder
	start := time.Now()
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "anyone home", 1*time.Second, nil, &out)
	if code != 2 {
		t.Fatalf("exit code %d, want 2; output:\n%s", code, out.String())
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("requester did not respect the timeout")
	}
	if !strings.Contains(out.String(), "never acknowledged") {
		t.Fatalf("output does not name the silence:\n%s", out.String())
	}
}

// Liveness (#23 review): `task` also holds a long-lived connection — a
// requester waiting on a slow task must heartbeat, or the hub flags it
// unresponsive for simply waiting on its answer.
func TestTaskRequesterHeartbeatsWhileWaiting(t *testing.T) {
	old := heartbeatEvery
	heartbeatEvery = 50 * time.Millisecond
	defer func() { heartbeatEvery = old }()

	h := bus.NewHub("host", nil)
	h.QuietAfter = 300 * time.Millisecond
	startRider(t, h, "worker", func(prompt string) (string, error) {
		time.Sleep(900 * time.Millisecond) // silent long enough to flag a non-heartbeating requester
		return "done", nil
	})

	obs, srv := net.Pipe()
	t.Cleanup(func() { obs.Close() })
	go h.Serve(srv)
	if _, err := obs.Write([]byte(bus.Hello("observer") + "\n")); err != nil {
		t.Fatal(err)
	}
	notices := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(obs)
		for sc.Scan() {
			notices <- sc.Text()
		}
	}()

	done := make(chan int, 1)
	var out strings.Builder
	go func() {
		done <- runTaskConn(requesterConn(t, h), "alice", "worker", "slow", 5*time.Second, nil, &out)
	}()
	for {
		select {
		case code := <-done:
			if code != 0 {
				t.Fatalf("slow task failed: exit %d, output %q", code, out.String())
			}
			return
		case l := <-notices:
			if strings.Contains(l, "alice") && strings.Contains(l, "unresponsive") {
				t.Fatalf("waiting requester flagged unresponsive: %q", l)
			}
		}
	}
}
