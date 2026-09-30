package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/tmc/go-iroh/key"
)

// Credentials travel over inherited stdin, never argv, logs or a shared socket.
type nativeConfig struct {
	Socket     string   `json:"socket"`
	Seed       string   `json:"seed,omitempty"`
	Endpoint   string   `json:"endpoint,omitempty"`
	Relay      string   `json:"relay"`
	IPs        []string `json:"ips"`
	Bind       string   `json:"bind"`
	RelayOnly  bool     `json:"relay_only"`
	DirectOnly bool     `json:"direct_only"`
}
type nativePath struct {
	ID                           string
	Validated, Selected, Relayed bool
	BytesSent, BytesReceived     uint64
}
type nativeStatus struct {
	Ready bool         `json:"ready"`
	ID    string       `json:"id"`
	Relay string       `json:"relay"`
	IPs   []string     `json:"ips"`
	Paths []nativePath `json:"paths"`
}
type nativeEndpoint struct {
	listener *net.UnixListener
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	dir      string
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	paths    []nativePath
	id       key.EndpointID
	local    netip.AddrPort
}

func (ep *nativeEndpoint) ID() key.EndpointID        { return ep.id }
func (ep *nativeEndpoint) LocalAddr() netip.AddrPort { return ep.local }
func (ep *nativeEndpoint) Closed() <-chan struct{}   { return ep.done }
func (ep *nativeEndpoint) Paths() []nativePath {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	return append([]nativePath(nil), ep.paths...)
}
func (ep *nativeEndpoint) Shutdown(context.Context) error {
	ep.once.Do(func() {
		ep.listener.Close()
		ep.stdin.Close()
		select {
		case <-ep.done:
		case <-time.After(2 * time.Second):
			ep.cmd.Process.Kill()
			<-ep.done
		}
		os.RemoveAll(ep.dir)
	})
	return nil
}
func nativeBinary() (string, error) {
	if p := os.Getenv("AGENTBUS_IROH_BIN"); p != "" {
		return p, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	p := filepath.Join(filepath.Dir(executable), "agentbus-iroh")
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("official Iroh helper missing: install agentbus-iroh beside agentbus, or run make build")
	}
	return p, nil
}
func startNative(ctx context.Context, cfg nativeConfig, opts ...nativeOptions) (*nativeEndpoint, nativeStatus, error) {
	var zero nativeStatus
	binary, err := nativeBinary()
	if err != nil {
		return nil, zero, err
	}
	if cfg.IPs == nil {
		cfg.IPs = []string{}
	}
	cfg.RelayOnly = os.Getenv("AGENTBUS_RELAY_ONLY") == "1"
	if len(opts) > 0 {
		cfg.Bind = opts[0].BindAddr
		cfg.DirectOnly = cfg.DirectOnly || opts[0].DirectOnly
		cfg.RelayOnly = cfg.RelayOnly || opts[0].RelayOnly
	}
	dir, err := os.MkdirTemp("", "ab-")
	if err != nil {
		return nil, zero, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	cfg.Socket = filepath.Join(dir, "stream.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.Socket, Net: "unix"})
	if err != nil {
		return nil, zero, err
	}
	if err = os.Chmod(cfg.Socket, 0600); err != nil {
		listener.Close()
		return nil, zero, err
	}
	cmd := exec.Command(binary)
	// Helper errors can include public peer addresses. Keep stderr out of user logs;
	// return stable errors below rather than accidentally exposing a configuration.
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		listener.Close()
		return nil, zero, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		listener.Close()
		stdin.Close()
		return nil, zero, err
	}
	if err = cmd.Start(); err != nil {
		listener.Close()
		stdin.Close()
		return nil, zero, fmt.Errorf("start official Iroh helper: %w", err)
	}
	ep := &nativeEndpoint{listener: listener, cmd: cmd, stdin: stdin, dir: dir, done: make(chan struct{})}
	ready := make(chan nativeStatus, 1)
	// Drain stdout before Wait, as required by os/exec's pipe lifecycle.
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 16384)
		for scanner.Scan() {
			var status nativeStatus
			if json.Unmarshal(scanner.Bytes(), &status) != nil {
				continue
			}
			if status.Ready {
				select {
				case ready <- status:
				default:
				}
			} else {
				ep.mu.Lock()
				ep.paths = status.Paths
				ep.mu.Unlock()
			}
		}
		cmd.Wait()
		listener.Close()
		close(ep.done)
	}()
	if err = json.NewEncoder(stdin).Encode(cfg); err != nil {
		ep.Shutdown(context.Background())
		return nil, zero, errors.New("Iroh helper configuration failed")
	}
	select {
	case status := <-ready:
		return ep, status, nil
	case <-ep.done:
		err = errors.New("official Iroh helper failed to establish transport (check relay availability and TLS trust)")
	case <-ctx.Done():
		err = ctx.Err()
	}
	ep.Shutdown(context.Background())
	return nil, zero, err
}
