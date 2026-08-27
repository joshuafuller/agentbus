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
