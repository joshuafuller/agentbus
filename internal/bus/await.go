package bus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Await blocks until inbox holds at least one complete line past the
// offset recorded in inbox+".pos", returns those lines, and advances
// the recorded offset. If unread lines already exist it returns them
// immediately — so a message that arrived before Await started is
// never missed. This is the whole activation contract in one call:
// an agent runs it in the background and is woken by its completion.
func Await(inbox string, poll time.Duration) ([]string, error) {
	posFile := inbox + ".pos"
	pos := readPos(posFile)
	for {
		data, err := os.ReadFile(inbox)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if int64(len(data)) < pos {
			pos = 0 // inbox truncated or replaced; start over
		}
		chunk := data[pos:]
		// Only consume complete lines; a partial write stays for next time.
		if i := strings.LastIndexByte(string(chunk), '\n'); i >= 0 {
			lines := strings.Split(strings.TrimRight(string(chunk[:i+1]), "\n"), "\n")
			newPos := pos + int64(i) + 1
			if err := os.WriteFile(posFile, []byte(strconv.FormatInt(newPos, 10)), 0o644); err != nil {
				return nil, fmt.Errorf("recording read position: %w", err)
			}
			return lines, nil
		}
		time.Sleep(poll)
	}
}

func readPos(posFile string) int64 {
	b, err := os.ReadFile(posFile)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
