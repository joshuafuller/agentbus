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

const usage = `agentbus — a message bus for AI agents. One ticket, any number of riders.

Usage:
  agentbus host [flags]                 start a bus, print its ticket
  agentbus join <ticket> [flags]        ride the bus (stays connected)
  agentbus send <ticket> [flags] <msg>  send one message and exit
  agentbus task <ticket> <rider> <msg>  send an A2A task to one rider and
                                        follow it to completion or failure
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
		err = runHost(*name, sinkFor(*inbox, *onMsg))
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

func runHost(name string, sink *bus.Sink) error {
	hub := bus.NewHub(name, sink.Deliver)
	hub.OnNotice = func(line string) { fmt.Println(line) }
	// Task lifecycle transitions become feed notices every driver sees
	// (issue #12). Injected here because bus cannot import task.
	hub.TaskNotice = task.TransitionNotice
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

func runJoin(ticket, name, onMsg string, sink *bus.Sink) error {
	conn, err := dial(ticket)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "%s\n", bus.Hello(name))

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
		rider = &task.Rider{Dir: dir, Runner: execRunner(onMsg), Send: sendLine}
	}

	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				sendLine(t)
			}
		}
	}()

	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := sc.Text()
		if bus.IsNotice(line) {
			fmt.Println(line) // visible to humans, never delivered to agents
			continue
		}
		if rider != nil {
			if from, payload, ok := bus.ParseMessage(line); ok {
				if _, isTask := task.DecodeMessage(payload); isTask {
					// Run in the background so a minutes-long model turn
					// never stops this loop from reading the bus.
					go rider.Handle(from, payload)
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
	fmt.Fprintf(conn, "%s\n", bus.HelloOneshot(name))

	// Wait for the welcome so we know the hub registered us before we
	// send; otherwise the message could relay before registration.
	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		return fmt.Errorf("bus closed the connection")
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
