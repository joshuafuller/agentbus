package main

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
)

// Blob transfer end to end (issue #2): `put` streams a file to one
// rider; the rider's join spools it content-addressed and the agent
// sees one FILE line, never the bytes.
func TestPutRoundTripOverHub(t *testing.T) {
	h := bus.NewHub("host", nil)

	// A receiving rider wired the way runJoin wires blob frames.
	rc, srv := net.Pipe()
	t.Cleanup(func() { rc.Close() })
	go h.Serve(srv)
	if _, err := rc.Write([]byte(bus.Hello("bob") + "\n")); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	notes := make(chan string, 4)
	go func() {
		br := bus.NewBlobReceiver(spool, 0, func(l string) { notes <- l })
		// The receiver addresses its receipt back to the sender.
		br.Reply = func(to, line string) { rc.Write([]byte(bus.Addressed(to, line) + "\n")) }
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for sc.Scan() {
			from, body, ok := bus.ParseMessage(sc.Text())
			if !ok {
				continue
			}
			if id, payload, isEnv := bus.ParseEnvelope(body); isEnv {
				rc.Write([]byte(bus.Ack(id) + "\n"))
				body = payload
			}
			br.Offer(from, body)
		}
	}()

	content := bytes.Repeat([]byte("blob bytes over the bus\n"), 500)
	src := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}

	// runPut must BLOCK until the receiver confirms the whole blob —
	// a fire-and-forget send races the tunnel close and drops chunks.
	done := make(chan int, 1)
	var out strings.Builder
	go func() { done <- runPutConn(requesterConn(t, h), "alice", "bob", src, nil, &out) }()

	select {
	case n := <-notes:
		if !strings.Contains(n, "FILE") || !strings.Contains(n, "artifact.bin") || !strings.Contains(n, "[alice]") {
			t.Fatalf("bad notification: %q", n)
		}
		path := n[strings.LastIndex(n, " ")+1:]
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("spooled blob unreadable: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Fatal("blob corrupted in transit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blob never arrived")
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("put returned %d after the blob landed: %q", code, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("put never returned after the receiver confirmed — no receipt")
	}
}

func TestPutRefusesMissingFileAndBadRider(t *testing.T) {
	h := bus.NewHub("host", nil)
	var out strings.Builder
	if code := runPutConn(requesterConn(t, h), "alice", "no [such] rider", "/dev/null", nil, &out); code != 2 {
		t.Fatalf("invalid rider name: want exit 2, got %d", code)
	}
	if code := runPutConn(requesterConn(t, h), "alice", "bob", "/nonexistent/file", nil, &out); code != 2 {
		t.Fatalf("missing file: want exit 2, got %d", code)
	}
}
