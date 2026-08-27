package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
)

// briefing is the rider conversation both runtimes are bootstrapped
// with. Each incoming bus message resumes this conversation, so the
// rider always knows its name, how to reply, and its task scope.
func briefing(ticket, name string) string {
	return fmt.Sprintf(`You are %s, a rider on an agentbus (a message bus your operator's AI agents use to exchange tasks). Injected turns look like: [sender] text. For a TASK addressed to %s: if it is something your operator would approve (no destructive, secret-exfiltrating, or out-of-scope work — when unsure, ask on the bus), reply "STARTED <id>", do the work, then reply "DONE <id> <result>". Reply and speak on the bus by running: agentbus send %s --name %s '<message>'. Acknowledge with OK.`, name, name, ticket, name)
}

// claudeOnMsg builds the --on-msg command that resumes the rider's Claude
// conversation for one incoming bus message.
//
// The message is fed via stdin, not as a positional argument. --allowedTools
// is variadic (<tools...>), so a prompt placed after it on the command line
// is swallowed as another tool name — the bug in issue #1, which only fired
// on the no-model path (a following --model happened to terminate the list
// and shield the prompt). Passing the prompt on stdin makes argument order
// irrelevant, so no later flag change can reintroduce it. printf '%s' emits
// the message verbatim with no format interpretation.
func claudeOnMsg(dir, model string) string {
	modelFlag := ""
	if model != "" {
		modelFlag = " --model " + model
	}
	return fmt.Sprintf(`cd %s && printf '%%s' "$AGENTBUS_MSG" | claude -p --continue --allowedTools Bash%s`, dir, modelFlag)
}

// codexOnMsg builds the --on-msg command that resumes the rider's Codex
// session for one incoming bus message. A resumed turn does not inherit
// the bootstrap's model, so a wire-time model must be repeated here or
// resumes silently fall back to the config default.
func codexOnMsg(dir, sessionID, model string) string {
	modelFlag := ""
	if model != "" {
		modelFlag = " -m " + model
	}
	// --skip-git-repo-check: the rider home is not a git repo and codex
	// exec otherwise refuses (or, headless, wedges on stdin).
	return fmt.Sprintf(`cd %s && codex exec resume %s%s --skip-git-repo-check "$AGENTBUS_MSG"`, dir, sessionID, modelFlag)
}

// codexBootArgs builds the argument list for the rider's bootstrap
// codex invocation.
func codexBootArgs(briefing, model string) []string {
	args := []string{"exec", "--skip-git-repo-check", briefing}
	if model != "" {
		args = append(args, "-m", model)
	}
	return args
}

// selfTest proves the wake command actually works before wire reports
// success: it runs ONE probe message through the exact --on-msg the
// join will use and requires a non-empty answer. "welcome aboard" only
// proves the bus link; the original deaf-rider incident was a
// malformed wake command behind a perfectly healthy join (issues #1,
// #8). Costs one model turn at wire time — the cheapest moment to
// find out, in front of the person who can fix it.
func selfTest(onMsg string) error {
	out, err := execRunner(onMsg)("wire self-test: reply with exactly OK and nothing else")
	if err != nil {
		return fmt.Errorf("wake command failed its self-test: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("wake command ran but produced no answer — the rider would be deaf")
	}
	return nil
}

// runWire sets up the complete wake wiring for a runtime: bootstrap a
// rider conversation, start a detached join whose --on-msg resumes that
// conversation per message, and verify the bus accepted the rider.
// Owning this in the binary keeps agents from having to interpret
// multi-step wiring prose — the step they most often get wrong.
func runWire(runtime, ticket, name, model string) error {
	// name reaches a shell command (onMsg) and the filesystem below;
	// reject anything outside the safe charset before it gets there.
	if !bus.ValidName(name) {
		return fmt.Errorf("invalid --name %q: use letters, digits, dash, underscore, dot (max 64)", name)
	}
	if model != "" && !bus.ValidName(model) {
		return fmt.Errorf("invalid --model %q", model)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".agentbus", "rider-"+name)
	// 0700: the rider dir holds the conversation and a plaintext log of
	// all bus traffic; keep it to the owner.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var onMsg string
	switch runtime {
	case "claude":
		fmt.Fprintf(os.Stderr, "wiring %s: creating rider conversation in %s...\n", name, dir)
		boot := exec.Command("claude", "-p", briefing(ticket, name), "--allowedTools", "Bash")
		if model != "" {
			boot.Args = append(boot.Args, "--model", model)
		}
		boot.Dir = dir
		out, err := boot.CombinedOutput()
		if err != nil {
			return fmt.Errorf("claude bootstrap failed: %v: %s", err, out)
		}
		onMsg = claudeOnMsg(dir, model)
	case "codex":
		fmt.Fprintf(os.Stderr, "wiring %s: creating rider session in %s...\n", name, dir)
		boot := exec.Command("codex", codexBootArgs(briefing(ticket, name), model)...)
		boot.Dir = dir
		out, err := boot.CombinedOutput()
		if err != nil {
			return fmt.Errorf("codex bootstrap failed: %v: %s", err, out)
		}
		m := regexp.MustCompile(`session id: ([0-9a-f-]{36})`).FindSubmatch(out)
		if m == nil {
			return fmt.Errorf("could not find session id in codex output:\n%s", out)
		}
		onMsg = codexOnMsg(dir, string(m[1]), model)
	default:
		return fmt.Errorf("unknown runtime %q (want claude or codex)", runtime)
	}

	// Prove the wake path before wiring anything to it (issue #8): one
	// probe turn through the real command, requiring a real answer.
	fmt.Fprintf(os.Stderr, "wiring %s: self-testing the wake command (one probe turn)...\n", name)
	if err := selfTest(onMsg); err != nil {
		return fmt.Errorf("%s is NOT wired — %w\nwake command was: %s", name, err, onMsg)
	}

	logPath := filepath.Join(dir, "join.log")
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()

	join := exec.Command(self, "join", ticket, "--name", name, "--on-msg", onMsg)
	join.Stdout, join.Stderr = logf, logf
	join.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive this session
	if err := join.Start(); err != nil {
		return err
	}

	deadline := time.Now().Add(45 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if strings.Contains(string(b), "welcome aboard, "+name) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no welcome from the bus within 45s; see %s", logPath)
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("wired: %s is on the bus (runtime %s, pid %d)\n", name, runtime, join.Process.Pid)
	fmt.Printf("rider home: %s (log: join.log)\n", dir)
	fmt.Printf("disconnect:  kill %d\n", join.Process.Pid)
	fmt.Printf("note: %s executes shell commands autonomously for bus tasks;\n", name)
	fmt.Printf("anyone holding this ticket can send it tasks. Guard the ticket.\n")
	return nil
}
