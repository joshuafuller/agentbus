package task

import (
	"strings"
	"testing"
	"unicode/utf8"

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
	short := shortID(tk.ID)
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
	short := shortID(tk.ID)
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

// A multi-line result must stay ONE physical line: Sink.Deliver and
// await both treat lines as message boundaries, so an embedded newline
// would shatter one task into several bogus messages. (PR #13 review.)
func TestRenderLineFlattensNewlines(t *testing.T) {
	payload, _ := snapshotPayload(a2a.TaskStateCompleted, "line one\nline two\r\nline three")
	line, ok := RenderLine("r", payload)
	if !ok {
		t.Fatal("rejected")
	}
	if strings.ContainsAny(line, "\n\r") {
		t.Fatalf("rendered line contains raw line breaks: %q", line)
	}
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(line, want) {
			t.Fatalf("content lost in flattening: %q", line)
		}
	}
}

// Truncation must cut at a rune boundary: a byte-index slice through a
// multi-byte rune emits invalid UTF-8 into terminals and inbox files.
// (PR #13 review.)
func TestRenderLineTruncatesAtRuneBoundary(t *testing.T) {
	// One ASCII byte then 2-byte runes, so a byte-index cut at any even
	// offset lands mid-rune.
	long := "x" + strings.Repeat("é", 300)
	payload, _ := snapshotPayload(a2a.TaskStateCompleted, long)
	line, ok := RenderLine("r", payload)
	if !ok {
		t.Fatal("rejected")
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("long result not truncated: %d bytes", len(line))
	}
	if !utf8.ValidString(line) {
		t.Fatalf("truncation produced invalid UTF-8: %q", line)
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
