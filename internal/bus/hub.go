package bus

import (
	"bufio"
	"crypto/ed25519"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Hub is the host-side relay: every line from one peer goes to every
// other peer (by name, so a peer's send and receive connections under
// the same name never echo back) and to the local sink.
type Hub struct {
	name string                 // the host's own participant name
	sink func(line string) bool // local delivery; reports acceptance; may be nil

	// OnNotice, if non-nil, receives system notice lines (joins and
	// leaves) for local display. Kept separate from sink so notices are
	// visible to the human at the host but never wake an agent.
	OnNotice func(line string)

	// Spool, if non-nil, durably stores addressed lines whose target
	// holds no live connection, for delivery when that name joins
	// (Gate 3, issue #7). Without a spool such lines are lost — either
	// way the hub says so on the feed rather than staying silent.
	Spool Spooler

	// RetryInterval is how long an offered envelope may go unACKed
	// before the pump redelivers it. Zero means the 5s default.
	RetryInterval time.Duration

	// TaskNotice, if non-nil, inspects each addressed payload the hub
	// relays and may return a feed notice describing it — how task
	// lifecycle transitions become visible to every driver (issue #12)
	// without the payload leaving its addressed path. Injected by the
	// caller because the hub does not know the task wire format; the
	// notice path guarantees it can never wake a rider.
	TaskNotice func(from, to, payload string) (string, bool)

	mu    sync.Mutex
	peers map[net.Conn]peer

	// bindings is the TOFU table (issue #6): name → the public key that
	// first claimed it, for the life of the bus. A bound name accepts
	// only connections that prove possession of that key via the fresh
	// per-connection challenge; the ticket admits, the key identifies.
	bindings map[string]ed25519.PublicKey
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
	oneshot bool          // write-only sender: receives no relays
	out     chan string   // per-peer write queue; one writer goroutine drains it
	kick    chan struct{} // wakes the pump when a new addressed line spools
	acks    chan string   // envelope ids the peer has acknowledged
}

// NewHub returns a hub for a host participating under name.
// sink, if non-nil, receives every relayed message line (never notices).
func NewHub(name string, sink func(line string) bool) *Hub {
	return &Hub{name: name, sink: sink, peers: make(map[net.Conn]peer),
		bindings: make(map[string]ed25519.PublicKey)}
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
	name, oneshot, pub, ok := ParseHelloKeyed(sc.Text())
	if !ok {
		return
	}

	// Identity gate (issue #6). Runs BEFORE registration, so a refused
	// connection never displaces the legitimate holder and the direct
	// writes below race nothing. Three cases:
	//   keyed hello  → fresh-nonce challenge; the signature must verify
	//                  and, if the name is bound, the key must match.
	//   unkeyed, name bound → refused: the name belongs to a key now.
	//   unkeyed, unbound    → legacy path, unchanged.
	if pub != nil {
		nonce, err := NewNonce()
		if err != nil {
			return
		}
		conn.SetDeadline(time.Now().Add(15 * time.Second))
		fmt.Fprintf(conn, "%s\n", Challenge(nonce))
		if !sc.Scan() {
			return
		}
		sig, sok := ParseSig(sc.Text())
		// The deadline set above stays armed through every refusal
		// write below: a refused client that stops reading must time
		// out, not park this goroutine forever (PR #20 review).
		if !sok || !VerifyChallenge(pub, nonce, name, sig) {
			fmt.Fprintf(conn, "%s\n", Notice("refused — signature does not prove the presented key"))
			h.notice(fmt.Sprintf("refused a join as %s: bad signature", name), nil)
			return
		}
		h.mu.Lock()
		bound, has := h.bindings[name]
		switch {
		case has && !bound.Equal(pub):
			h.mu.Unlock()
			fmt.Fprintf(conn, "%s\n", Notice("refused — "+name+" is bound to a different key"))
			h.notice(fmt.Sprintf("refused a join as %s: wrong key for a bound name", name), nil)
			return
		case !has && !oneshot:
			// Trust on first use: the first RIDER to claim the name
			// binds it. Oneshot senders prove keys but never bind.
			h.bindings[name] = pub
			h.mu.Unlock()
			h.notice(fmt.Sprintf("%s is now key-bound (trust on first use)", name), nil)
		default:
			h.mu.Unlock()
		}
		conn.SetDeadline(time.Time{}) // handshake passed
	} else {
		h.mu.Lock()
		_, has := h.bindings[name]
		h.mu.Unlock()
		if has {
			// Bounded refusal write, same rationale as above.
			conn.SetDeadline(time.Now().Add(15 * time.Second))
			fmt.Fprintf(conn, "%s\n", Notice("refused — "+name+" is key-bound; connect with its key"))
			h.notice(fmt.Sprintf("refused an unkeyed connection as %s: name is key-bound", name), nil)
			return
		}
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
	me := peer{name: name, oneshot: oneshot, out: out,
		kick: make(chan struct{}, 1), acks: make(chan string, outboxDepth)}
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
	h.mu.Unlock()
	// Delivery for this peer — backlog catch-up AND live addressed
	// lines — is one mechanism: the pump. Everything addressed goes
	// through the spool; the pump offers entries oldest-first as
	// envelopes and the spool forgets an entry only when the rider's
	// ACK arrives (issue #7, ADR 0004).
	if !oneshot && h.Spool != nil {
		go h.pump(conn, me, name)
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
		line := sc.Text()
		// ACKs are control traffic between this peer and its pump —
		// consumed here, never relayed.
		if id, ok := ParseAck(line); ok {
			select {
			case me.acks <- id:
			default: // pump far behind; the retry path re-converges
			}
			continue
		}
		h.deliver(name, line, conn)
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

// pump is a rider's delivery loop: it lives as long as the peer does,
// offering spooled entries oldest-first as envelopes, redelivering
// what goes unACKed past RetryInterval, and removing an entry only
// when the rider's ACK arrives. Each pass is bounded (a headroom's
// worth of sends, never past the outbox threshold) and its Offer pass
// runs under h.mu, which serializes SENDING with registration and
// removal — the pump can only send while its peer is registered, so it
// never writes to a closed outbox. ACK-driven Removes run outside the
// lock: FileSpool operations are safe concurrently, and only this
// goroutine removes for this name. Between passes it sleeps until kicked by a new
// Add, an ACK, or the retry half-interval.
func (h *Hub) pump(conn net.Conn, me peer, name string) {
	inflight := map[string]time.Time{}
	retry := h.RetryInterval
	if retry <= 0 {
		retry = 5 * time.Second
	}
	for {
		// Absorb pending ACKs first: forget the entry durably.
		for {
			select {
			case id := <-me.acks:
				if err := h.Spool.Remove(name, id); err != nil && !os.IsNotExist(err) {
					h.notice(fmt.Sprintf("could not clear acked entry %s for %s: %v", id, name, err), nil)
				}
				delete(inflight, id)
				continue
			default:
			}
			break
		}

		h.mu.Lock()
		if _, alive := h.peers[conn]; !alive {
			h.mu.Unlock()
			return
		}
		now := time.Now()
		sent := 0
		err := h.Spool.Offer(name, func(id, line string) bool {
			if sent >= outboxDepth-catchUpHeadroom || len(me.out) >= outboxDepth-catchUpHeadroom {
				return false
			}
			if dl, in := inflight[id]; in && now.Before(dl) {
				return true // in flight and not yet due for retry: skip
			}
			from, payload, ok := ParseMessage(line)
			if !ok {
				// Not a message line: unrecoverable garbage; drop it,
				// and its inflight record with it.
				h.Spool.Remove(name, id)
				delete(inflight, id)
				return true
			}
			select {
			case me.out <- Message(from, Envelope(id, payload)):
				inflight[id] = now.Add(retry)
				sent++
				return true
			default:
				return false
			}
		})
		h.mu.Unlock()
		if err != nil {
			h.notice(fmt.Sprintf("spool offer for %s failed: %v", name, err), nil)
		}

		select {
		case <-me.kick:
		case id := <-me.acks:
			if err := h.Spool.Remove(name, id); err != nil && !os.IsNotExist(err) {
				h.notice(fmt.Sprintf("could not clear acked entry %s for %s: %v", id, name, err), nil)
			}
			delete(inflight, id)
		case <-time.After(retry / 2):
		}
	}
}

// AckLocal acknowledges a host-addressed envelope: the host durably
// accepted it, so the spool may forget it.
func (h *Hub) AckLocal(id string) {
	if h.Spool == nil {
		return
	}
	if err := h.Spool.Remove(h.name, id); err != nil && !os.IsNotExist(err) {
		h.notice(fmt.Sprintf("could not clear host entry %s: %v", id, err), nil)
	}
}

// DrainLocal re-offers unacked host-addressed entries to the sink,
// oldest first — the host's catch-up after a restart. Stops at the
// first refusal.
func (h *Hub) DrainLocal() {
	if h.Spool == nil || h.sink == nil {
		return
	}
	err := h.Spool.Offer(h.name, func(id, line string) bool {
		from, payload, ok := ParseMessage(line)
		if !ok {
			h.Spool.Remove(h.name, id) // garbage entry
			return true
		}
		return h.sink(Message(from, Envelope(id, payload)))
	})
	if err != nil {
		h.notice(fmt.Sprintf("host spool drain failed: %v", err), nil)
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
		var spoolNotice string
		toHost := to == h.name
		h.mu.Lock()
		var hostEnvelope string
		switch {
		case toHost && h.Spool != nil:
			// The host gets the same durable-first contract as remote
			// riders (PR #18 review): spool, deliver enveloped, forget
			// only on AckLocal. (Sink call after unlock.)
			id, err := h.Spool.Add(to, line)
			if err != nil {
				spoolNotice = fmt.Sprintf("could not spool for %s: %v — line lost", to, err)
				break
			}
			hostEnvelope = Message(from, Envelope(id, payload))
		case toHost:
			// Legacy no-spool hub: direct sink delivery.
		case h.Spool != nil:
			// Durable first, always: every addressed line lands in the
			// spool, the target's pump delivers it as an envelope, and
			// the entry survives until the rider's ACK (issue #7,
			// ADR 0004). Ordering falls out: the spool is the single
			// queue, oldest first. Disk I/O under h.mu is the price of
			// making the add atomic with join/leave; one small line each.
			if _, err := h.Spool.Add(to, line); err != nil {
				spoolNotice = fmt.Sprintf("could not spool for %s: %v — line lost", to, err)
				break
			}
			present := false
			for conn, p := range h.peers {
				if !p.oneshot && p.name == to && conn != via {
					present = true
					select {
					case p.kick <- struct{}{}:
					default: // pump already awake
					}
				}
			}
			if !present {
				spoolNotice = fmt.Sprintf("%s is away — line spooled (%d pending)", to, h.Spool.Pending(to))
			}
		default:
			// Legacy no-spool hub: best-effort direct delivery, loudly
			// lossy when nobody holds the name.
			delivered := 0
			for conn, p := range h.peers {
				if p.oneshot || p.name != to || conn == via {
					continue
				}
				enqueue(conn, p, line)
				delivered++
			}
			if delivered == 0 {
				spoolNotice = fmt.Sprintf("nobody holds the name %s — line dropped (no spool)", to)
			}
		}
		h.mu.Unlock()
		if toHost && h.sink != nil && from != h.name {
			if hostEnvelope != "" {
				// Acceptance is signalled via AckLocal (after the host
				// durably has it); a refusal leaves the entry for
				// DrainLocal on the next start.
				h.sink(hostEnvelope)
			} else {
				h.sink(line)
			}
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
