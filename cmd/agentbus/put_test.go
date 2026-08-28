package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	go func() { done <- runPutConn(requesterConn(t, h), "alice", "bob", src, 5*time.Minute, nil, &out) }()

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

func TestPutZeroByteRoundTrip(t *testing.T) {
	h := bus.NewHub("host", nil)
	rc, srv := net.Pipe()
	t.Cleanup(func() { rc.Close() })
	go h.Serve(srv)
	if _, err := rc.Write([]byte(bus.Hello("bob") + "\n")); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	frames := make(chan string, 4)
	go func() {
		br := bus.NewBlobReceiver(spool, 0, func(string) {})
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
			frames <- body
			br.Offer(from, body)
		}
	}()

	src := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	var out strings.Builder
	go func() { done <- runPutConn(requesterConn(t, h), "alice", "bob", src, 5*time.Minute, nil, &out) }()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-frames:
			if _, _, _, ok := bus.ParseBlobChunk(frame); ok {
				select {
				case code := <-done:
					if code != 0 {
						t.Fatalf("zero-byte put returned %d: %q", code, out.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("zero-byte put did not complete after empty chunk")
				}
				return
			}
		case <-deadline:
			t.Fatal("zero-byte put did not emit an empty completion chunk")
		}
	}
}

func TestPutUsesConfiguredTimeout(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		peer.Close()
	})
	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	var out strings.Builder
	go func() { done <- runPutConn(client, "alice", "bob", src, 50*time.Millisecond, nil, &out) }()
	select {
	case code := <-done:
		if code != 2 {
			t.Fatalf("timed-out put returned %d: %q", code, out.String())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("put ignored the configured timeout")
	}
}

func TestPutReportsClosedConnectionBeforeWelcome(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		peer.Close()
	})
	go func() {
		reader := bufio.NewReader(peer)
		reader.ReadString('\n')
		peer.Close()
	}()
	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if code := runPutConn(client, "alice", "bob", src, time.Second, nil, &out); code != 2 {
		t.Fatalf("closed connection returned %d: %q", code, out.String())
	}
	if !strings.Contains(out.String(), "connection closed before welcome") {
		t.Fatalf("closed connection message = %q", out.String())
	}
}

func TestPutDoesNotAckUnrelatedEnvelope(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		peer.Close()
	})
	acks := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(peer)
		if !sc.Scan() {
			return
		}
		peer.Write([]byte(bus.Notice("welcome aboard, alice — 2 on the bus") + "\n"))
		var transferID string
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if id, ok := bus.ParseAck(line); ok {
				acks <- id
				continue
			}
			_, payload, ok := bus.ParseAddressed(line)
			if !ok {
				continue
			}
			if h, ok := bus.ParseBlobHeader(payload); ok {
				transferID = h.ID
				continue
			}
			if _, _, _, ok := bus.ParseBlobChunk(payload); ok && !sent {
				sent = true
				go func() {
					peer.Write([]byte(bus.Message("server", bus.Envelope("unrelated-envelope", bus.BlobReceipt("other", true, ""))) + "\n"))
					peer.Write([]byte(bus.Message("server", bus.Envelope("matching-envelope", bus.BlobReceipt(transferID, true, ""))) + "\n"))
				}()
			}
		}
	}()
	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	var out strings.Builder
	go func() { done <- runPutConn(client, "alice", "bob", src, time.Second, nil, &out) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("put returned %d: %q", code, out.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("put did not receive its matching receipt")
	}
	gotMatching := false
	for !gotMatching {
		select {
		case id := <-acks:
			if id == "unrelated-envelope" {
				t.Fatal("put ACKed an unrelated envelope")
			}
			if id == "matching-envelope" {
				gotMatching = true
			}
		case <-time.After(time.Second):
			t.Fatal("matching receipt envelope was not ACKed")
		}
	}
}

func TestOfferUnwrappedBlobFrame(t *testing.T) {
	dir := t.TempDir()
	r := bus.NewBlobReceiver(dir, 0, func(string) {})
	frames := bus.BlobFrames("unwrapped", "f.bin", []byte("data"), 4)
	for _, frame := range frames {
		if !offerUnwrappedBlob(r, "sender", frame) {
			t.Fatalf("unwrapped blob frame was not consumed: %q", frame)
		}
	}
	sum := sha256.Sum256([]byte("data"))
	if _, err := os.Stat(filepath.Join(dir, hex.EncodeToString(sum[:])+"-f.bin")); err != nil {
		t.Fatalf("unwrapped blob was not published: %v", err)
	}
}

func TestPutRefusesMissingFileAndBadRider(t *testing.T) {
	h := bus.NewHub("host", nil)
	var out strings.Builder
	if code := runPutConn(requesterConn(t, h), "alice", "no [such] rider", "/dev/null", 5*time.Minute, nil, &out); code != 2 {
		t.Fatalf("invalid rider name: want exit 2, got %d", code)
	}
	if code := runPutConn(requesterConn(t, h), "alice", "bob", "/nonexistent/file", 5*time.Minute, nil, &out); code != 2 {
		t.Fatalf("missing file: want exit 2, got %d", code)
	}
}

func TestPutRefusesNonRegularFile(t *testing.T) {
	h := bus.NewHub("host", nil)
	var out strings.Builder
	code := runPutConn(requesterConn(t, h), "alice", "bob", "/dev/null", 20*time.Millisecond, nil, &out)
	if code != 2 {
		t.Fatalf("device path: want exit 2, got %d", code)
	}
	if !strings.Contains(out.String(), "not a regular file") {
		t.Fatalf("device path was not rejected as non-regular: %q", out.String())
	}
}
