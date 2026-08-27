package bus

// Dedup is a fixed-capacity seen-set for envelope ids: at-least-once
// delivery means a receiver can get the same envelope twice (retry
// racing the ACK, or a crash between processing and ACK), and the
// second copy must be re-ACKed but not re-processed. Oldest ids are
// evicted first. Not safe for concurrent use; each receiver loop owns
// one.
type Dedup struct {
	seen  map[string]bool
	order []string
	cap   int
}

// NewDedup returns a dedup window remembering the last n ids.
func NewDedup(n int) *Dedup {
	if n < 1 {
		n = 1 // a non-positive window would panic the eviction path
	}
	return &Dedup{seen: make(map[string]bool, n), cap: n}
}

// Has reports whether id was recorded, without recording it.
func (d *Dedup) Has(id string) bool {
	return d.seen[id]
}

// Seen records id and reports whether it was already present.
func (d *Dedup) Seen(id string) bool {
	if d.seen[id] {
		return true
	}
	if len(d.order) >= d.cap {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	d.seen[id] = true
	d.order = append(d.order, id)
	return false
}
