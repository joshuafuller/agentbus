package main

import (
	"fmt"
	"reflect"
	"testing"
)

// A task worker finishing while the join is between sessions must not
// lose its output to a dead socket (#48 review, P1): lines sent while
// detached queue, and flush in order when the next session attaches.
func TestReconnectWriterQueuesWhileDetached(t *testing.T) {
	w := &reconnectWriter{}
	var got []string
	sink := func(line string) error { got = append(got, line); return nil }

	w.Send("lost-without-queueing-1")
	w.Send("lost-without-queueing-2")
	w.Attach(sink)
	w.Send("live-1")

	want := []string{"lost-without-queueing-1", "lost-without-queueing-2", "live-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// Detach: queueing resumes; a second attach flushes again.
	w.Attach(nil)
	w.Send("mid-reconnect")
	if len(got) != 3 {
		t.Fatalf("line leaked to a detached writer: %v", got)
	}
	w.Attach(sink)
	if want = append(want, "mid-reconnect"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after reattach got %v, want %v", got, want)
	}
}

// The #49 review race: a write that FAILS (socket died between the
// writer copy and the write) must re-queue the line, not drop it — the
// old ignored-error path lost terminal task snapshots permanently.
func TestReconnectWriterRequeuesFailedWrite(t *testing.T) {
	w := &reconnectWriter{}
	dead := func(string) error { return fmt.Errorf("write on closed conn") }
	w.Attach(dead)
	w.Send("must-survive")

	var got []string
	w.Attach(func(line string) error { got = append(got, line); return nil })
	if !reflect.DeepEqual(got, []string{"must-survive"}) {
		t.Fatalf("failed write was dropped: got %v", got)
	}
}

// A flush that fails mid-way re-queues the unflushed lines for the
// next attach instead of losing them.
func TestReconnectWriterRequeuesFailedFlush(t *testing.T) {
	w := &reconnectWriter{}
	w.Send("q1")
	w.Send("q2")
	w.Attach(func(string) error { return fmt.Errorf("still dead") })

	var got []string
	w.Attach(func(line string) error { got = append(got, line); return nil })
	if !reflect.DeepEqual(got, []string{"q1", "q2"}) {
		t.Fatalf("flush failure lost lines: got %v", got)
	}
}
