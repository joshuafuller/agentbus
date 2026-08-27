package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSinkInboxAppends(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	s := &Sink{Inbox: inbox}
	s.Start()
	s.Deliver(Message("codex", "TASK t1 review the diff"))
	s.Deliver(Message("codex", "second line"))

	b, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatal(err)
	}
	want := "[codex] TASK t1 review the diff\n[codex] second line\n"
	if string(b) != want {
		t.Fatalf("inbox = %q, want %q", b, want)
	}
}

// Deliver must report acceptance so callers can withhold the envelope
// ACK when delivery actually failed — an unconditional ACK after a
// dropped queue entry or failed inbox append is permanent message loss
// dressed as success. (PR #18 review, P1.)
func TestSinkDeliverReportsInboxFailure(t *testing.T) {
	// A directory as the inbox path makes the append fail.
	s := &Sink{Inbox: t.TempDir()}
	s.Start()
	if s.Deliver(Message("a", "will not land")) {
		t.Fatal("Deliver claimed acceptance though the inbox append failed")
	}
}

func TestSinkDeliverReportsQueueOverflow(t *testing.T) {
	s := &Sink{OnMsg: "sleep 5"}
	s.Start()
	// One executing + fill the 256-slot queue; the next must refuse.
	accepted := 0
	for i := 0; i < 300; i++ {
		if s.Deliver(Message("a", "x")) {
			accepted++
		}
	}
	if accepted >= 300 {
		t.Fatal("Deliver accepted every line; overflow was silent")
	}
	if accepted == 0 {
		t.Fatal("Deliver accepted nothing")
	}
}

func TestSinkDeliverAcceptsNormally(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	s := &Sink{Inbox: inbox}
	s.Start()
	if !s.Deliver(Message("a", "fine")) {
		t.Fatal("healthy delivery reported failure")
	}
}

func TestSinkOnMsgEnv(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	s := &Sink{OnMsg: `printf '%s|%s|%s\n' "$AGENTBUS_FROM" "$AGENTBUS_TEXT" "$AGENTBUS_MSG" >> ` + out}
	s.Start()
	s.Deliver(Message("alice", `tricky "quoted" $text`))

	deadline := time.Now().Add(2 * time.Second)
	for {
		b, _ := os.ReadFile(out)
		if strings.TrimSpace(string(b)) == `alice|tricky "quoted" $text|[alice] tricky "quoted" $text` {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("on-msg output = %q", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
