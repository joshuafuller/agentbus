package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
	"github.com/tmc/go-iroh/endpointticket"

	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relayserver"
)

func localRelay(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(relayserver.NewWithOptions(relayserver.WithClientRate(0)))
	t.Cleanup(s.Close)
	return s.URL
}

func testHost(t *testing.T, id *hostIdentity, serve func(net.Conn), opts ...nativeOptions) (*nativeEndpoint, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	ep, ticket, err := startHostTransport(ctx, id, serve, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ep.Shutdown(context.Background()) })
	return ep, ticket
}

func testDial(t *testing.T, ticket string, opts ...nativeOptions) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := dialIroh(ctx, ticket, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestIrohTicketValidation(t *testing.T) {
	id, err := newHostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	id.Relay = "https://relay.example.invalid/"
	ticket := identityTicket(t, id)
	addr, secret, err := parseTicket(ticket)
	if err != nil {
		t.Fatal(err)
	}
	sk, _ := key.SecretKeyFromSlice(id.Private)
	if addr.ID != sk.Public().EndpointID() || !bytes.Equal(secret, id.Admission) {
		t.Fatal("ticket lost endpoint identity or admission")
	}
	invalid := []string{
		"", "tc-old-ticket", "ab2-future-version", "ab1!", "ab1" + strings.Repeat("A", 4096),
		endpointticket.Encode(addr),
		encodeTicket(addr, make([]byte, admissionBytes)),
		encodeTicket(netaddr.NewEndpointAddr(addr.ID), secret),
		encodeTicket(netaddr.NewEndpointAddr(addr.ID).WithIP(netip.MustParseAddrPort("0.0.0.0:42")), secret),
		ticket + "=",
		ticket + "\n",
		ticket + "\r",
		ticketPrefix + base64.RawURLEncoding.EncodeToString(append(secret, []byte("not an endpoint")...)),
	}
	for i, s := range invalid {
		if _, _, err := parseTicket(s); err == nil {
			t.Errorf("invalid ticket %d accepted", i)
		}
	}
	if err := runWire("codex", "ab1-invalid", "bob", ""); err == nil {
		t.Fatal("wire accepted an invalid ticket before runtime bootstrap")
	}
	if err := runInvite("ab1-invalid", "bob"); err == nil {
		t.Fatal("invite emitted an invalid boarding pass")
	}
	if err := runJoin("ab1-invalid", "bob", "", &bus.Sink{}); err == nil {
		t.Fatal("join accepted a permanently invalid ticket")
	}
	for _, s := range []string{"file:///tmp/relay", "https://u:secret@example.invalid", "https://example.invalid/?token=secret"} {
		if _, err := parseRelay(s); err == nil {
			t.Error("unsafe relay URL accepted")
		}
	}
	// Flags on either side still work with the new ticket prefix.
	got, rest := popTicket([]string{"--name", "alice", ticket, "hi"})
	if got != ticket || strings.Join(rest, " ") != "--name alice hi" {
		t.Fatal("ticket extraction changed command arguments")
	}
}

func TestIrohStreamsAdmissionDeadlinesAndClose(t *testing.T) {
	for _, viaRelay := range []bool{false, true} {
		name := "direct"
		if viaRelay {
			name = "relay-only"
		}
		t.Run(name, func(t *testing.T) {
			id, err := newHostIdentity()
			if err != nil {
				t.Fatal(err)
			}
			id.Relay = localRelay(t)
			served := make(chan struct{}, 8)
			opts := []nativeOptions{{BindAddr: "127.0.0.1:0"}}
			if viaRelay {
				opts = []nativeOptions{{RelayOnly: true}}
			}
			ep, ticket := testHost(t, id, func(c net.Conn) {
				served <- struct{}{}
				io.Copy(c, c)
			}, opts...)
			if !viaRelay {
				ticket = encodeTicket(netaddr.NewEndpointAddr(ep.ID()).WithIP(ep.LocalAddr()), id.Admission)
			}
			c := testDial(t, ticket, opts...)
			select {
			case <-served:
			case <-time.After(3 * time.Second):
				t.Fatal("admitted stream never reached server")
			}
			c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			var b [2]byte
			if _, err := c.Read(b[:]); err == nil {
				t.Fatal("read deadline did not expire")
			} else {
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("read deadline returned non-timeout: %v", err)
				}
			}
			c.SetDeadline(time.Now().Add(-time.Second))
			if _, err := c.Write([]byte("no")); err == nil {
				t.Fatal("write deadline did not expire")
			}
			c.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := io.WriteString(c, "ok"); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(c, b[:]); err != nil || string(b[:]) != "ok" {
				t.Fatalf("reset deadline/duplex stream failed: %v", err)
			}
			c.SetDeadline(time.Time{})
			readDone := make(chan error, 1)
			go func() { _, err := c.Read(b[:]); readDone <- err }()
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-readDone:
				if err == nil {
					t.Fatal("closed read succeeded")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Close did not unblock Read")
			}
			select {
			case <-c.(*endpointConn).ep.Closed():
			default:
				t.Fatal("client endpoint leaked after Close")
			}

			addr, secret, _ := parseTicket(ticket)
			wrong := append([]byte{}, secret...)
			wrong[0] ^= 1
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if c, err := dialIroh(ctx, encodeTicket(addr, wrong), opts...); err == nil {
				c.Close()
				t.Fatal("wrong admission secret accepted")
			}
			select {
			case <-served:
				t.Fatal("unadmitted client reached hub")
			default:
			}
			if !viaRelay {
				impostor, err := key.GenerateSecretKey()
				if err != nil {
					t.Fatal(err)
				}
				addr.ID = impostor.Public().EndpointID()
				if c, err := dialIroh(ctx, encodeTicket(addr, secret), opts...); err == nil {
					c.Close()
					t.Fatal("server with the wrong endpoint identity was authenticated")
				}
			}
		})
	}
}

func TestIrohRestartDeliversSpoolAndActivatesRider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previousHeartbeat := heartbeatEvery
	heartbeatEvery = 50 * time.Millisecond
	t.Cleanup(func() { heartbeatEvery = previousHeartbeat })
	dir := t.TempDir()
	id, err := newHostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	id.Relay = localRelay(t)
	spoolDir := filepath.Join(dir, "spool")
	makeHub := func() *bus.Hub {
		h := bus.NewHub("host", nil)
		h.Spool = bus.NewFileSpool(spoolDir, time.Hour)
		h.RetryInterval = 100 * time.Millisecond
		if err := h.PersistBindings(filepath.Join(dir, hostTOFUFile)); err != nil {
			t.Fatal(err)
		}
		return h
	}
	h1 := makeHub()
	ep1, ticket := testHost(t, id, h1.Serve, nativeOptions{RelayOnly: true})
	if err := saveHostIdentity(dir, id); err != nil {
		t.Fatal(err)
	}
	// Bind the rider's name through the real Iroh stream before restart.
	_, riderKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	initial := testDial(t, ticket, nativeOptions{RelayOnly: true})
	sc := bufio.NewScanner(initial)
	if err := bus.ClientHello(initial, sc, "bob", false, riderKey); err != nil {
		t.Fatal(err)
	}
	initial.SetReadDeadline(time.Now().Add(3 * time.Second))
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome aboard") {
		t.Fatal("no initial welcome")
	}
	initial.Close()
	if err := runSendConn(testDial(t, ticket, nativeOptions{RelayOnly: true}), "alice", "bob", "work survives restart", nil); err != nil {
		t.Fatal(err)
	}
	if h1.Spool.Pending("bob") == 0 {
		t.Fatal("addressed work was not spooled")
	}
	ep1.Shutdown(context.Background())

	loaded, err := loadHostIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2 := makeHub()
	ep2, ticket2 := testHost(t, loaded, h2.Serve, nativeOptions{RelayOnly: true})
	if ticket != ticket2 {
		t.Fatal("restart changed the issued ticket")
	}
	// A restart must not reopen TOFU for a different key under the same name.
	_, impostor, _ := ed25519.GenerateKey(rand.Reader)
	refused := testDial(t, ticket2)
	refused.SetDeadline(time.Now().Add(3 * time.Second))
	refusalScanner := bufio.NewScanner(refused)
	if err := bus.ClientHello(refused, refusalScanner, "bob", false, impostor); err != nil {
		t.Fatal(err)
	}
	if !refusalScanner.Scan() || !strings.Contains(refusalScanner.Text(), "refused") {
		t.Fatalf("persisted identity refusal was not delivered: %v", refusalScanner.Err())
	}
	refused.Close()

	marker := filepath.Join(dir, "activated")
	sink := &bus.Sink{OnMsg: fmt.Sprintf(`printf '%%s' "$AGENTBUS_MSG" > '%s'`, marker)}
	sink.Start()
	joined := make(chan error, 1)
	go func() { joined <- joinSession(ticket2, "bob", sink, riderKey, nil, &reconnectWriter{}) }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		data, _ := os.ReadFile(marker)
		if string(data) == "[alice] work survives restart" && h2.Spool.Pending("bob") == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("spooled work did not activate the rider and receive an ACK after restart")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ep2.Shutdown(context.Background())
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("join did not exit after host shutdown")
	}
}
