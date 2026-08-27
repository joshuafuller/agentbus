package bus

import (
	"bufio"
	"fmt"
	"net"
	"sync"
)

// Hub is the host-side relay: every line from one peer goes to every
// other peer (by name, so a peer's send and receive connections under
// the same name never echo back) and to the local sink.
type Hub struct {
	name string             // the host's own participant name
	sink func(line string)  // local delivery of message lines; may be nil

	mu    sync.Mutex
	peers map[net.Conn]string
}

// NewHub returns a hub for a host participating under name.
// sink, if non-nil, receives every relayed message line (never notices).
func NewHub(name string, sink func(line string)) *Hub {
	return &Hub{name: name, sink: sink, peers: make(map[net.Conn]string)}
}

// Serve owns conn: it reads the HELLO, registers the peer, relays its
// lines until EOF, then unregisters it. It blocks; run in a goroutine.
func (h *Hub) Serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		return
	}
	name, ok := ParseHello(sc.Text())
	if !ok {
		return
	}

	h.mu.Lock()
	h.peers[conn] = name
	n := len(h.peers) + 1 // peers plus the host
	h.mu.Unlock()
	// The welcome confirms registration: once a joiner reads it, the
	// hub is guaranteed to relay to it.
	fmt.Fprintf(conn, "%s\n", Notice(fmt.Sprintf("welcome aboard, %s — %d on the bus", name, n)))
	h.notice(fmt.Sprintf("%s hopped on the bus", name), conn)

	for sc.Scan() {
		h.deliver(name, sc.Text(), conn)
	}

	h.mu.Lock()
	delete(h.peers, conn)
	h.mu.Unlock()
	h.notice(fmt.Sprintf("%s hopped off the bus", name), nil)
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
	for _, n := range h.peers {
		names = append(names, n)
	}
	return names
}

// deliver relays a message from sender name `from` to every peer with a
// different name and to the local sink. `via` is the originating conn.
func (h *Hub) deliver(from, text string, via net.Conn) {
	line := Message(from, text)
	h.mu.Lock()
	for conn, name := range h.peers {
		if name == from || conn == via {
			continue
		}
		fmt.Fprintf(conn, "%s\n", line)
	}
	h.mu.Unlock()
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
	defer h.mu.Unlock()
	for conn := range h.peers {
		if conn == except {
			continue
		}
		fmt.Fprintf(conn, "%s\n", line)
	}
}
