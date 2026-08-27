package main

import (
	"slices"
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

// wire must not report success until one probe has actually run
// through the real wake command and produced an answer: "welcome
// aboard" only proves the bus link, and the original deaf-rider
// incident (#1, #8) was a malformed wake command behind a healthy
// join. (Issue #8, item 1.)
func TestSelfTestPassesWorkingCommand(t *testing.T) {
	if err := selfTest(`printf 'OK %s' "$AGENTBUS_MSG"`); err != nil {
		t.Fatalf("working wake command failed self-test: %v", err)
	}
}

func TestSelfTestFailsBrokenCommand(t *testing.T) {
	if err := selfTest(`exit 7`); err == nil {
		t.Fatal("broken wake command passed self-test")
	}
}

func TestSelfTestFailsSilentCommand(t *testing.T) {
	// Exit 0 with no output is the deaf shape: the command "ran" but
	// no turn produced anything — wire must not call that wired.
	if err := selfTest(`true`); err == nil {
		t.Fatal("silent wake command passed self-test")
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

// The rider home is neither a git repo nor a codex-trusted directory;
// without this flag codex exec refuses (current versions) or wedges
// (headless stdin). Both the bootstrap and every resume need it.
func TestCodexCommandsSkipGitRepoCheck(t *testing.T) {
	if cmd := codexOnMsg("/d", "01a0-abcd", ""); !strings.Contains(cmd, "--skip-git-repo-check") {
		t.Errorf("resume lacks --skip-git-repo-check: %q", cmd)
	}
	if args := codexBootArgs("hello briefing", ""); !slices.Contains(args, "--skip-git-repo-check") {
		t.Errorf("bootstrap lacks --skip-git-repo-check: %v", args)
	}
	if args := codexBootArgs("hello", "gpt-5.6-luna"); !slices.Contains(args, "-m") || !slices.Contains(args, "gpt-5.6-luna") {
		t.Errorf("bootstrap lacks model args: %v", args)
	}
}
