package bus

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The WAN finding: flooding every pending spool entry into the outbox
// at once (~300KB of blob chunks) wedged the tunnel to a remote rider
// outright, and redelivery re-wedged it forever. The pump must pace by
// unACKed bytes: with 43KB entries and a 64KB budget, at most one
// large entry may be un-ACKed in flight at a time.
func TestPumpPacesLargeSpoolEntries(t *testing.T) {
	h := NewHub("host", nil)
	h.Spool = NewFileSpool(t.TempDir(), time.Hour)
	h.RetryInterval = 30 * time.Second // retries off the table: outstanding = pacing

	big := strings.Repeat("z", 43<<10)
	for i := 0; i < 6; i++ {
		if _, err := h.Spool.Add("bob", Message("alice", big)); err != nil {
			t.Fatal(err)
		}
	}

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := client.Write([]byte(Hello("bob") + "\n")); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	outstanding, maxOutstanding, delivered := 0, 0, 0
	sendLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		client.Write([]byte(line + "\n"))
	}
	sc := bufio.NewScanner(client)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	deadline := time.Now().Add(15 * time.Second)
	for sc.Scan() {
		line := sc.Text()
		if IsNotice(line) {
			continue
		}
		_, body, ok := ParseMessage(line)
		if !ok {
			continue
		}
		id, _, isEnv := ParseEnvelope(body)
		if !isEnv {
			continue
		}
		mu.Lock()
		outstanding++
		if outstanding > maxOutstanding {
			maxOutstanding = outstanding
		}
		delivered++
		done := delivered == 6
		mu.Unlock()
		// ACK after a beat — long enough that an unpaced pump would
		// have flooded the rest of the backlog meanwhile.
		go func(envID string) {
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			outstanding--
			mu.Unlock()
			sendLine(Ack(envID))
		}(id)
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 6 entries delivered before the deadline", delivered)
		}
	}
	if delivered != 6 {
		t.Fatalf("delivered %d of 6 entries (scan err: %v)", delivered, sc.Err())
	}
	if maxOutstanding > 2 {
		t.Fatalf("pump flooded: %d large entries un-ACKed at once, budget allows 1 (tolerance 2)", maxOutstanding)
	}
}
