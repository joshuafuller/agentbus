package main

import (
	"strings"
	"testing"
)

func TestVersionOutput(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() { version, commit, date = oldVersion, oldCommit, oldDate })
	version, commit, date = "1.2.3", "abc123", "2026-08-27"

	var out strings.Builder
	printVersion(&out)

	if got, want := out.String(), "agentbus 1.2.3 (abc123, 2026-08-27)\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
