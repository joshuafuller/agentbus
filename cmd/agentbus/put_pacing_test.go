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

// The deadlock regression (WAN test finding): a blob larger than the
// pump's unACKed-byte budget could never finish while frame ACKs were
// held until blob completion — header+chunk1 filled the budget and
// chunk2 was never offered. With per-frame durable ACKs the transfer
// must complete over a spooled (paced) hub.
func TestBlobLargerThanPumpBudgetOverSpooledHub(t *testing.T) {
	h := bus.NewHub("host", nil)
	h.Spool = bus.NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 200 * time.Millisecond

	// A receiving rider wired the way runJoin wires blob frames,
	// including the per-frame ACK policy.
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
		br.Reply = func(to, line string) { rc.Write([]byte(bus.Addressed(to, line) + "\n")) }
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for sc.Scan() {
			from, body, ok := bus.ParseMessage(sc.Text())
			if !ok {
				continue
			}
			id, payload, isEnv := bus.ParseEnvelope(body)
			if !isEnv {
				continue
			}
			if consumed, accepted := br.Offer(from, payload); consumed {
				blobID := blobFrameID(payload)
				switch {
				case accepted:
					rc.Write([]byte(bus.Ack(id) + "\n"))
					br.TakeCompleted(blobID)
				case br.TakeDuplicate(blobID), br.TakeRejected(blobID):
					rc.Write([]byte(bus.Ack(id) + "\n"))
				}
				continue
			}
			rc.Write([]byte(bus.Ack(id) + "\n"))
		}
	}()

	// 200KB: several 32KB chunks, comfortably over the 64KB budget.
	content := bytes.Repeat([]byte("deadlock regression bytes\n"), 8000)
	src := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	var out strings.Builder
	go func() { done <- runPutConn(requesterConn(t, h), "alice", "bob", src, time.Minute, nil, &out) }()

	select {
	case n := <-notes:
		path := n[strings.LastIndex(n, " ")+1:]
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("spooled blob unreadable: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Fatal("blob corrupted in transit")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("blob never completed — the pacing deadlock is back")
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("put returned %d: %q", code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("put never got its receipt")
	}
}
