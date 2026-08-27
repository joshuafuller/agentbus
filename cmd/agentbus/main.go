// Command agentbus is a tiny multi-agent message bus over tailcat.
//
// One machine hosts the bus and prints a ticket. Any number of agents
// (or humans) join with that ticket from anywhere; every line one
// participant sends is relayed to all the others. Received lines can
// be appended to an inbox file (to fire a file watcher such as Claude
// Code's Monitor) or handed to a command (such as codex queue) so an
// idle agent starts working without a human turn.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
	"github.com/joshuafuller/agentbus/internal/task"
	"github.com/tailscale/tailcat"
)

// busPort is the virtual TCP port the bus speaks on inside the tunnel.
const busPort = 2255

// heartbeatEvery is how often a long-lived connection pings the hub.
// A var so tests can shrink it; must stay well under the hub's default
// QuietAfter or every healthy rider gets flagged unresponsive.
var heartbeatEvery = 30 * time.Second

const usage = `agentbus — a message bus for AI agents. One ticket, any number of riders.

Usage:
  agentbus host [flags]                 start a bus, print its ticket
  agentbus join <ticket> [flags]        ride the bus (stays connected)
  agentbus send <ticket> [flags] <msg>  send one message and exit
  agentbus version                      print version information
  agentbus task <ticket> <rider> <msg>  send an A2A task to one rider and
                                        follow it to completion or failure
  agentbus put <ticket> <rider> <file>  stream a file to one rider out of
                                        band; the agent sees one FILE line
  agentbus invite <ticket> [flags]      print a copy-paste boarding pass
                                        that onboards a fresh agent
  agentbus await [--inbox <file>]       block until unread messages exist,
                                        print them, remember what was read
  agentbus wire <claude|codex> <ticket> [flags]
                                        set up complete wake wiring: rider
                                        conversation + detached join that
                                        resumes it per message

Flags:
  --name <name>     participant name (default: hostname)
  --inbox <file>    append received messages to this file
  --on-msg <cmd>    run this shell command per received message;
                    the message is in $AGENTBUS_MSG, $AGENTBUS_FROM, $AGENTBUS_TEXT

Examples:
  agentbus host --name hub --inbox ~/.agentbus/inbox
  agentbus join tc0abc... --name codex-1 --on-msg 'codex queue --thread "$T" --message "$AGENTBUS_MSG"'
  agentbus send tc0abc... --name claude "DONE task-1: refactor is green"
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	name := fs.String("name", defaultName(), "participant name")
	inbox := fs.String("inbox", "", "append received messages to this file")
	onMsg := fs.String("on-msg", "", "shell command run per received message")

	// Names reach the wire protocol, the filesystem, and (via wire)
	// shell commands. Validate once, centrally, for every subcommand
	// that takes one.
	validateName := func() {
		if !bus.ValidName(*name) {
			fmt.Fprintf(os.Stderr, "agentbus: invalid --name %q: letters, digits, dash, underscore, dot (max 64)\n", *name)
			os.Exit(2)
		}
	}

	var err error
	switch cmd {
	case "host":
		fs.Parse(args)
		validateName()
		err = runHost(*name, *onMsg, sinkFor(*inbox, *onMsg))
	case "join":
		ticket, rest := popTicket(args)
		fs.Parse(rest)
		validateName()
		err = runJoin(ticket, *name, *onMsg, sinkFor(*inbox, *onMsg))
	case "send":
		ticket, rest := popTicket(args)
		fs.Parse(rest)
		validateName()
		err = runSend(ticket, *name, strings.Join(fs.Args(), " "))
	case "version":
		fs.Parse(args)
		printVersion(os.Stdout)
	case "task":
		ticket, rest := popTicket(args)
		timeout := fs.Duration("timeout", 10*time.Minute, "give up if the task has not finished by then")
		fs.Parse(rest)
		validateName()
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "agentbus: task needs a rider name and a message")
			os.Exit(2)
		}
		err = runTask(ticket, *name, fs.Arg(0), strings.Join(fs.Args()[1:], " "), *timeout)
	case "put":
		ticket, rest := popTicket(args)
		timeout := fs.Duration("timeout", 10*time.Minute, "give up if the transfer has not finished by then")
		fs.Parse(rest)
		validateName()
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "agentbus: put needs a rider name and a file path")
			os.Exit(2)
		}
		err = runPut(ticket, *name, fs.Arg(0), fs.Arg(1), *timeout)
	case "await":
		fs.Parse(args)
		err = runAwait(*inbox)
	case "wire":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "agentbus: wire needs a runtime (claude or codex) and a ticket")
			os.Exit(2)
		}
		runtime := args[0]
		ticket, rest := popTicket(args[1:])
		model := fs.String("model", "", "model for the rider's runtime")
		fs.Parse(rest)
		err = runWire(runtime, ticket, *name, *model)
	case "invite":
		ticket, rest := popTicket(args)
		fs.Parse(rest)
		// The host's hostname is a bad name for the *invited* agent;
		// default to "agent" unless --name was given explicitly.
		inviteName := "agent"
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "name" {
				inviteName = *name
			}
		})
		err = runInvite(ticket, inviteName)
	case "help", "--help", "-h":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "agentbus: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentbus: %v\n", err)
		os.Exit(1)
	}
}

func defaultName() string {
	h, err := os.Hostname()
	if err != nil {
		return "rider"
	}
	return h
}

// popTicket takes the ticket (first non-flag arg) so flags may appear
// before or after it.
func popTicket(args []string) (ticket string, rest []string) {
	for i, a := range args {
		if strings.HasPrefix(a, "tc") && !strings.HasPrefix(a, "-") {
			return a, append(append([]string{}, args[:i]...), args[i+1:]...)
		}
	}
	fmt.Fprintln(os.Stderr, "agentbus: need a ticket (starts with \"tc\")")
	os.Exit(2)
	return "", nil
}

func sinkFor(inbox, onMsg string) *bus.Sink {
	s := &bus.Sink{Out: os.Stdout, Inbox: inbox, OnMsg: onMsg}
	s.Start()
	return s
}

func logf() func(string, ...any) {
	if os.Getenv("AGENTBUS_DEBUG") != "" {
		return func(f string, a ...any) { fmt.Fprintf(os.Stderr, "debug: "+f+"\n", a...) }
	}
	return func(string, ...any) {}
}

func runHost(name, onMsg string, sink *bus.Sink) error {
	// With --on-msg the host is a rider like any other: tasks addressed
	// to it run the lifecycle, with results broadcast back as addressed
	// lines. Without it the host is a driver and hostSink renders task
	// payloads readable.
	var hub *bus.Hub
	var hostRider *task.Rider
	if onMsg != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, ".agentbus", "rider-"+name, "tasks")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		hostRider = &task.Rider{Dir: dir, Runner: execRunner(onMsg),
			Send:  func(line string) { hub.Broadcast(line) },
			Acked: func(id string) { hub.AckLocal(id) }}
	}
	hub = bus.NewHub(name, hostSink(hostRider, sink.Deliver,
		func(id string) { hub.AckLocal(id) }, bus.NewDedup(1024)))
	hub.OnNotice = func(line string) { fmt.Println(line) }
	// Task lifecycle transitions become feed notices every driver sees
	// (issue #12). Injected here because bus cannot import task.
	hub.TaskNotice = task.TransitionNotice
	// Gate 3: addressed lines for absent riders wait on disk and flush
	// when the name rejoins. 24h is long enough to sleep on a task and
	// short enough that a renamed rider's spool does not rot forever.
	if home, err := os.UserHomeDir(); err == nil {
		spool := bus.NewFileSpool(filepath.Join(home, ".agentbus", "spool"), 24*time.Hour)
		// A host serves indefinitely: sweep hourly (and once now) so
		// entries for names that never rejoin cannot outlive the TTL
		// until a restart that may never come.
		defer spool.SweepEvery(time.Hour, func(err error) {
			fmt.Fprintf(os.Stderr, "agentbus: spool sweep: %v\n", err)
		})()
		hub.Spool = spool
		// The host's own catch-up: redeliver unacked host-addressed
		// entries from before the restart.
		hub.DrainLocal()
	} else {
		fmt.Fprintf(os.Stderr, "agentbus: no home dir (%v) — offline spool disabled\n", err)
	}
	srv := &tailcat.Server{
		Logf: logf(),
		OnTCP: func(port uint16) func(net.Conn) {
			if port != busPort {
				return nil
			}
			return func(c net.Conn) { hub.Serve(c) }
		},
	}
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Close()

	ticket := srv.ConnBlob()
	fmt.Printf("🚌 the bus is running. your ticket:\n\n  %s\n\n", ticket)
	fmt.Printf("riders join with:    agentbus join <ticket> --name <who>\n")
	fmt.Printf("onboard a fresh agent: agentbus invite %s --name <who>\n\n", ticket)

	// Host stdin broadcasts as this participant.
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			hub.Broadcast(t)
		}
	}
	// stdin closed (e.g. running in background): keep serving.
	select {}
}

func dial(ticket string) (net.Conn, error) {
	c := tailcat.NewClient(tailcat.ConnBlob(ticket))
	c.Logf = logf()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.DialTCPPort(ctx, busPort)
}

// riderDir is a participant's home: conversation state, task store,
// and identity key all live here, keyed by bus name.
func riderDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentbus", "rider-"+name), nil
}

func runJoin(ticket, name, onMsg string, sink *bus.Sink) error {
	conn, err := dial(ticket)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Every join is keyed (issue #6): the first join under a name binds
	// it (TOFU) and every later connection must prove the same key.
	rdir, err := riderDir(name)
	if err != nil {
		return err
	}
	key, err := bus.LoadOrCreateKey(rdir)
	if err != nil {
		return err
	}

	// The conn is written from stdin forwarding, and — when this join
	// is a wired rider — from task goroutines reporting state. Guard it
	// so concurrent lines never interleave mid-line.
	var writeMu sync.Mutex
	sendLine := func(line string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		fmt.Fprintf(conn, "%s\n", line)
	}

	// A join with a wake command is a rider: A2A task requests are
	// claimed here and run through the task lifecycle instead of the
	// plain sink, with stdout as the result. Joins without --on-msg
	// (humans on --inbox) leave task traffic to the sink, visible as
	// ordinary lines.
	var rider *task.Rider
	if onMsg != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, ".agentbus", "rider-"+name, "tasks")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		rider = &task.Rider{Dir: dir, Runner: execRunner(onMsg), Send: sendLine,
			// ACK a task envelope only once its SUBMITTED snapshot is
			// on disk — the hub then forgets its spooled copy.
			Acked: func(id string) { sendLine(bus.Ack(id)) }}
	}

	sc := bufio.NewScanner(conn)
	// The hub accepts lines up to 256KB; the default 64KB token limit
	// would fail a legitimate large task line (PR #20 review). Must be
	// set before the scanner's first Scan.
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	if err := bus.ClientHello(conn, sc, name, false, key); err != nil {
		return err
	}
	// Heartbeat: silence must mean something is wrong, not that the
	// rider is idle — every join pings so the hub's liveness monitor
	// can flag genuinely unresponsive participants (issue #8).
	interval := heartbeatEvery
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			sendLine(bus.Ping())
		}
	}()
	// Stdin forwarding starts only AFTER the handshake: a buffered
	// stdin line sent between HELLO and SIG would be read as handshake
	// traffic and get the connection refused (PR #20 review, P1).
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				sendLine(t)
			}
		}
	}()
	// Blob transfers (issue #2) reassemble into a content-addressed
	// spool; the agent gets one FILE notice per file, never the bytes.
	var blobs *bus.BlobReceiver
	if home, err := os.UserHomeDir(); err == nil {
		blobDir := filepath.Join(home, ".agentbus", "blobs")
		if err := os.MkdirAll(blobDir, 0o700); err == nil {
			blobs = bus.NewBlobReceiver(blobDir, 0, func(l string) { sink.Deliver(l) })
			// The delivery receipt goes back to the sender as an
			// addressed line, so `put` knows the bytes landed.
			blobs.Reply = func(to, line string) { sendLine(bus.Addressed(to, line)) }
		}
	}
	// At-least-once delivery: the hub redelivers unACKed envelopes, so
	// remember recent ids and re-ACK duplicates without reprocessing.
	seen := bus.NewDedup(1024)
	for sc.Scan() {
		line := sc.Text()
		if bus.IsNotice(line) {
			fmt.Println(line) // visible to humans, never delivered to agents
			continue
		}
		from, body, isMsg := bus.ParseMessage(line)
		if isMsg {
			if id, payload, isEnv := bus.ParseEnvelope(body); isEnv {
				if rider != nil {
					if _, isTask := task.DecodeMessage(payload); isTask {
						// Task envelopes bypass the join-level dedup
						// entirely: the Rider owns task dedup with its
						// DURABLE accepted-set, and ACKs only after the
						// SUBMITTED persist. A join-level seen-mark here
						// would re-ACK a redelivery while the task is
						// still queued unpersisted — the hub would then
						// forget its only durable copy (PR #18 review).
						rider.HandleEnveloped(from, id, payload)
						continue
					}
				}
				if seen.Has(id) {
					sendLine(bus.Ack(id)) // duplicate chat: re-ACK, don't reprocess
					continue
				}
				// A blob frame is spooled out of band, not delivered to
				// the agent. ACK only once the receiver accepts it, the
				// same at-least-once contract as chat.
				if blobs != nil {
					if consumed, ok := blobs.Offer(from, payload); consumed {
						if ok {
							seen.Seen(id)
							sendLine(bus.Ack(id))
						}
						continue
					}
				}
				// Record the id only AFTER acceptance: marking first
				// would turn a failed delivery's redelivery into an
				// ACKed no-op — zero deliveries (PR #21 review).
				plain := bus.Message(from, payload)
				if rider != nil {
					if sink.Deliver(plain) {
						seen.Seen(id)
						sendLine(bus.Ack(id)) // accepted: inbox append is synchronous
					}
					continue
				}
				if sink.Deliver(driverLine(plain)) {
					seen.Seen(id)
					sendLine(bus.Ack(id))
				}
				continue
			}
		}
		if rider != nil {
			if isMsg {
				if _, isTask := task.DecodeMessage(body); isTask {
					rider.Handle(from, body)
					continue
				}
			}
			sink.Deliver(line)
			continue
		}
		// A join without a wake command is a driver's seat: task
		// payloads render as readable lines, not raw JSON (issue #12).
		sink.Deliver(driverLine(line))
	}
	return fmt.Errorf("disconnected from the bus")
}

// runAwait blocks until the inbox holds unread complete lines, prints
// them, and exits. An agent runs this as a background task; the task
// completing is the wake-up. Catch-up is built in: pending lines return
// immediately, so a message that landed before await started is never
// lost.
func runAwait(inbox string) error {
	if inbox == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		inbox = home + "/.agentbus/inbox"
	}
	lines, err := bus.Await(inbox, 200*time.Millisecond)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

func runSend(ticket, name, msg string) error {
	if msg == "" {
		b, _ := io.ReadAll(os.Stdin)
		msg = strings.TrimSpace(string(b))
	}
	if msg == "" {
		return fmt.Errorf("nothing to send")
	}
	conn, err := dial(ticket)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Authenticate when we hold this name's key (an operator sending
	// under their rider's name on the same host); a bound name refuses
	// unkeyed sends outright (issue #6).
	rdir, err := riderDir(name)
	if err != nil {
		return err
	}
	key, err := bus.LoadKeyIfExists(rdir)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	if err := bus.ClientHello(conn, sc, name, true, key); err != nil {
		return err
	}
	// Wait for the welcome so we know the hub registered us before we
	// send; otherwise the message could relay before registration.
	if !sc.Scan() {
		return fmt.Errorf("bus closed the connection")
	}
	if !strings.Contains(sc.Text(), "welcome aboard") {
		return fmt.Errorf("bus refused this connection: %s", sc.Text())
	}
	for _, line := range strings.Split(msg, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			fmt.Fprintf(conn, "%s\n", line)
		}
	}
	// Brief linger so the tunnel flushes and ACKs before teardown.
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	io.Copy(io.Discard, conn)
	return nil
}
