package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/joshuafuller/agentbus/internal/bus"
	"github.com/joshuafuller/agentbus/internal/task"
)

// runTask sends one A2A task to a named rider and follows its
// lifecycle to a terminal state. Exit codes: 0 completed, 1 the rider
// reported failure/rejection/cancellation, 2 the task never finished —
// including the deaf-rider case, which this command exists to expose.
func runTask(ticket, name, rider, prompt string, timeout time.Duration) error {
	if !bus.ValidName(rider) {
		return fmt.Errorf("invalid rider name %q", rider)
	}
	if prompt == "" {
		return fmt.Errorf("nothing to ask")
	}
	conn, err := dial(ticket)
	if err != nil {
		// Exit 1 is reserved for a rider-reported terminal failure;
		// a transport/setup error means NO terminal state was received,
		// which is the exit-2 contract (PR #16 review).
		fmt.Fprintf(os.Stderr, "agentbus: could not reach the bus: %v\n", err)
		os.Exit(2)
	}
	code := runTaskConn(conn, name, rider, prompt, timeout, os.Stdout)
	os.Exit(code)
	return nil
}

// runTaskConn is the transport-independent body of runTask, split out
// so tests can drive it over an in-memory hub. It closes conn before
// returning: runTask exits via os.Exit, which skips defers, and an
// unclosed tunnel conn leaves a ghost peer on the hub that swallows
// every line addressed to this name — results would reach the ghost
// instead of the spool.
func runTaskConn(conn net.Conn, name, rider, prompt string, timeout time.Duration, out io.Writer) int {
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	conn.SetReadDeadline(deadline)

	fmt.Fprintf(conn, "%s\n", bus.Hello(name))
	br := bufio.NewReader(conn)
	// The welcome confirms the hub registered us; only then may we send,
	// or the reply could relay before we can receive it.
	if _, err := br.ReadString('\n'); err != nil {
		fmt.Fprintf(out, "no welcome from the bus: %v\n", err)
		return 2
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(prompt))
	fmt.Fprintf(conn, "%s\n", bus.Addressed(rider, task.EncodeMessage(msg)))

	// The hub delivers addressed lines as envelopes: ACK each (from
	// this goroutine — the only writer once the request is sent), drop
	// duplicates, and hand Watch the plain line it expects.
	pr, pw := io.Pipe()
	// Closing the read end on return unblocks a writer goroutine caught
	// mid-write after Watch finished early — otherwise it leaks, parked
	// on a pipe nobody reads (PR #18 review).
	defer pr.Close()
	go func() {
		defer pw.Close()
		seen := bus.NewDedup(256)
		sc := bufio.NewScanner(br)
		sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for sc.Scan() {
			line := sc.Text()
			if from, body, ok := bus.ParseMessage(line); ok {
				if id, payload, isEnv := bus.ParseEnvelope(body); isEnv {
					fmt.Fprintf(conn, "%s\n", bus.Ack(id))
					if seen.Seen(id) {
						continue
					}
					line = bus.Message(from, payload)
				}
			}
			if _, err := fmt.Fprintln(pw, line); err != nil {
				return // reader gone; stop instead of blocking forever
			}
		}
	}()

	// Snapshots are correlated by our message ID in the task history —
	// the task ID itself is minted by the rider (server-generated per
	// spec) and unknown here until the first snapshot arrives.
	final, err := task.Watch(pr, msg.ID, func(snap *a2a.Task) {
		fmt.Fprintf(out, "%s  %s\n", renderState(snap.Status.State), snap.ID)
	})

	timedOut := time.Now().After(deadline)
	switch {
	case final == nil && timedOut:
		fmt.Fprintf(out, "task never acknowledged by %s within %s — the rider may be deaf (wire self-test: issue #8)\n", rider, timeout)
		return 2
	case final == nil:
		fmt.Fprintf(out, "no answer from the bus: %v\n", err)
		return 2
	case !final.Status.State.Terminal() && !timedOut:
		// The stream ended before the deadline: report the loss itself,
		// not a timeout that never happened.
		fmt.Fprintf(out, "bus connection lost while task was %s: %v\n", renderState(final.Status.State), err)
		return 2
	case !final.Status.State.Terminal():
		fmt.Fprintf(out, "task still %s after %s — no terminal state reached\n", renderState(final.Status.State), timeout)
		return 2
	case final.Status.State == a2a.TaskStateCompleted:
		fmt.Fprintln(out, resultText(final))
		return 0
	default:
		fmt.Fprintf(out, "task %s: %s\n", renderState(final.Status.State), resultText(final))
		return 1
	}
}

// driverLine is the driver's seat (issue #12): a received line whose
// payload is a task envelope is rewritten as one readable line before
// it reaches the driver's terminal and inbox; chat lines pass through
// untouched. Only joins without --on-msg use it — riders keep the raw
// payloads their task handler consumes.
func driverLine(line string) string {
	from, payload, ok := bus.ParseMessage(line)
	if !ok {
		return line
	}
	if rendered, ok := task.RenderLine(from, payload); ok {
		return rendered
	}
	return line
}

// hostSink routes the host's own deliveries the same way a join routes
// a rider's or driver's: with a task rider (host has --on-msg), task
// requests addressed to the host run through the lifecycle instead of
// leaking raw JSON into the wake command; without one, the host is a
// driver and task payloads render readable. Chat passes to the sink
// either way. Host-addressed lines arrive ENVELOPED from the hub
// (durable-first, PR #18 review): tasks ACK via ackLocal after the
// SUBMITTED persist (wired through Rider.Acked by the caller), chat
// ACKs after the sink accepts; a refusal leaves the entry spooled for
// DrainLocal. The returned bool reports sink-level acceptance.
func hostSink(rider *task.Rider, sink func(line string) bool, ackLocal func(id string), seen *bus.Dedup) func(line string) bool {
	return func(line string) bool {
		from, body, ok := bus.ParseMessage(line)
		if !ok {
			return sink(line)
		}
		payload := body
		envID := ""
		if id, p, isEnv := bus.ParseEnvelope(body); isEnv {
			envID, payload = id, p
			if seen != nil && seen.Seen(envID) {
				if ackLocal != nil {
					ackLocal(envID)
				}
				return true
			}
		}
		if rider != nil {
			if _, isTask := task.DecodeMessage(payload); isTask {
				rider.HandleEnveloped(from, envID, payload)
				return true
			}
			accepted := sink(bus.Message(from, payload))
			if accepted && envID != "" && ackLocal != nil {
				ackLocal(envID)
			}
			return accepted
		}
		out := bus.Message(from, payload)
		if rendered, ok := task.RenderLine(from, payload); ok {
			out = rendered
		}
		accepted := sink(out)
		if accepted && envID != "" && ackLocal != nil {
			ackLocal(envID)
		}
		return accepted
	}
}

// execRunner adapts the rider's --on-msg wake command into a task
// runner: the task prompt reaches the command only via env vars (never
// interpolated into the shell — the T3 rule), and stdout is captured
// as the task result. This is the same wake path chat messages use;
// only the plumbing of its output differs.
func execRunner(onMsg string) func(prompt string) (string, error) {
	return func(prompt string) (string, error) {
		cmd := exec.Command("sh", "-c", onMsg)
		cmd.Env = append(os.Environ(),
			"AGENTBUS_MSG="+prompt,
			"AGENTBUS_TEXT="+prompt,
		)
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("wake command: %w", err)
		}
		return strings.TrimSpace(out.String()), nil
	}
}

// renderState turns TASK_STATE_WORKING into "working" for humans.
func renderState(s a2a.TaskState) string {
	return strings.ToLower(strings.TrimPrefix(string(s), "TASK_STATE_"))
}

func resultText(t *a2a.Task) string {
	m := t.Status.Message
	if m == nil || len(m.Parts) == 0 {
		return ""
	}
	return m.Parts[0].Text()
}
