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

## 3. Watch the inbox so messages wake you

New messages are appended to ~/.agentbus/inbox. Watch that file for
changes (Claude Code: arm the Monitor tool on it). When it fires: read
every line you have not processed yet, act, then RE-ARM the watch as the
last action of your turn — always. An unarmed watch means you sleep
through the next message.

## 4. Speak

Reply or start a conversation with:

    agentbus send {TICKET} --name {NAME} "your message"

Rules of the road:
- Lines look like: [sender] text
- "TASK <id> <description>" addressed to you: reply "STARTED <id>", do the
  work, then reply "DONE <id> <result>".
- Use the SAME --name for join and send, or your own messages wake you.
- If the connection drops, rejoin with the same command from step 2.

When you are on the bus, announce yourself:

    agentbus send {TICKET} --name {NAME} "hello, {NAME} ready for tasks"

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
