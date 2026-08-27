package bus

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Sink delivers received message lines to the local agent. Delivery is
// what wakes an idle agent: an inbox append fires a file watcher
// (Claude Code's Monitor), an OnMsg command injects a turn directly
// (codex queue). Notices are never delivered.
type Sink struct {
	Out   io.Writer // human-visible echo; nil to disable
	Inbox string    // file to append message lines to; empty to disable
	OnMsg string    // shell command run per message; empty to disable

	queue chan string
}

// Start begins sequential OnMsg processing. Call once before Deliver.
func (s *Sink) Start() {
	if s.OnMsg == "" {
		return
	}
	s.queue = make(chan string, 256)
	go func() {
		for line := range s.queue {
			from, text, _ := ParseMessage(line)
			// s.OnMsg is the local operator's own --on-msg flag, never
			// remote data. Remote message content reaches the command
			// only through env vars, so it cannot inject into the shell.
			cmd := exec.Command("sh", "-c", s.OnMsg)
			cmd.Env = append(os.Environ(),
				"AGENTBUS_MSG="+line,
				"AGENTBUS_FROM="+from,
				"AGENTBUS_TEXT="+text,
			)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "agentbus: on-msg failed: %v\n", err)
			}
		}
	}()
}

// Deliver handles one received message line.
func (s *Sink) Deliver(line string) {
	if s.Out != nil {
		fmt.Fprintln(s.Out, line)
	}
	if s.Inbox != "" {
		// 0600: the inbox is a plaintext record of bus traffic.
		f, err := os.OpenFile(s.Inbox, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agentbus: inbox: %v\n", err)
		} else {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
	if s.queue != nil {
		select {
		case s.queue <- line:
		default:
			fmt.Fprintln(os.Stderr, "agentbus: on-msg queue full, dropping")
		}
	}
}
