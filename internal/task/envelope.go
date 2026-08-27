package task

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

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

	once    sync.Once
	queue   chan queuedTask
	rejects chan rejection
}

type rejection struct {
	from  string
	msg   *a2a.Message
	cause string
}

type queuedTask struct {
	from string
	msg  *a2a.Message
}

// Handle inspects one received payload. It returns true when the
// payload was a task request it claimed; chat lines and task snapshots
// (requester-bound) are left for the normal sink. A claimed task is
// ENQUEUED, not run: Handle returns immediately (the caller is the
// join read loop, which must keep reading the bus), and one worker
// executes tasks strictly in arrival order — the runner resumes a
// single persisted model conversation, and concurrent turns against
// it race (PR #11 review).
func (r *Rider) Handle(from, payload string) bool {
	msg, ok := DecodeMessage(payload)
	if !ok {
		return false
	}
	r.once.Do(func() {
		r.queue = make(chan queuedTask, taskQueueDepth)
		r.rejects = make(chan rejection, taskQueueDepth)
		go func() {
			for q := range r.queue {
				r.run(q.from, q.msg)
			}
		}()
		// Rejections get their own worker: reject writes to disk and
		// the network, and doing that on the caller — the join read
		// loop — would stop the rider from reading the bus at exactly
		// the moment it is overloaded (PR #17 review).
		go func() {
			for rej := range r.rejects {
				r.reject(rej.from, rej.msg, rej.cause)
			}
		}()
	})
	select {
	case r.queue <- queuedTask{from: from, msg: msg}:
	default:
		// Queue full: refuse VISIBLY — the requester gets a terminal
		// REJECTED snapshot instead of waiting out its timeout
		// (PR #15 review). If even the rejection queue is full, the
		// task is dropped: the requester's timeout is then the truthful
		// signal that this rider is overwhelmed.
		select {
		case r.rejects <- rejection{from: from, msg: msg,
			cause: "rider task queue full (" + fmt.Sprint(taskQueueDepth) + " pending)"}:
		default:
		}
	}
	return true
}

// taskQueueDepth bounds how many tasks may wait behind the one
// running; past that, new tasks are rejected rather than silently
// dropped or allowed to block the read loop.
const taskQueueDepth = 64

// reject persists and reports a task that goes straight from SUBMITTED
// to REJECTED without ever running.
func (r *Rider) reject(from string, msg *a2a.Message, cause string) {
	tk := a2a.NewSubmittedTask(msg, msg)
	if err := Save(r.Dir, tk); err != nil {
		return // nothing durable to report; requester sees never-acknowledged
	}
	r.Send(bus.Addressed(from, EncodeTask(tk)))
	now := time.Now().UTC()
	tk.Status = a2a.TaskStatus{State: a2a.TaskStateRejected,
		Message:   a2a.NewMessageForTask(a2a.MessageRoleAgent, tk, a2a.NewTextPart(cause)),
		Timestamp: &now}
	if err := Save(r.Dir, tk); err != nil {
		return
	}
	r.Send(bus.Addressed(from, EncodeTask(tk)))
}

func (r *Rider) run(from string, msg *a2a.Message) {
	tk := a2a.NewSubmittedTask(msg, msg)
	notify := func(snap *a2a.Task) {
		r.Send(bus.Addressed(from, EncodeTask(snap)))
	}
	// A Run error means the task could not be advanced (persistence
	// failure). If it failed before the first snapshot went out, the
	// requester sees the task as never acknowledged — the same signal
	// as a deaf rider, and truthful; after that, as stuck in its last
	// reported state. (Wording per PR #11 review.)
	_ = Run(r.Dir, tk, r.Runner, notify)
}
