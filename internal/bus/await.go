package bus

import (
	"fmt"
	"os"
	"path/filepath"
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
	// The inbox path reaches the filesystem below (both the inbox read
	// and the derived ".pos" file), so guard the taint chain here: every
	// path component must pass the same conservative-charset check used
	// for rider dirs, and "." / ".." components are rejected outright so
	// traversal is impossible.
	for _, comp := range strings.Split(filepath.ToSlash(filepath.Clean(inbox)), "/") {
		// Skip the empty component from a leading "/" (absolute path);
		// Clean has already collapsed any duplicate/inner slashes.
		if comp == "" {
			continue
		}
		if comp == "." || comp == ".." || !ValidName(comp) {
			return nil, fmt.Errorf("invalid inbox path %q", inbox)
		}
	}
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
			// #nosec G703 -- inbox path is validated above: every component
			// must pass ValidName and "." / ".." are rejected, so no
			// traversal is possible; gosec's taint engine cannot see this.
			if err := os.WriteFile(posFile, []byte(strconv.FormatInt(newPos, 10)), 0o600); err != nil {
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
