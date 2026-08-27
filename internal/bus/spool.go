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
// drained in order on rejoin. Drain hands entries oldest-first to
// accept and removes each entry only AFTER accept returns true; the
// first false stops the drain and leaves that entry and everything
// after it spooled. An entry is therefore never deleted before it has
// been taken for delivery — a drain that outruns the receiver loses
// nothing (PR #15 review, P1).
type Spooler interface {
	Add(rider, line string) error
	Drain(rider string, accept func(line string) bool) (delivered, remaining int, err error)
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

	mu     sync.Mutex
	seq    int   // tie-breaker for same-stamp adds
	lastNs int64 // last stamp issued; stamps never go backwards
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
	// The filename is the sort key, so the stamp must never run
	// backwards: the wall clock can (NTP step, VM resume), and a
	// backwards step would scramble drain order mid-stream. Clamp to
	// strictly increasing within this instance; across restarts the
	// wall anchor keeps rough order and the TTL its meaning.
	ns := time.Now().UnixNano()
	if ns <= s.lastNs {
		ns = s.lastNs + 1
	}
	s.lastNs = ns
	name := fmt.Sprintf("%019d-%06d.line", ns, s.seq)
	s.mu.Unlock()
	tmp := filepath.Join(rdir, name+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	// The spool's one job is surviving a host crash: fsync the entry
	// before the rename, and the directory after, or "durably stored"
	// is only a page-cache promise (PR #14 review).
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(rdir, name)); err != nil {
		return err
	}
	// The directory entry must be durable too, and a failure here is
	// the caller's business: "durably stored" must not be claimed on a
	// best-effort sync (PR #15 review).
	d, err := os.Open(rdir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Drain feeds unexpired spooled lines for a rider to accept, oldest
// first, removing each file only after accept takes the line. Expired
// entries are deleted, not offered. The first refusal ends the drain
// with everything undelivered still on disk.
func (s *FileSpool) Drain(rider string, accept func(line string) bool) (delivered, remaining int, err error) {
	rdir, err := s.riderDir(rider)
	if err != nil {
		return 0, 0, err
	}
	names, err := spoolEntries(rdir)
	if err != nil {
		return 0, 0, err
	}
	for i, name := range names {
		path := filepath.Join(rdir, name)
		if s.expired(name) {
			os.Remove(path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return delivered, len(names) - i, err
		}
		if !accept(string(data)) {
			return delivered, len(names) - i, nil
		}
		delivered++
		if err := os.Remove(path); err != nil {
			return delivered, len(names) - i - 1, err
		}
	}
	return delivered, 0, nil
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

// SweepEvery runs SweepExpired now and then again on each interval
// until the returned stop function is called. A host serves
// indefinitely, so a startup-only sweep would let entries for
// never-returning names outlive the TTL until the next restart —
// which may be never (PR #15 review).
func (s *FileSpool) SweepEvery(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		s.SweepExpired()
		for {
			select {
			case <-t.C:
				s.SweepExpired()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// SweepExpired deletes expired entries for EVERY rider name, so the
// TTL bounds disk usage even for names that never rejoin (misspelled,
// renamed, abandoned). Empty rider dirs are removed. Run it at host
// startup; Drain still expires per-rider on join.
func (s *FileSpool) SweepExpired() error {
	if s.ttl <= 0 {
		return nil
	}
	riders, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, r := range riders {
		if !r.IsDir() {
			continue
		}
		rdir := filepath.Join(s.dir, r.Name())
		names, err := spoolEntries(rdir)
		if err != nil {
			return err
		}
		remaining := len(names)
		for _, name := range names {
			// Only a Remove that actually succeeded shrinks the count,
			// so a failed delete never leads to claiming the dir is
			// empty (PR #15 review).
			if s.expired(name) && os.Remove(filepath.Join(rdir, name)) == nil {
				remaining--
			}
		}
		if remaining == 0 {
			os.Remove(rdir) // fails harmlessly if non-empty
		}
	}
	return nil
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
