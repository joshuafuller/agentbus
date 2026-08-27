package bus

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"
)

// Hub is the host-side relay: every line from one peer goes to every
// other peer (by name, so a peer's send and receive connections under
// the same name never echo back) and to the local sink.
type Hub struct {
	name string            // the host's own participant name
	sink func(line string) // local delivery of message lines; may be nil

	// OnNotice, if non-nil, receives system notice lines (joins and
	// leaves) for local display. Kept separate from sink so notices are
	// visible to the human at the host but never wake an agent.
	OnNotice func(line string)

	// Spool, if non-nil, durably stores addressed lines whose target
	// holds no live connection, for delivery when that name joins
	// (Gate 3, issue #7). Without a spool such lines are lost — either
	// way the hub says so on the feed rather than staying silent.
	Spool Spooler

	// TaskNotice, if non-nil, inspects each addressed payload the hub
	// relays and may return a feed notice describing it — how task
	// lifecycle transitions become visible to every driver (issue #12)
	// without the payload leaving its addressed path. Injected by the
	// caller because the hub does not know the task wire format; the
	// notice path guarantees it can never wake a rider.
	TaskNotice func(from, to, payload string) (string, bool)

	mu    sync.Mutex
	peers map[net.Conn]peer
}

// maxLineBytes bounds a single bus line. Generous for real messages
// (task descriptions, results) while blocking abuse.
const maxLineBytes = 256 * 1024

// outboxDepth bounds how far behind a peer may fall before it is
// dropped: enqueue is non-blocking, so a stalled reader costs the bus
// nothing — it costs the stalled peer its seat.
const outboxDepth = 256

// catchUpHeadroom is outbox capacity a spool drain leaves free for
// live traffic, so a large catch-up cannot push its own rider over the
// slow-consumer drop threshold.
const catchUpHeadroom = 64

type peer struct {
	name    string
	oneshot bool        // write-only sender: receives no relays
	out     chan string // per-peer write queue; one writer goroutine drains it
}

// NewHub returns a hub for a host participating under name.
// sink, if non-nil, receives every relayed message line (never notices).
func NewHub(name string, sink func(line string)) *Hub {
	return &Hub{name: name, sink: sink, peers: make(map[net.Conn]peer)}
}

// enqueue hands one line to a peer's writer without ever blocking the
// hub: every write to a conn goes through its outbox and its single
// writer goroutine, so a peer that stopped reading stalls only itself.
// A full outbox means the peer is outboxDepth lines behind; its conn is
// closed (its Serve then unregisters it) rather than letting it wedge
// the bus — the review finding behind this design was one non-reading
// peer freezing every participant for the write deadline (PR #9).
// Callers hold h.mu, which makes enqueue order the wire order.
func enqueue(conn net.Conn, p peer, line string) {
	select {
	case p.out <- line:
	default:
		conn.Close()
	}
}

// writePeer is a peer's single writer: it drains the outbox in order.
// On a write error it closes the conn (unblocking the peer's Serve,
// which unregisters it and closes the outbox) and discards the rest.
func writePeer(conn net.Conn, out <-chan string) {
	defer conn.Close()
	broken := false
	for line := range out {
		if broken {
			continue
		}
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprintf(conn, "%s\n", line); err != nil {
			broken = true
			conn.Close()
		}
	}
}

// Serve owns conn: it reads the HELLO, registers the peer, relays its
// lines until EOF, then unregisters it. It blocks; run in a goroutine.
func (h *Hub) Serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	// Cap line length so one rider cannot force unbounded buffering in
	// the hub or an unbounded append to every rider's inbox. A line
	// over the cap ends that connection; it does not stall the bus.
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	if !sc.Scan() {
		return
	}
	name, oneshot, ok := ParseHello(sc.Text())
	if !ok {
		return
	}

	out := make(chan string, outboxDepth)
	go writePeer(conn, out)

	h.mu.Lock()
	// A rider's name is its identity: a new join under an existing name
	// supersedes the old connection, so stale wiring (a dead session's
	// leftover join) cannot cause duplicate deliveries. Oneshot senders
	// neither displace nor count.
	var stale net.Conn
	var stalePeer peer
	if !oneshot {
		for c, p := range h.peers {
			if p.name == name && !p.oneshot {
				stale, stalePeer = c, p
				break
			}
		}
		if stale != nil {
			delete(h.peers, stale)
		}
	}
	me := peer{name: name, oneshot: oneshot, out: out}
	h.peers[conn] = me
	n := 1 // the host
	for _, p := range h.peers {
		if !p.oneshot {
			n++
		}
	}
	// The welcome confirms registration: once a joiner reads it, the
	// hub is guaranteed to relay to it. Enqueued under the same lock
	// that orders every other line to this conn, so the welcome is
	// always the FIRST line a client reads — another peer's concurrent
	// join notice cannot interleave ahead of it. Clients (send, task)
	// rely on first-line-is-welcome.
	enqueue(conn, me, Notice(fmt.Sprintf("welcome aboard, %s — %d on the bus", name, n)))
	// Catch-up before live traffic: lines spooled while this name was
	// away flush now, oldest first, on this connection only. Under the
	// same lock as registration and as deliver()'s absence-check+add,
	// so the drain and the spool add are strictly ordered — a line goes
	// to the live conn or to the spool this drain will read, never to a
	// spool nobody drains — and no live line can interleave into the
	// middle of the catch-up.
	// Catch-up may exceed one outbox: accept only while headroom
	// remains (so live traffic enqueued after the drain cannot push the
	// peer over the drop threshold), and let Drain keep the remainder
	// on disk — it removes an entry only after we take it, so a drain
	// that outruns the outbox loses nothing.
	var drained, left int
	var drainErr error
	if !oneshot && h.Spool != nil {
		drained, left, drainErr = h.Spool.Drain(name, func(l string) bool {
			if len(me.out) >= outboxDepth-catchUpHeadroom {
				return false
			}
			select {
			case me.out <- l:
				return true
			default:
				return false
			}
		})
	}
	h.mu.Unlock()
	if drainErr != nil {
		h.notice(fmt.Sprintf("spool drain for %s failed: %v", name, drainErr), nil)
	}
	if left > 0 {
		h.notice(fmt.Sprintf("%s caught up %d spooled lines — %d still pending, rejoin to collect the rest", name, drained, left), nil)
	}
	if stale != nil {
		// Tell the displaced connection why it is going away. Without
		// this its operator sees a bare EOF in the rider log, which is
		// indistinguishable from a network drop — and a displacement is
		// exactly the event an operator most needs to notice, because
		// names are not authenticated (SECURITY.md T2) and the joiner
		// may not be who the name implies. Best effort: its writer
		// flushes what it can and closes. The stale peer is out of
		// h.peers, so this goroutine is the only remaining sender and
		// may close the outbox.
		enqueue(stale, stalePeer, Notice(fmt.Sprintf(
			"displaced — another connection joined as %s and took this name", name)))
		close(stalePeer.out)
	}
	if !oneshot {
		if stale != nil {
			// Say what happened, not what it usually means. "reconnected"
			// describes the common case (a rider's host woke up) but reads
			// identically when the joiner is a different party taking the
			// name, which unauthenticated names permit. Naming the
			// displacement costs nothing in the benign case and removes
			// the disguise in the hostile one. Prevention — a returning
			// rider proving itself with a key — is issue #6.
			h.notice(fmt.Sprintf(
				"%s joined, displacing an existing connection under that name", name), conn)
		} else {
			h.notice(fmt.Sprintf("%s hopped on the bus", name), conn)
		}
	}

	for sc.Scan() {
		h.deliver(name, sc.Text(), conn)
	}

	h.mu.Lock()
	// still is false when this conn was displaced by a later same-name
	// join: that join already removed it from h.peers (and owns closing
	// its outbox), so suppressing the departure notice here avoids
	// reporting a leave that would read as the *new* holder leaving.
	_, still := h.peers[conn]
	delete(h.peers, conn)
	h.mu.Unlock()
	if still {
		close(out) // the writer flushes and closes the conn
		if !oneshot {
			h.notice(fmt.Sprintf("%s hopped off the bus", name), nil)
		}
	}
}

// Broadcast sends a message line from the host itself to all peers.
func (h *Hub) Broadcast(text string) {
	h.deliver(h.name, text, nil)
}

// Peers returns the names currently on the bus.
func (h *Hub) Peers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.peers))
	for _, p := range h.peers {
		if !p.oneshot {
			names = append(names, p.name)
		}
	}
	return names
}

// deliver relays a message from sender name `from` to every peer with a
// different name and to the local sink. `via` is the originating conn.
// An addressed line (TO <name> <payload>) goes only to peers holding
// that name — or only to the sink when addressed to the host — so an
// addressed delivery never wakes an uninvolved agent (ADR 0003).
func (h *Hub) deliver(from, text string, via net.Conn) {
	if to, payload, ok := ParseAddressed(text); ok {
		line := Message(from, payload)
		delivered := 0
		var spoolNotice string
		h.mu.Lock()
		for conn, p := range h.peers {
			if p.oneshot || p.name != to || conn == via {
				continue
			}
			enqueue(conn, p, line)
			delivered++
		}
		if to == h.name {
			// The host is always present; its sink is its delivery.
			delivered++
		}
		if delivered == 0 {
			// Nobody holds that name. Spool for its return, or at least
			// name the loss — a silent drop is the failure mode this
			// project exists to kill (issue #8, ADR 0004). The spool add
			// happens under h.mu, the same lock a join's drain holds, so
			// the absence check and the add are one atomic step: a line
			// can never land in the spool after the drain that should
			// have delivered it. Disk I/O under the hub lock is the
			// price of that atomicity; entries are one small line each.
			if h.Spool != nil {
				if err := h.Spool.Add(to, line); err != nil {
					spoolNotice = fmt.Sprintf("could not spool for %s: %v — line lost", to, err)
				} else {
					spoolNotice = fmt.Sprintf("%s is away — line spooled (%d pending)", to, h.Spool.Pending(to))
				}
			} else {
				spoolNotice = fmt.Sprintf("nobody holds the name %s — line dropped (no spool)", to)
			}
		}
		h.mu.Unlock()
		if delivered > 0 && to == h.name && h.sink != nil && from != h.name {
			h.sink(line)
		}
		if spoolNotice != "" {
			h.notice(spoolNotice, nil)
		}
		if h.TaskNotice != nil {
			if n, ok := h.TaskNotice(from, to, payload); ok {
				h.notice(n, nil)
			}
		}
		return
	}
	line := Message(from, text)
	h.mu.Lock()
	for conn, p := range h.peers {
		if p.oneshot || p.name == from || conn == via {
			continue
		}
		enqueue(conn, p, line)
	}
	h.mu.Unlock()
	// The local sink is the host's own participation as the peer named
	// h.name, so `from != h.name` is its same-name self-filter — the sink
	// counterpart of the `p.name == from` guard in the peer loop above.
	// The two paths enforce one invariant (a participant never receives
	// its own name's messages) via different predicates; they coincide
	// only because the sink's participant name is h.name. Keep that true:
	// if the sink is ever given a name distinct from h.name, this guard
	// must compare against that name instead.
	if h.sink != nil && from != h.name {
		h.sink(line)
	}
}

// notice sends a system notice to all peers except the excluded conn.
// Notices are not delivered to the local sink, so they never wake an
// agent.
func (h *Hub) notice(text string, except net.Conn) {
	line := Notice(text)
	h.mu.Lock()
	for conn, p := range h.peers {
		// Oneshot senders get no notices: they may never read again,
		// and their outbox would only fill toward a disconnect.
		if p.oneshot || conn == except {
			continue
		}
		enqueue(conn, p, line)
	}
	h.mu.Unlock()
	if h.OnNotice != nil {
		h.OnNotice(line)
	}
}
