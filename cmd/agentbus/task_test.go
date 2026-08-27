package main

import (
	"bufio"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"net"
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
	route := hostSink(r, sink, nil, bus.NewDedup(64))

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
	route := hostSink(nil, func(line string) bool { delivered <- line; return true }, nil, bus.NewDedup(64))

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
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "ping", 5*time.Second, &out)
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
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "x", 5*time.Second, &out)
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
	code := runTaskConn(client, "alice", "worker", "x", 5*time.Second, &out)
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
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "ping", 10*time.Second, &out)
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
	runTaskConn(requesterConn(t, h), "alice", "worker", "x", 500*time.Millisecond, &out)

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
	code := runTaskConn(requesterConn(t, h), "alice", "worker", "anyone home", 1*time.Second, &out)
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
