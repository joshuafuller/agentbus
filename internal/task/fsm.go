// Package task gives bus messages real A2A task semantics: a typed
// lifecycle instead of the TASK/STARTED/DONE convention, so a sender
// can distinguish "working" from "never arrived" from "crashed"
// (ADR 0001). Types are a2a-go's own; only the carriage is ours
// (ADR 0004).
package task

import "github.com/a2aproject/a2a-go/v2/a2a"

// validNext is the whole lifecycle as data: every legal transition,
// nothing else. Terminal states are absent on the left by construction,
// so a finished task is immutable (a2a.TaskState.Terminal is the same
// judgment; the table is the enforcement).
var validNext = map[a2a.TaskState][]a2a.TaskState{
	a2a.TaskStateSubmitted: {
		a2a.TaskStateWorking, a2a.TaskStateRejected, a2a.TaskStateCanceled,
	},
	a2a.TaskStateWorking: {
		a2a.TaskStateCompleted, a2a.TaskStateFailed,
		a2a.TaskStateCanceled, a2a.TaskStateInputRequired,
	},
	a2a.TaskStateInputRequired: {
		a2a.TaskStateWorking, a2a.TaskStateCanceled,
	},
}

// ValidTransition reports whether a task may move from one state to
// another.
func ValidTransition(from, to a2a.TaskState) bool {
	for _, next := range validNext[from] {
		if next == to {
			return true
		}
	}
	return false
}
