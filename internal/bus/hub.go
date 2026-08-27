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
	name string             // the host's own participant name
	sink func(line string)  // local delivery of message lines; may be nil

	// OnNotice, if non-nil, receives system notice lines (joins and
	// leaves) for local display. Kept separate from sink so notices are
	// visible to the human at the host but never wake an agent.
	OnNotice func(line string)

	mu    sync.Mutex
	peers map[net.Conn]peer
}

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
		stale.Close()
	}
	// The welcome confirms registration: once a joiner reads it, the
	// hub is guaranteed to relay to it.
	fmt.Fprintf(conn, "%s\n", Notice(fmt.Sprintf("welcome aboard, %s — %d on the bus", name, n)))
	if !oneshot {
		if stale != nil {
			h.notice(fmt.Sprintf("%s reconnected", name), conn)
		} else {
			h.notice(fmt.Sprintf("%s hopped on the bus", name), conn)
		}
	}

	for sc.Scan() {
		h.deliver(name, sc.Text(), conn)
	}

	h.mu.Lock()
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
func (h *Hub) deliver(from, text string, via net.Conn) {
	line := Message(from, text)
	h.mu.Lock()
	for conn, p := range h.peers {
		if p.oneshot || p.name == from || conn == via {
			continue
		}
		writeLine(conn, line)
	}
	h.mu.Unlock()
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
