package main

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
)

// chatRider joins hub as a plain chat rider (no task runner) that ACKs
// enveloped deliveries with receiver-side dedup, the way the real join
// loop does, and records every chat line it accepts.
func chatRider(t *testing.T, h *bus.Hub, name string) func() []string {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	if _, err := client.Write([]byte(bus.Hello(name) + "\n")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	sendLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		client.Write([]byte(line + "\n"))
	}
	sc := bufio.NewScanner(client)
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome aboard") {
		t.Fatalf("no welcome for rider %s: %q", name, sc.Text())
	}
	go func() {
		seen := bus.NewDedup(256)
		for sc.Scan() {
			line := sc.Text()
			if bus.IsNotice(line) {
				continue
			}
			from, body, ok := bus.ParseMessage(line)
			if !ok {
				continue
			}
			if id, payload, isEnv := bus.ParseEnvelope(body); isEnv {
				sendLine(bus.Ack(id))
				if seen.Seen(id) {
					continue // duplicate: re-ACKed, not re-recorded
				}
				body = payload
			}
			mu.Lock()
			got = append(got, bus.Message(from, body))
			mu.Unlock()
		}
	}()
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// The headline acceptance test (#33): a --to send while the rider is
// offline must survive on the host spool and reach the rider exactly
// once when it rejoins.
func TestSendToOfflineRiderSpoolsAndDelivers(t *testing.T) {
	h := bus.NewHub("host", nil)
	h.Spool = bus.NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 200 * time.Millisecond

	if err := runSendConn(requesterConn(t, h), "alice", "bob", "TASK t9 audit the spool", nil); err != nil {
		t.Fatalf("send --to while bob offline: %v", err)
	}
	if h.Spool.Pending("bob") == 0 {
		t.Fatal("nothing spooled for bob after an addressed send to an absent rider")
	}

	bobGot := chatRider(t, h, "bob")
	want := "[alice] TASK t9 audit the spool"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lines := bobGot(); len(lines) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bob never received the spooled line; got %v", bobGot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Wait out several retry intervals: an unACKed or badly deduped
	// delivery would show up here as a second copy.
	time.Sleep(3 * h.RetryInterval)
	lines := bobGot()
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("bob got %v, want exactly [%q]", lines, want)
	}
}

// A --to send with the rider live: delivered exactly once, and only to
// that rider — never to bystanders.
func TestSendToLiveRiderDeliversOnlyToTarget(t *testing.T) {
	h := bus.NewHub("host", nil)
	h.Spool = bus.NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 200 * time.Millisecond

	bobGot := chatRider(t, h, "bob")
	carolGot := chatRider(t, h, "carol")

	if err := runSendConn(requesterConn(t, h), "alice", "bob", "TASK t9 for bob only", nil); err != nil {
		t.Fatalf("send --to live bob: %v", err)
	}

	want := "[alice] TASK t9 for bob only"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lines := bobGot(); len(lines) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bob never received the addressed line; got %v", bobGot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(3 * h.RetryInterval)
	if lines := bobGot(); len(lines) != 1 || lines[0] != want {
		t.Fatalf("bob got %v, want exactly [%q]", lines, want)
	}
	if lines := carolGot(); len(lines) != 0 {
		t.Fatalf("carol saw an addressed line not meant for her: %v", lines)
	}
}

// A broadcast send (no --to) must stay a broadcast: everyone gets it.
func TestSendWithoutToStillBroadcasts(t *testing.T) {
	h := bus.NewHub("host", nil)
	bobGot := chatRider(t, h, "bob")
	carolGot := chatRider(t, h, "carol")

	if err := runSendConn(requesterConn(t, h), "alice", "", "hello everyone", nil); err != nil {
		t.Fatalf("broadcast send: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if len(bobGot()) > 0 && len(carolGot()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("broadcast not seen by everyone: bob=%v carol=%v", bobGot(), carolGot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// AC (#33): --to with an invalid rider name exits 2 with a clear error,
// before any network use. Re-execs the test binary so os.Exit is real.
func TestSendToInvalidRiderNameExits2(t *testing.T) {
	if os.Getenv("AGENTBUS_RUN_MAIN") == "1" {
		os.Args = []string{"agentbus", "send", "tc-unused-ticket", "--to", "bad name!", "hi"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run", "TestSendToInvalidRiderNameExits2")
	cmd.Env = append(os.Environ(), "AGENTBUS_RUN_MAIN=1")
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("want exit error code 2, got err=%v output=%s", err, out)
	}
	if code := ee.ExitCode(); code != 2 {
		t.Fatalf("exit code %d, want 2; output:\n%s", code, out)
	}
	if !strings.Contains(string(out), "--to") {
		t.Fatalf("error does not name --to:\n%s", out)
	}
}
