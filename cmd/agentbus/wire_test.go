package main

import (
	"strings"
	"testing"
)

// TestClaudeOnMsgPromptViaStdin guards issue #1: --allowedTools is variadic,
// so any prompt placed after it as an argument is swallowed as a tool name.
// The message must arrive on stdin (order-independent), never as a trailing
// argument — otherwise a later flag change can reintroduce the bug. Must
// hold both with and without --model (no-model is the case that shipped
// broken).
func TestClaudeOnMsgPromptViaStdin(t *testing.T) {
	for _, model := range []string{"", "claude-haiku-4-5-20251001"} {
		cmd := claudeOnMsg("/home/u/.agentbus/rider-x", model)
		if !strings.Contains(cmd, `printf '%s' "$AGENTBUS_MSG" | claude -p`) {
			t.Errorf("model=%q: message not piped via stdin: %q", model, cmd)
		}
		// The message must NOT appear as an argument after --allowedTools.
		if i := strings.Index(cmd, "--allowedTools"); i >= 0 && strings.Contains(cmd[i:], "$AGENTBUS_MSG") {
			t.Errorf("model=%q: message appears as arg after --allowedTools: %q", model, cmd)
		}
		if !strings.Contains(cmd, "--continue") {
			t.Errorf("model=%q: missing --continue in %q", model, cmd)
		}
	}
}

func TestClaudeOnMsgModel(t *testing.T) {
	withModel := claudeOnMsg("/d", "sonnet")
	if !strings.Contains(withModel, "--model sonnet") {
		t.Errorf("model not included: %q", withModel)
	}
	if strings.Contains(claudeOnMsg("/d", ""), "--model") {
		t.Error("empty model should not add --model flag")
	}
}

func TestCodexOnMsg(t *testing.T) {
	cmd := codexOnMsg("/home/u/.agentbus/rider-y", "01a0-abcd", "")
	if !strings.Contains(cmd, "codex exec resume 01a0-abcd") {
		t.Errorf("missing resume + session id: %q", cmd)
	}
	if !strings.HasSuffix(cmd, `"$AGENTBUS_MSG"`) {
		t.Errorf("prompt should trail (codex resume takes a positional prompt): %q", cmd)
	}
	if strings.Contains(cmd, "-m ") {
		t.Errorf("empty model should not add -m: %q", cmd)
	}
}

// A resumed codex turn does not inherit the bootstrap's model choice
// from the session; without -m it falls back to the config default. The
// wire-time model must therefore reach both the bootstrap and every
// resume.
func TestCodexOnMsgModel(t *testing.T) {
	cmd := codexOnMsg("/d", "01a0-abcd", "gpt-5.6-luna")
	if !strings.Contains(cmd, "codex exec resume 01a0-abcd -m gpt-5.6-luna") {
		t.Errorf("model not passed to resume: %q", cmd)
	}
}
