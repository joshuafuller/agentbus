package task

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/joshuafuller/agentbus/internal/bus"
)

func TestMessageEnvelopeRoundTrip(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("run the tests"))
	payload := EncodeMessage(msg)
	if !strings.HasPrefix(payload, "A2A-MSG ") {
		t.Fatalf("payload %q lacks A2A-MSG marker", payload)
	}
	if strings.ContainsRune(payload, '\n') {
		t.Fatalf("payload spans lines: %q", payload)
	}
	got, ok := DecodeMessage(payload)
	if !ok {
		t.Fatal("DecodeMessage rejected its own encoding")
	}
	if got.ID != msg.ID || got.Parts[0].Text() != "run the tests" {
		t.Fatalf("round trip lost content: %+v", got)
	}
}

func TestTaskEnvelopeRoundTrip(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	tk := a2a.NewSubmittedTask(msg, msg)
	payload := EncodeTask(tk)
	got, ok := DecodeTask(payload)
	if !ok {
		t.Fatal("DecodeTask rejected its own encoding")
	}
	if got.ID != tk.ID || got.Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("round trip lost content: %+v", got)
	}
}

func TestDecodeRejectsForeignPayloads(t *testing.T) {
	for _, p := range []string{"hi", "A2A-MSG not-json", "A2A-TASK ", "DONE t1", ""} {
		if _, ok := DecodeMessage(p); ok {
			t.Fatalf("DecodeMessage accepted %q", p)
		}
		if _, ok := DecodeTask(p); ok {
			t.Fatalf("DecodeTask accepted %q", p)
		}
	}
}

// The rider: a received A2A-MSG becomes a run task whose every
// transition goes back to the requester as an addressed A2A-TASK line.
func TestRiderHandlesTaskMessage(t *testing.T) {
	dir := t.TempDir()
	sentCh := make(chan string, 8)
	r := &Rider{
		Dir:    dir,
		Runner: func(prompt string) (string, error) { return "did: " + prompt, nil },
		Send:   func(line string) { sentCh <- line },
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("count to 3"))
	if !r.Handle("alice", EncodeMessage(msg)) {
		t.Fatal("Handle did not claim an A2A-MSG payload")
	}

	// Execution is asynchronous; collect the three snapshots.
	var sent []string
	for len(sent) < 3 {
		select {
		case l := <-sentCh:
			sent = append(sent, l)
		case <-time.After(2 * time.Second):
			t.Fatalf("sent %d lines, want 3 (submitted, working, completed): %v", len(sent), sent)
		}
	}
	var states []a2a.TaskState
	var final *a2a.Task
	for _, line := range sent {
		to, payload, ok := bus.ParseAddressed(line)
		if !ok || to != "alice" {
			t.Fatalf("reply %q not addressed to alice", line)
		}
		tk, ok := DecodeTask(payload)
		if !ok {
			t.Fatalf("reply payload is not a task: %q", payload)
		}
		states = append(states, tk.Status.State)
		final = tk
	}
	want := []a2a.TaskState{a2a.TaskStateSubmitted, a2a.TaskStateWorking, a2a.TaskStateCompleted}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("states %v, want %v", states, want)
		}
	}
	if final.Status.Message.Parts[0].Text() != "did: count to 3" {
		t.Fatalf("result missing: %+v", final.Status)
	}
}

// Two tasks arriving back to back must run one after the other: the
// runner resumes ONE persisted model conversation (claude --continue /
// codex resume), and concurrent turns against it race. (PR #11 review.)
// Handle must also return quickly — the caller is the join read loop,
// which may not stall for the length of a model turn.
func TestRiderSerializesTaskExecution(t *testing.T) {
	var mu sync.Mutex
	running, maxRunning, runs := 0, 0, 0
	r := &Rider{
		Dir: t.TempDir(),
		Runner: func(prompt string) (string, error) {
			mu.Lock()
			running++
			if running > maxRunning {
				maxRunning = running
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			running--
			runs++
			mu.Unlock()
			return "ok", nil
		},
		Send: func(string) {},
	}

	start := time.Now()
	for i := 0; i < 3; i++ {
		msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("t"))
		if !r.Handle("alice", EncodeMessage(msg)) {
			t.Fatal("Handle did not claim the task")
		}
	}
	if d := time.Since(start); d > 40*time.Millisecond {
		t.Fatalf("Handle blocked the caller for %v; it must enqueue and return", d)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		done := runs == 3
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/3 tasks ran", runs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if maxRunning != 1 {
		t.Fatalf("runner overlapped: %d concurrent invocations, want 1", maxRunning)
	}
}

func TestRiderIgnoresChatLines(t *testing.T) {
	r := &Rider{Dir: t.TempDir(), Runner: nil, Send: func(string) { t.Fatal("sent") }}
	if r.Handle("alice", "just chatting") {
		t.Fatal("Handle claimed a plain chat line")
	}
	if r.Handle("alice", EncodeTask(&a2a.Task{ID: "t", ContextID: "c"})) {
		t.Fatal("Handle claimed a status update meant for a requester")
	}
}
