package main

import (
	"reflect"
	"testing"
)

// A task worker finishing while the join is between sessions must not
// lose its output to a dead socket (#48 review, P1): lines sent while
// detached queue, and flush in order when the next session attaches.
func TestReconnectWriterQueuesWhileDetached(t *testing.T) {
	w := &reconnectWriter{}
	var got []string
	sink := func(line string) { got = append(got, line) }

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
