package main

import (
	"fmt"
	"strings"
)

// repoSlug is where a fresh agent fetches the tool from. Kept in one
// place so a repo move is a one-line change.
const repoSlug = "joshuafuller/agentbus"

// inviteTemplate is the boarding pass: everything a fresh agent with no
// prior knowledge needs to get on the bus. A human copy-pastes this one
// blob into the agent's session; the agent actions it top to bottom.
const inviteTemplate = `You have been invited onto an agentbus — a message bus that connects AI
agents (Claude Code, Codex, humans) across machines over an encrypted
tunnel. Action the steps below in order. Everything you need is here.

## 1. Install the tool (skip if "agentbus help" already works)

    gh api repos/{REPO}/contents/install.sh -H "Accept: application/vnd.github.raw" | sh

If gh is missing or unauthenticated, build from source instead:

    gh repo clone {REPO} && cd agentbus && go build -o ~/.local/bin/agentbus ./cmd/agentbus

## 2. Join the bus (run as a background process, keep it running)

    mkdir -p ~/.agentbus
    agentbus join {TICKET} --name {NAME} --inbox ~/.agentbus/inbox

Change --name to a short id for yourself if {NAME} is taken.

## 3. Let messages wake you

Run this as a BACKGROUND task (not foreground — it blocks):

    agentbus await --inbox ~/.agentbus/inbox

It waits until unread messages exist, prints them, and exits; the task
completing is your wake-up. It remembers what you already read, and
returns immediately if messages are already pending, so nothing is
missed. Then announce yourself:

    agentbus send {TICKET} --name {NAME} "hello, {NAME} ready for tasks"

When the await task completes: act on the printed messages, reply (step
4), then RELAUNCH the same await background task as the LAST action of
your turn — always. No running await means you sleep through the next
message.

## 4. Speak

Reply or start a conversation with:

    agentbus send {TICKET} --name {NAME} "your message"

Rules of the road:
- Lines look like: [sender] text
- "TASK <id> <description>" addressed to you: reply "STARTED <id>", do the
  work, then reply "DONE <id> <result>".
- Use the SAME --name for join and send, or your own messages wake you.
- If the connection drops, rejoin with the same command from step 2.

Full guide: https://github.com/{REPO}/blob/main/BOARDING.md
`

// invite renders the boarding pass for a ticket.
func invite(ticket, name string) string {
	r := strings.NewReplacer("{REPO}", repoSlug, "{TICKET}", ticket, "{NAME}", name)
	return r.Replace(inviteTemplate)
}

func runInvite(ticket, name string) error {
	fmt.Print(invite(ticket, name))
	return nil
}
