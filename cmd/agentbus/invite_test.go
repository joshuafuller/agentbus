package main

import (
	"strings"
	"testing"
)

func TestInviteIsSelfContained(t *testing.T) {
	got := invite("ab1ABC123", "codex-2")
	for _, want := range []string{
		"ab1ABC123", "codex-2", "install.sh", "agentbus join",
		"agentbus send", "review before running", "agentbus wire claude",
		"agentbus wire codex", "confirm with your operator", repoSlug,
		"Iroh build", "ab1", "before continuing", "make install", "Rust",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("invite missing %q", want)
		}
	}
	if strings.Contains(got, "{") && strings.Contains(got, "{TICKET}") {
		t.Error("unexpanded template placeholder in invite")
	}
}
