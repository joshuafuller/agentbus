package main

import (
	"strings"
	"testing"

	"github.com/joshuafuller/agentbus/internal/bus"
)

func TestDefaultNameFromHostname(t *testing.T) {
	for _, hostname := range []string{"workstation.local", strings.Repeat("a", 64), strings.Repeat("a", 72) + ".local", "", "bad name!", "münchen"} {
		got := nameFromHostname(hostname)
		if !bus.ValidName(got) {
			t.Errorf("default name invalid for hostname %q: %q", hostname, got)
		}
		if bus.ValidName(hostname) && got != hostname {
			t.Errorf("valid hostname changed: %q", got)
		}
	}
}
