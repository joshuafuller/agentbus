package task

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestValidTransitions(t *testing.T) {
	allowed := []struct{ from, to a2a.TaskState }{
		{a2a.TaskStateSubmitted, a2a.TaskStateWorking},
		{a2a.TaskStateSubmitted, a2a.TaskStateRejected},
		{a2a.TaskStateSubmitted, a2a.TaskStateCanceled},
		{a2a.TaskStateWorking, a2a.TaskStateCompleted},
		{a2a.TaskStateWorking, a2a.TaskStateFailed},
		{a2a.TaskStateWorking, a2a.TaskStateCanceled},
		{a2a.TaskStateWorking, a2a.TaskStateInputRequired},
		{a2a.TaskStateInputRequired, a2a.TaskStateWorking},
		{a2a.TaskStateInputRequired, a2a.TaskStateCanceled},
	}
	for _, tr := range allowed {
		if !ValidTransition(tr.from, tr.to) {
			t.Errorf("ValidTransition(%s, %s) = false, want true", tr.from, tr.to)
		}
	}
}

func TestTerminalStatesAllowNothing(t *testing.T) {
	terminals := []a2a.TaskState{
		a2a.TaskStateCompleted, a2a.TaskStateFailed,
		a2a.TaskStateCanceled, a2a.TaskStateRejected,
	}
	all := []a2a.TaskState{
		a2a.TaskStateSubmitted, a2a.TaskStateWorking, a2a.TaskStateInputRequired,
		a2a.TaskStateCompleted, a2a.TaskStateFailed, a2a.TaskStateCanceled,
		a2a.TaskStateRejected,
	}
	for _, from := range terminals {
		for _, to := range all {
			if ValidTransition(from, to) {
				t.Errorf("ValidTransition(%s, %s) = true; terminal states are immutable", from, to)
			}
		}
	}
}

func TestBackwardsAndSkippingTransitionsRejected(t *testing.T) {
	invalid := []struct{ from, to a2a.TaskState }{
		{a2a.TaskStateSubmitted, a2a.TaskStateCompleted}, // must pass through WORKING
		{a2a.TaskStateSubmitted, a2a.TaskStateFailed},
		{a2a.TaskStateWorking, a2a.TaskStateSubmitted},
		{a2a.TaskStateWorking, a2a.TaskStateRejected}, // rejection is pre-start only
		{a2a.TaskStateSubmitted, a2a.TaskStateSubmitted},
	}
	for _, tr := range invalid {
		if ValidTransition(tr.from, tr.to) {
			t.Errorf("ValidTransition(%s, %s) = true, want false", tr.from, tr.to)
		}
	}
}
