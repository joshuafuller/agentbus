//go:build natlab

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
)

// This runs only in the disposable Docker lab, using the production transport.
func TestNATProbe(t *testing.T) {
	data, err := os.ReadFile("/tmp/ticket")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 240*time.Second)
	defer cancel()
	c, err := dialIroh(ctx, strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sc := bufio.NewScanner(c)
	k, err := bus.LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.ClientHello(c, sc, "nat-probe", false, k); err != nil {
		t.Fatal(err)
	}
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome aboard") {
		t.Fatal("no welcome")
	}
	for ctx.Err() == nil {
		if _, err := os.Stat("/tmp/stop"); err == nil {
			return
		}
		c.SetDeadline(time.Now().Add(45 * time.Second))
		if _, err := fmt.Fprintln(c, bus.Ping()); err != nil {
			t.Fatal(err)
		}
		pong := false
		for sc.Scan() {
			if bus.IsPong(sc.Text()) {
				pong = true
				break
			}
		}
		if !pong {
			t.Fatalf("no PONG: %v", sc.Err())
		}
		phase, _ := os.ReadFile("/tmp/phase")
		row := struct {
			Phase   string `json:"phase"`
			Paths   any    `json:"paths"`
			Report  any    `json:"report"`
			Metrics any    `json:"metrics"`
		}{strings.TrimSpace(string(phase)), c.(*endpointConn).ep.Paths(), nil, nil}
		encoded, _ := json.Marshal(row)
		fmt.Println(string(encoded))
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("probe timed out")
}
