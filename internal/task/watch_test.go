package task

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/joshuafuller/agentbus/internal/bus"
)

func snapshotLine(tk *a2a.Task, state a2a.TaskState, result string) string {
	c := *tk
	c.Status = a2a.TaskStatus{State: state}
	if result != "" {
		c.Status.Message = a2a.NewMessageForTask(a2a.MessageRoleAgent, &c, a2a.NewTextPart(result))
	}
	return bus.Message("rider", EncodeTask(&c))
}

func TestWatchFollowsOwnTaskToTerminalState(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	tk := a2a.NewSubmittedTask(msg, msg)
	other := a2a.NewSubmittedTask(
		a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("y")),
		a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("y")))

	feed := strings.Join([]string{
		"* rider hopped on the bus",                             // notice: ignore
		bus.Message("rider", "unrelated chatter"),               // chat: ignore
		snapshotLine(other, a2a.TaskStateWorking, ""),           // other task: ignore
		snapshotLine(tk, a2a.TaskStateSubmitted, ""),            // ours
		snapshotLine(tk, a2a.TaskStateWorking, ""),              // ours
		snapshotLine(tk, a2a.TaskStateCompleted, "all done"),    // ours, terminal
		snapshotLine(tk, a2a.TaskStateWorking, "must not read"), // after terminal
	}, "\n") + "\n"

	var states []a2a.TaskState
	final, err := Watch(strings.NewReader(feed), msg.ID, func(s *a2a.Task) {
		states = append(states, s.Status.State)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []a2a.TaskState{a2a.TaskStateSubmitted, a2a.TaskStateWorking, a2a.TaskStateCompleted}
	if len(states) != len(want) {
		t.Fatalf("saw states %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("saw states %v, want %v", states, want)
		}
	}
	if final.Status.State != a2a.TaskStateCompleted ||
		final.Status.Message.Parts[0].Text() != "all done" {
		t.Fatalf("final = %+v", final.Status)
	}
}

func TestWatchReportsStreamEndBeforeTerminal(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	tk := a2a.NewSubmittedTask(msg, msg)

	feed := snapshotLine(tk, a2a.TaskStateSubmitted, "") + "\n"
	final, err := Watch(strings.NewReader(feed), msg.ID, func(*a2a.Task) {})
	if err == nil {
		t.Fatal("Watch returned nil error though the task never finished")
	}
	// The last known state comes back with the error: a task stuck in
	// SUBMITTED must be reportable — that is the deaf-rider signal.
	if final == nil || final.Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("final = %+v, want last-known SUBMITTED snapshot", final)
	}
}

func TestWatchWithNoSnapshotAtAll(t *testing.T) {
	final, err := Watch(strings.NewReader(""), "t1", func(*a2a.Task) {})
	if err == nil {
		t.Fatal("Watch returned nil error on empty stream")
	}
	if final != nil {
		t.Fatalf("final = %+v, want nil (nothing was ever heard)", final)
	}
}
