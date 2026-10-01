package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tmc/go-iroh/endpointticket"

	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

const (
	busALPN        = "agentbus/1"
	ticketPrefix   = "ab1"
	admissionBytes = 32
)

// The endpoint address is public. Admission is a separate bearer secret,
// checked inside the authenticated TLS stream before any hub traffic.
type hostIdentity struct {
	Version   int    `json:"version"`
	Private   []byte `json:"private"`
	Admission []byte `json:"admission"`
	Relay     string `json:"relay"`
}

func newHostIdentity() (*hostIdentity, error) {
	sk, err := key.GenerateSecretKey()
	if err != nil {
		return nil, err
	}
	seed := sk.Bytes()
	id := &hostIdentity{Version: 1, Private: seed[:], Admission: make([]byte, admissionBytes)}
	if _, err := rand.Read(id.Admission); err != nil {
		return nil, err
	}
	return id, nil
}

func encodeTicket(addr netaddr.EndpointAddr, admission []byte) string {
	b := append(append([]byte{}, admission...), endpointticket.New(addr).EncodeBytes()...)
	return ticketPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func parseTicket(s string) (netaddr.EndpointAddr, []byte, error) {
	bad := errors.New("invalid Iroh bus ticket (expected ab1…); Tailcat tickets require host --new-ticket and a new boarding pass")
	if !strings.HasPrefix(s, ticketPrefix) || len(s) > 4096 || strings.ContainsAny(s, "\r\n") {
		return netaddr.EndpointAddr{}, nil, bad
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s[len(ticketPrefix):])
	if err != nil || len(b) <= admissionBytes || subtle.ConstantTimeCompare(b[:admissionBytes], make([]byte, admissionBytes)) == 1 {
		return netaddr.EndpointAddr{}, nil, bad
	}
	t, err := endpointticket.DecodeBytes(b[admissionBytes:])
	if err != nil {
		return netaddr.EndpointAddr{}, nil, bad
	}
	addr := t.Addr()
	if len(addr.Addrs()) == 0 || len(addr.Addrs()) > 8 {
		return netaddr.EndpointAddr{}, nil, bad
	}
	for _, a := range addr.Addrs() {
		switch a := a.(type) {
		case netaddr.RelayAddr:
			if _, err := parseRelay(a.URL.String()); err != nil {
				return netaddr.EndpointAddr{}, nil, bad
			}
		case netaddr.IPAddr:
			if !a.Addr.IsValid() || a.Addr.Port() == 0 || a.Addr.Addr().IsUnspecified() || a.Addr.Addr().IsMulticast() {
				return netaddr.EndpointAddr{}, nil, bad
			}
		default:
			return netaddr.EndpointAddr{}, nil, bad
		}
	}
	return addr, b[:admissionBytes], nil
}

func parseRelay(s string) (netaddr.RelayURL, error) {
	u, err := netaddr.ParseRelayURL(s)
	if err != nil {
		return netaddr.RelayURL{}, errors.New("invalid Iroh relay URL")
	}
	v := u.URL()
	if (v.Scheme != "https" && v.Scheme != "http") || v.Hostname() == "" || v.User != nil || v.RawQuery != "" || v.Fragment != "" {
		return netaddr.RelayURL{}, errors.New("Iroh relay must be an HTTP(S) URL without credentials, query or fragment")
	}
	return u, nil
}

// nativeOptions are used by isolated transport tests; production uses defaults.
type nativeOptions struct {
	BindAddr              string
	DirectOnly, RelayOnly bool
}

func startHostTransport(ctx context.Context, id *hostIdentity, serve func(net.Conn), opts ...nativeOptions) (*nativeEndpoint, string, error) {
	cfg := nativeConfig{Seed: base64.StdEncoding.EncodeToString(id.Private), Relay: id.Relay}
	if cfg.Relay == "" {
		cfg.Relay = os.Getenv("AGENTBUS_RELAY")
	}
	if cfg.Relay != "" {
		if _, err := parseRelay(cfg.Relay); err != nil {
			return nil, "", err
		}
	}
	ep, ready, err := startNative(ctx, cfg, opts...)
	if err != nil {
		return nil, "", err
	}
	endpointID, err := key.ParseEndpointID(ready.ID)
	if err != nil {
		ep.Shutdown(context.Background())
		return nil, "", errors.New("invalid native endpoint identity")
	}
	sk, err := key.SecretKeyFromSlice(id.Private)
	if err != nil || endpointID != sk.Public().EndpointID() {
		ep.Shutdown(context.Background())
		return nil, "", errors.New("native endpoint identity does not match saved seed")
	}
	ep.id = endpointID
	addr := netaddr.NewEndpointAddr(endpointID)
	if ready.Relay != "" {
		id.Relay = ready.Relay
		u, err := parseRelay(id.Relay)
		if err != nil {
			ep.Shutdown(context.Background())
			return nil, "", err
		}
		addr = addr.WithRelayURL(u)
	} else if !cfg.DirectOnly && (len(opts) == 0 || !opts[0].DirectOnly) {
		ep.Shutdown(context.Background())
		return nil, "", errors.New("Iroh host has no connected relay")
	}
	for _, ip := range ready.IPs {
		ap, err := netip.ParseAddrPort(ip)
		if err == nil {
			ep.local = ap
		}
	}
	ticket := encodeTicket(addr, id.Admission)
	go func() {
		for {
			c, err := ep.listener.Accept()
			if err != nil {
				return
			}
			go serveNative(ctx, c, id.Admission, serve)
		}
	}()
	context.AfterFunc(ctx, func() { ep.Shutdown(context.Background()) })
	return ep, ticket, nil
}

func serveNative(ctx context.Context, c net.Conn, admission []byte, serve func(net.Conn)) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	var presented [admissionBytes]byte
	if _, err := io.ReadFull(c, presented[:]); err != nil || subtle.ConstantTimeCompare(presented[:], admission) != 1 {
		return
	}
	if _, err := c.Write([]byte{1}); err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	serve(c)
}

type endpointConn struct {
	net.Conn
	ep   *nativeEndpoint
	once sync.Once
	err  error
}

func (c *endpointConn) Close() error {
	c.once.Do(func() { c.err = c.Conn.Close(); c.ep.Shutdown(context.Background()) })
	return c.err
}

func dialIroh(ctx context.Context, ticket string, opts ...nativeOptions) (net.Conn, error) {
	addr, admission, err := parseTicket(ticket)
	if err != nil {
		return nil, err
	}
	cfg := nativeConfig{Endpoint: addr.ID.String(), IPs: []string{}}
	if urls := addr.RelayURLs(); len(urls) > 0 {
		cfg.Relay = urls[0].String()
	} else {
		cfg.DirectOnly = true
	}
	for _, a := range addr.Addrs() {
		if ip, ok := a.(netaddr.IPAddr); ok {
			cfg.IPs = append(cfg.IPs, ip.Addr.String())
		}
	}
	ep, _, err := startNative(ctx, cfg, opts...)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			ep.Shutdown(context.Background())
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		ep.listener.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { ep.listener.Close() })
	defer stop()
	c, err := ep.listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("Iroh local stream: %w", err)
	}
	defer func() {
		if !keep {
			c.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	stopRead := context.AfterFunc(ctx, func() { c.Close() })
	defer stopRead()
	if _, err = c.Write(admission); err != nil {
		return nil, fmt.Errorf("Iroh admission failed: %w", err)
	}
	var accepted [1]byte
	if _, err = io.ReadFull(c, accepted[:]); err != nil || accepted[0] != 1 {
		return nil, errors.New("Iroh bus admission refused")
	}
	if !stopRead() || ctx.Err() != nil {
		return nil, context.Canceled
	}
	c.SetDeadline(time.Time{})
	keep = true
	return &endpointConn{Conn: c, ep: ep}, nil
}

func dial(ticket string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return dialIroh(ctx, ticket)
}
