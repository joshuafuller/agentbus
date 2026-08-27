package task

import (
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// TransitionNotice turns a relayed task snapshot into the feed notice
// every driver sees: "task 01a0448d: working (alice → codex-luna)".
// The hub calls it (via an injected hook — bus cannot import this
// package) with the relay's direction: snapshots flow rider →
// requester, so from is the rider and to is the requester. ok is false
// for anything that is not a task snapshot; a task *request* is not a
// transition — its SUBMITTED snapshot is the first notice.
func TransitionNotice(from, to, payload string) (string, bool) {
	tk, ok := DecodeTask(payload)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("task %s: %s (%s → %s)",
		shortID(tk.ID), renderState(tk.Status.State), to, from), true
}

// RenderLine is the driver's seat: an arriving task payload becomes one
// readable line — state, short id, parties, result or cause — instead
// of raw JSON. ok is false for chat lines, which pass through untouched.
func RenderLine(from, payload string) (string, bool) {
	if tk, ok := DecodeTask(payload); ok {
		line := fmt.Sprintf("[%s] task %s %s", from, shortID(tk.ID), renderState(tk.Status.State))
		if txt := statusText(tk); txt != "" {
			line += " → " + truncate(txt, 200)
		}
		return line, true
	}
	if msg, ok := DecodeMessage(payload); ok {
		prompt := ""
		if len(msg.Parts) > 0 {
			prompt = msg.Parts[0].Text()
		}
		return fmt.Sprintf("[%s] task request: %s", from, truncate(prompt, 200)), true
	}
	return "", false
}

func shortID(id a2a.TaskID) string {
	s := string(id)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// renderState turns TASK_STATE_WORKING into "working" for humans.
func renderState(s a2a.TaskState) string {
	const prefix = "TASK_STATE_"
	out := string(s)
	if len(out) > len(prefix) && out[:len(prefix)] == prefix {
		out = out[len(prefix):]
	}
	b := []byte(out)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
		if c == '_' {
			b[i] = ' '
		}
	}
	return string(b)
}

func statusText(tk *a2a.Task) string {
	m := tk.Status.Message
	if m == nil || len(m.Parts) == 0 {
		return ""
	}
	return m.Parts[0].Text()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
