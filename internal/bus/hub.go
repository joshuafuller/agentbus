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

	mu    sync.Mutex
	peers map[net.Conn]peer
}

// maxLineBytes bounds a single bus line. Generous for real messages
// (task descriptions, results) while blocking abuse.
const maxLineBytes = 256 * 1024

type peer struct {
	name    string
	oneshot bool // write-only sender: receives no relays
}

// NewHub returns a hub for a host participating under name.
// sink, if non-nil, receives every relayed message line (never notices).
func NewHub(name string, sink func(line string)) *Hub {
	return &Hub{name: name, sink: sink, peers: make(map[net.Conn]peer)}
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

	h.mu.Lock()
	// A rider's name is its identity: a new join under an existing name
	// supersedes the old connection, so stale wiring (a dead session's
	// leftover join) cannot cause duplicate deliveries. Oneshot senders
	// neither displace nor count.
	var stale net.Conn
	if !oneshot {
		for c, p := range h.peers {
			if p.name == name && !p.oneshot {
				stale = c
				break
			}
		}
		if stale != nil {
			delete(h.peers, stale)
		}
	}
	h.peers[conn] = peer{name: name, oneshot: oneshot}
	n := 1 // the host
	for _, p := range h.peers {
		if !p.oneshot {
			n++
		}
	}
	h.mu.Unlock()
	if stale != nil {
		// Tell the displaced connection why it is going away. Without
		// this its operator sees a bare EOF in the rider log, which is
		// indistinguishable from a network drop — and a displacement is
		// exactly the event an operator most needs to notice, because
		// names are not authenticated (SECURITY.md T2) and the joiner
		// may not be who the name implies. Best effort: the peer may
		// already be gone, and writeLine's deadline bounds the wait.
		writeLine(stale, Notice(fmt.Sprintf(
			"displaced — another connection joined as %s and took this name", name)))
		stale.Close()
	}
	// The welcome confirms registration: once a joiner reads it, the
	// hub is guaranteed to relay to it.
	fmt.Fprintf(conn, "%s\n", Notice(fmt.Sprintf("welcome aboard, %s — %d on the bus", name, n)))
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
	// join: that join already removed it from h.peers and announced the
	// displacement, so suppressing the departure notice here avoids
	// reporting a leave that would read as the *new* holder leaving.
	_, still := h.peers[conn]
	delete(h.peers, conn)
	h.mu.Unlock()
	if !oneshot && still {
		h.notice(fmt.Sprintf("%s hopped off the bus", name), nil)
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
		h.mu.Lock()
		for conn, p := range h.peers {
			if p.oneshot || p.name != to || conn == via {
				continue
			}
			writeLine(conn, line)
		}
		h.mu.Unlock()
		if h.sink != nil && to == h.name && from != h.name {
			h.sink(line)
		}
		return
	}
	line := Message(from, text)
	h.mu.Lock()
	for conn, p := range h.peers {
		if p.oneshot || p.name == from || conn == via {
			continue
		}
		writeLine(conn, line)
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

// writeLine writes one line with a short deadline so a stalled peer
// cannot block the hub for everyone; a timed-out peer's connection
// errors out and it drops off the bus on its own.
func writeLine(conn net.Conn, line string) {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "%s\n", line)
	conn.SetWriteDeadline(time.Time{})
}

// notice sends a system notice to all peers except the excluded conn.
// Notices are not delivered to the local sink, so they never wake an
// agent.
func (h *Hub) notice(text string, except net.Conn) {
	line := Notice(text)
	h.mu.Lock()
	for conn, p := range h.peers {
		// Oneshot senders get no notices: they may never read again,
		// and a blocked write here would stall the whole hub.
		if p.oneshot || conn == except {
			continue
		}
		writeLine(conn, line)
	}
	h.mu.Unlock()
	if h.OnNotice != nil {
		h.OnNotice(line)
	}
}
