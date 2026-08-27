package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Spooler is what the hub needs from a spool: durable per-rider lines,
// drained in order on rejoin.
type Spooler interface {
	Add(rider, line string) error
	Drain(rider string) ([]string, error)
	Pending(rider string) int
}

// FileSpool is the host-side durable queue behind offline delivery
// (Gate 3, issue #7): one line per file under <dir>/<rider>/, named by
// a sortable nanosecond timestamp so drain order is send order. Entries
// carry their age in the filename and expire at drain time — no
// background sweeper (the TTL-in-the-name shape is borrowed from prior
// art per ADR 0004). Files are 0600 in 0700 dirs: spooled lines are
// bus traffic at rest.
type FileSpool struct {
	dir string
	ttl time.Duration

	mu  sync.Mutex
	seq int // tie-breaker for same-nanosecond adds
}

// NewFileSpool returns a spool rooted at dir. ttl bounds how long an
// entry waits for its rider; zero or negative means keep forever.
func NewFileSpool(dir string, ttl time.Duration) *FileSpool {
	return &FileSpool{dir: dir, ttl: ttl}
}

// Add durably stores one line for a rider that is not connected.
func (s *FileSpool) Add(rider, line string) error {
	rdir, err := s.riderDir(rider)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(rdir, 0o700); err != nil {
		return err
	}
	s.mu.Lock()
	s.seq++
	name := fmt.Sprintf("%019d-%06d.line", time.Now().UnixNano(), s.seq)
	s.mu.Unlock()
	tmp := filepath.Join(rdir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(line), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(rdir, name))
}

// Drain returns and removes every unexpired spooled line for a rider,
// oldest first. Expired entries are deleted, not returned.
func (s *FileSpool) Drain(rider string) ([]string, error) {
	rdir, err := s.riderDir(rider)
	if err != nil {
		return nil, err
	}
	names, err := spoolEntries(rdir)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, name := range names {
		path := filepath.Join(rdir, name)
		if s.expired(name) {
			os.Remove(path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return lines, err
		}
		lines = append(lines, string(data))
		if err := os.Remove(path); err != nil {
			return lines, err
		}
	}
	return lines, nil
}

// Pending reports how many lines wait for a rider (expired included —
// it is a cheap count for notices, not a promise of delivery).
func (s *FileSpool) Pending(rider string) int {
	rdir, err := s.riderDir(rider)
	if err != nil {
		return 0
	}
	names, err := spoolEntries(rdir)
	if err != nil {
		return 0
	}
	return len(names)
}

func (s *FileSpool) riderDir(rider string) (string, error) {
	// Rider names come off the wire; ValidName is the same gate the
	// rest of the system applies before a name touches the filesystem.
	if !ValidName(rider) {
		return "", fmt.Errorf("invalid rider name %q", rider)
	}
	return filepath.Join(s.dir, rider), nil
}

func (s *FileSpool) expired(name string) bool {
	if s.ttl <= 0 {
		return false
	}
	ns, ok := entryTime(name)
	if !ok {
		return true // unparsable entry: treat as garbage
	}
	return time.Since(time.Unix(0, ns)) > s.ttl
}

func spoolEntries(rdir string) ([]string, error) {
	entries, err := os.ReadDir(rdir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".line") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func entryTime(name string) (int64, bool) {
	base, found := strings.CutSuffix(name, ".line")
	if !found {
		return 0, false
	}
	ts, _, found := strings.Cut(base, "-")
	if !found {
		return 0, false
	}
	var ns int64
	if _, err := fmt.Sscanf(ts, "%d", &ns); err != nil {
		return 0, false
	}
	return ns, true
}
