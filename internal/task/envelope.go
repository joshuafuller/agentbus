package task

import (
	"encoding/json"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/joshuafuller/agentbus/internal/bus"
)

// Task traffic rides the existing line protocol as addressed payloads,
// marked so the rider's join loop can tell a task from chat. The JSON
// inside the markers is a2a-go's own encoding of the official types —
// this file is the single translation boundary (ADR 0004).
//
//	A2A-MSG  <json a2a.Message>   requester → rider: new task request
//	A2A-TASK <json a2a.Task>      rider → requester: full task snapshot
const (
	msgMarker  = "A2A-MSG "
	taskMarker = "A2A-TASK "
)

// EncodeMessage wraps a task-request message for the wire.
func EncodeMessage(m *a2a.Message) string {
	return msgMarker + mustJSON(m)
}

// DecodeMessage unwraps a task-request payload; ok is false for
// anything that is not a well-formed A2A-MSG line.
func DecodeMessage(payload string) (*a2a.Message, bool) {
	rest, found := strings.CutPrefix(payload, msgMarker)
	if !found {
		return nil, false
	}
	var m a2a.Message
	if err := json.Unmarshal([]byte(rest), &m); err != nil || m.ID == "" {
		return nil, false
	}
	return &m, true
}

// EncodeTask wraps a task snapshot for the wire.
func EncodeTask(t *a2a.Task) string {
	return taskMarker + mustJSON(t)
}

// DecodeTask unwraps a task-snapshot payload.
func DecodeTask(payload string) (*a2a.Task, bool) {
	rest, found := strings.CutPrefix(payload, taskMarker)
	if !found {
		return nil, false
	}
	var t a2a.Task
	if err := json.Unmarshal([]byte(rest), &t); err != nil || t.ID == "" {
		return nil, false
	}
	return &t, true
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// a2a types marshal cleanly by construction; reaching this is a
		// programming error, not a runtime condition.
		panic(err)
	}
	return string(b)
}

// Rider is the receiving half of the slice: it claims A2A-MSG payloads
// from the join loop, runs them through the task lifecycle, and sends
// every state snapshot back addressed to the requester.
type Rider struct {
	Dir    string                              // task persistence directory
	Runner func(prompt string) (string, error) // the wake path (--on-msg)
	Send   func(line string)                   // writes one line to the bus
}

// Handle inspects one received payload. It returns true when the
// payload was a task request it consumed; chat lines and task
// snapshots (requester-bound) are left for the normal sink.
func (r *Rider) Handle(from, payload string) bool {
	msg, ok := DecodeMessage(payload)
	if !ok {
		return false
	}
	tk := a2a.NewSubmittedTask(msg, msg)
	notify := func(snap *a2a.Task) {
		r.Send(bus.Addressed(from, EncodeTask(snap)))
	}
	if err := Run(r.Dir, tk, r.Runner, notify); err != nil {
		// The task could not be advanced (persistence failure). There is
		// no state to report; the requester will see it stuck.
		return true
	}
	return true
}
