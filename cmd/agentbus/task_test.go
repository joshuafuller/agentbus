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
func startRider(t *testing.T, h *bus.Hub, name string, runner func(string) (string, error)) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	if _, err := client.Write([]byte(bus.Hello(name) + "\n")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	r := &task.Rider{
		Dir:    t.TempDir(),
		Runner: runner,
		Send: func(line string) {
			mu.Lock()
			defer mu.Unlock()
			client.Write([]byte(line + "\n"))
		},
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
		for sc.Scan() {
			from, payload, ok := bus.ParseMessage(sc.Text())
			if !ok || runner == nil {
				continue // notices, or a deaf rider
			}
			r.Handle(from, payload)
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
