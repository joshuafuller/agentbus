package task

import (
	"fmt"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Run drives one submitted task to a terminal state: SUBMITTED →
// WORKING → COMPLETED or FAILED. Every transition is persisted to dir
// and only then reported through notify, so anything an observer is
// told is already recoverable from disk. A failing runner yields a
// FAILED task, not an error — failure is an outcome the protocol can
// express, which is the point of adopting task semantics (ADR 0001).
// Run returns an error only when the task itself cannot be advanced
// (persistence failure, illegal state).
func Run(dir string, tk *a2a.Task, runner func(prompt string) (string, error), notify func(*a2a.Task)) error {
	report := func() error {
		if err := Save(dir, tk); err != nil {
			return err
		}
		// Notify with the persisted snapshot, not the live pointer:
		// later transitions must not mutate what an observer was told.
		snap, err := Load(dir, tk.ID)
		if err != nil {
			return err
		}
		notify(snap)
		return nil
	}
	set := func(to a2a.TaskState, msg *a2a.Message) error {
		if !ValidTransition(tk.Status.State, to) {
			return fmt.Errorf("task %s: illegal transition %s → %s", tk.ID, tk.Status.State, to)
		}
		now := time.Now().UTC()
		tk.Status = a2a.TaskStatus{State: to, Message: msg, Timestamp: &now}
		return report()
	}

	// Acknowledge receipt: the requester's first signal that a rider
	// durably has the task.
	if tk.Status.State != a2a.TaskStateSubmitted {
		return fmt.Errorf("task %s: Run needs a submitted task, got %s", tk.ID, tk.Status.State)
	}
	if err := report(); err != nil {
		return err
	}
	if err := set(a2a.TaskStateWorking, nil); err != nil {
		return err
	}

	prompt := ""
	if len(tk.History) > 0 && len(tk.History[0].Parts) > 0 {
		prompt = tk.History[0].Parts[0].Text()
	}
	result, err := runner(prompt)
	if err != nil {
		return set(a2a.TaskStateFailed,
			a2a.NewMessageForTask(a2a.MessageRoleAgent, tk, a2a.NewTextPart(err.Error())))
	}
	return set(a2a.TaskStateCompleted,
		a2a.NewMessageForTask(a2a.MessageRoleAgent, tk, a2a.NewTextPart(result)))
}
