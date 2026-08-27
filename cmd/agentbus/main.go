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
	"strings"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
	"github.com/tailscale/tailcat"
)

// busPort is the virtual TCP port the bus speaks on inside the tunnel.
const busPort = 2255

const usage = `agentbus — a message bus for AI agents. One ticket, any number of riders.

Usage:
  agentbus host [flags]                 start a bus, print its ticket
  agentbus join <ticket> [flags]        ride the bus (stays connected)
  agentbus send <ticket> [flags] <msg>  send one message and exit

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

	var err error
	switch cmd {
	case "host":
		fs.Parse(args)
		err = runHost(*name, sinkFor(*inbox, *onMsg))
	case "join":
		ticket, rest := popTicket(args)
		fs.Parse(rest)
		err = runJoin(ticket, *name, sinkFor(*inbox, *onMsg))
	case "send":
		ticket, rest := popTicket(args)
		fs.Parse(rest)
		err = runSend(ticket, *name, strings.Join(fs.Args(), " "))
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

	fmt.Printf("🚌 the bus is running. your ticket:\n\n  %s\n\n", srv.ConnBlob())
	fmt.Printf("riders join with: agentbus join <ticket> --name <who>\n\n")

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

func runJoin(ticket, name string, sink *bus.Sink) error {
	conn, err := dial(ticket)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "%s\n", bus.Hello(name))

	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				fmt.Fprintf(conn, "%s\n", t)
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
		sink.Deliver(line)
	}
	return fmt.Errorf("disconnected from the bus")
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
	fmt.Fprintf(conn, "%s\n", bus.Hello(name))

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
