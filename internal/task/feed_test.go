package task

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func snapshotPayload(state a2a.TaskState, result string) (string, *a2a.Task) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("count the tests"))
	tk := a2a.NewSubmittedTask(msg, msg)
	tk.Status = a2a.TaskStatus{State: state}
	if result != "" {
		tk.Status.Message = a2a.NewMessageForTask(a2a.MessageRoleAgent, tk, a2a.NewTextPart(result))
	}
	return EncodeTask(tk), tk
}

// TransitionNotice is the hub's view: a task snapshot relayed from a
// rider to its requester becomes one feed notice every driver can see.
func TestTransitionNoticeNamesStateAndParties(t *testing.T) {
	payload, tk := snapshotPayload(a2a.TaskStateWorking, "")
	line, ok := TransitionNotice("codex-luna", "alice", payload)
	if !ok {
		t.Fatal("TransitionNotice rejected a task snapshot")
	}
	short := string(tk.ID)[:8]
	for _, want := range []string{"task " + short, "working", "alice → codex-luna"} {
		if !strings.Contains(line, want) {
			t.Fatalf("notice %q missing %q", line, want)
		}
	}
}

func TestTransitionNoticeIgnoresNonTaskPayloads(t *testing.T) {
	for _, p := range []string{"just chatting", "A2A-MSG {}", ""} {
		if _, ok := TransitionNotice("a", "b", p); ok {
			t.Fatalf("TransitionNotice accepted %q", p)
		}
	}
	// A task *request* is not a transition; the first notice is SUBMITTED.
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	if _, ok := TransitionNotice("a", "b", EncodeMessage(msg)); ok {
		t.Fatal("TransitionNotice accepted a task request")
	}
}

// RenderLine is the driver's seat: an arriving task payload becomes one
// readable line instead of raw JSON.
func TestRenderLineForCompletedSnapshot(t *testing.T) {
	payload, tk := snapshotPayload(a2a.TaskStateCompleted, "42 tests, all green")
	line, ok := RenderLine("codex-luna", payload)
	if !ok {
		t.Fatal("RenderLine rejected a task snapshot")
	}
	short := string(tk.ID)[:8]
	for _, want := range []string{"[codex-luna]", "task " + short, "completed", "42 tests, all green"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
	if strings.Contains(line, "{") {
		t.Fatalf("line %q leaks JSON", line)
	}
}

func TestRenderLineForFailedSnapshotCarriesCause(t *testing.T) {
	payload, _ := snapshotPayload(a2a.TaskStateFailed, "wake command: exit status 1")
	line, ok := RenderLine("broken-rider", payload)
	if !ok {
		t.Fatal("RenderLine rejected a failed snapshot")
	}
	if !strings.Contains(line, "failed") || !strings.Contains(line, "wake command: exit status 1") {
		t.Fatalf("failure cause missing from %q", line)
	}
}

func TestRenderLineForTaskRequest(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("collect the join.log errors"))
	line, ok := RenderLine("alice", EncodeMessage(msg))
	if !ok {
		t.Fatal("RenderLine rejected a task request")
	}
	for _, want := range []string{"[alice]", "task request", "collect the join.log errors"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

func TestRenderLinePassesChatThrough(t *testing.T) {
	if _, ok := RenderLine("alice", "plain chat"); ok {
		t.Fatal("RenderLine claimed a chat line")
	}
}

func TestRenderLineTruncatesLongResults(t *testing.T) {
	payload, _ := snapshotPayload(a2a.TaskStateCompleted, strings.Repeat("x", 500))
	line, ok := RenderLine("r", payload)
	if !ok {
		t.Fatal("rejected")
	}
	if len(line) > 300 {
		t.Fatalf("rendered line is %d chars; long results must be truncated", len(line))
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("truncated line %q lacks an ellipsis", line)
	}
}
