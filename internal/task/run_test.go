package task

import (
	"errors"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func newTestTask(t *testing.T, prompt string) *a2a.Task {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(prompt))
	return a2a.NewSubmittedTask(msg, msg)
}

func statesOf(snapshots []*a2a.Task) []a2a.TaskState {
	var s []a2a.TaskState
	for _, tk := range snapshots {
		s = append(s, tk.Status.State)
	}
	return s
}

func TestRunCompletesAndReportsEveryTransition(t *testing.T) {
	dir := t.TempDir()
	tk := newTestTask(t, "say hello")
	var seen []*a2a.Task
	var gotPrompt string

	err := Run(dir, tk,
		func(prompt string) (string, error) {
			gotPrompt = prompt
			return "hello from the rider", nil
		},
		func(snap *a2a.Task) { seen = append(seen, snap) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "say hello" {
		t.Fatalf("runner got prompt %q", gotPrompt)
	}

	want := []a2a.TaskState{a2a.TaskStateSubmitted, a2a.TaskStateWorking, a2a.TaskStateCompleted}
	got := statesOf(seen)
	if len(got) != len(want) {
		t.Fatalf("notified states %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notified states %v, want %v", got, want)
		}
	}
	final := seen[len(seen)-1]
	if final.Status.Message == nil || final.Status.Message.Parts[0].Text() != "hello from the rider" {
		t.Fatalf("result not in final status: %+v", final.Status)
	}

	// The terminal state survived to disk.
	stored, err := Load(dir, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("stored state %q, want COMPLETED", stored.Status.State)
	}
}

func TestRunFailureIsExpressible(t *testing.T) {
	dir := t.TempDir()
	tk := newTestTask(t, "explode")
	var seen []*a2a.Task

	err := Run(dir, tk,
		func(string) (string, error) { return "", errors.New("wake command exited 1") },
		func(snap *a2a.Task) { seen = append(seen, snap) },
	)
	if err != nil {
		t.Fatal(err) // a failed TASK is a delivered outcome, not a Run error
	}

	final := seen[len(seen)-1]
	if final.Status.State != a2a.TaskStateFailed {
		t.Fatalf("final state %q, want FAILED", final.Status.State)
	}
	if final.Status.Message == nil || final.Status.Message.Parts[0].Text() != "wake command exited 1" {
		t.Fatalf("failure cause not in status: %+v", final.Status)
	}
	stored, err := Load(dir, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.State != a2a.TaskStateFailed {
		t.Fatalf("stored state %q, want FAILED", stored.Status.State)
	}
}

func TestRunPersistsBeforeNotifying(t *testing.T) {
	dir := t.TempDir()
	tk := newTestTask(t, "x")

	// At every notification the same state must already be on disk:
	// an observer told of a transition can always recover it.
	err := Run(dir, tk,
		func(string) (string, error) { return "ok", nil },
		func(snap *a2a.Task) {
			stored, err := Load(dir, tk.ID)
			if err != nil {
				t.Fatalf("state %s notified before any persist: %v", snap.Status.State, err)
			}
			if stored.Status.State != snap.Status.State {
				t.Fatalf("notified %s but disk has %s", snap.Status.State, stored.Status.State)
			}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}
