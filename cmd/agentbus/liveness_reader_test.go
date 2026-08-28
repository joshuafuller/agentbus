package main

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// The WAN finding: a large line arriving slowly must NOT expire the
// liveness deadline mid-line — every arriving byte is proof of life.
func TestLivenessReaderSurvivesSlowLargeLine(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	lr := &livenessReader{conn: client}
	lr.arm(150 * time.Millisecond)

	// The whole line takes ~600ms — four times the window — but bytes
	// keep arriving well inside it.
	go func() {
		for i := 0; i < 12; i++ {
			server.Write([]byte(strings.Repeat("x", 10)))
			time.Sleep(50 * time.Millisecond)
		}
		server.Write([]byte("\n"))
	}()

	sc := bufio.NewScanner(lr)
	if !sc.Scan() {
		t.Fatalf("liveness deadline killed a healthy slow transfer: %v", sc.Err())
	}
	if got := len(sc.Text()); got != 120 {
		t.Fatalf("line truncated: %d bytes", got)
	}
}

// True silence must still end the read within the window.
func TestLivenessReaderStillDetectsSilence(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	lr := &livenessReader{conn: client}
	lr.arm(150 * time.Millisecond)

	// net.Pipe writes are synchronous: they park until the reader takes
	// the bytes, so the partial write must not run on the test goroutine.
	go server.Write([]byte("partial-then-silence")) // no newline, then nothing

	// The scanner may hand back the buffered partial as a final token
	// when the deadline error surfaces (bufio.Scanner semantics); what
	// matters is that scanning TERMINATES within the window instead of
	// blocking forever on the silent conn.
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(lr)
		for sc.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("silent connection not detected within the window")
	}
}
