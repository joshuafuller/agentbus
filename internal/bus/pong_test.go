package bus

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// The hub answers every PING with a PONG on the same conn (#34): a
// rider's heartbeat becomes a liveness probe of the HOST, so a rider
// whose host died silently (no FIN through the tunnel) sees its read
// deadline expire and reconnects, instead of hanging deaf forever.
func TestHubAnswersPingWithPong(t *testing.T) {
	h := NewHub("host", nil)
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go h.Serve(server)
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write([]byte(Hello("alice") + "\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	welcome, err := br.ReadString('\n')
	if err != nil || !strings.Contains(welcome, "welcome aboard") {
		t.Fatalf("no welcome: %q err=%v", welcome, err)
	}
	if _, err := client.Write([]byte(Ping() + "\n")); err != nil {
		t.Fatal(err)
	}
	reply, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("no pong: %v", err)
	}
	if !IsPong(strings.TrimSpace(reply)) {
		t.Fatalf("expected PONG, got %q", reply)
	}
}
