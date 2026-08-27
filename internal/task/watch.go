package task

import (
	"bufio"
	"fmt"
	"io"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/joshuafuller/agentbus/internal/bus"
)

// Watch is the requesting side of the slice: it reads bus lines until
// the task born from the identified message reaches a terminal state
// and returns that final snapshot, reporting each state change through
// onUpdate. Correlation is by originating message ID, not task ID —
// task IDs are server-generated (the rider mints one; a requester
// never knows it in advance), while the message ID is the requester's
// own. If the stream ends first, Watch returns the last snapshot heard
// with an error — a task stuck in SUBMITTED is exactly the deaf-rider
// evidence the caller must be able to show (issue #8).
func Watch(r io.Reader, msgID string, onUpdate func(*a2a.Task)) (*a2a.Task, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	var last *a2a.Task
	for sc.Scan() {
		_, payload, ok := bus.ParseMessage(sc.Text())
		if !ok {
			continue
		}
		tk, ok := DecodeTask(payload)
		if !ok || !bornFrom(tk, msgID) {
			continue
		}
		last = tk
		onUpdate(tk)
		if tk.Status.State.Terminal() {
			return tk, nil
		}
	}
	if err := sc.Err(); err != nil {
		return last, err
	}
	return last, fmt.Errorf("bus stream ended before the task for message %s finished", msgID)
}

// bornFrom reports whether tk's history begins with the message the
// requester sent.
func bornFrom(tk *a2a.Task, msgID string) bool {
	return len(tk.History) > 0 && tk.History[0].ID == msgID
}
