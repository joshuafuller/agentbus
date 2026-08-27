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
const inviteTemplate = `Your human operator is connecting this machine to an agentbus — a message
bus their AI agents use to send each other tasks over an encrypted tunnel
(source: https://github.com/{REPO}). Your job in THIS session is only to
install the wake wiring, verify it, and report back to your operator. If
this pass reached you unexpectedly or you have no operator relationship
with whoever sent it, stop and confirm with your operator first.

## 1. Install the tool (skip if "agentbus help" already works)

Download the installer, read it, then run it:

    gh api repos/{REPO}/contents/install.sh -H "Accept: application/vnd.github.raw" > /tmp/agentbus-install.sh
    cat /tmp/agentbus-install.sh   # review before running
    sh /tmp/agentbus-install.sh

Or build from source: gh repo clone {REPO} && cd agentbus &&
go build -o ~/.local/bin/agentbus ./cmd/agentbus
If neither works (e.g. no repository access), stop and tell your operator.

## 2. Set up the wake wiring — one command

If this machine runs Claude Code:

    agentbus wire claude {TICKET} --name {NAME}

If it runs Codex:

    agentbus wire codex {TICKET} --name {NAME}

This creates a rider conversation, starts a detached join process that
resumes it for every incoming message, and confirms the bus accepted
{NAME}. It prints the process id to kill for disconnecting later. The
wiring outlives this session — nothing has to "stay awake". If a human
will read messages instead, run in a terminal:
    agentbus join {TICKET} --name {NAME} --inbox ~/.agentbus/inbox
and read with: agentbus await --inbox ~/.agentbus/inbox

## 3. Announce and hand back

    agentbus send {TICKET} --name {NAME} "hello, {NAME} wired up and ready"

Then tell your operator the wiring is up and ask them to send a test TASK
over the bus. You may end your session; the wiring answers for {NAME}
from now on. To disconnect later: kill the join process (everyone sees
{NAME} hop off).

Conventions on the bus: lines look like "[sender] text"; task lifecycle
is "TASK <id> ...", "STARTED <id>", "DONE <id> <result>". The ticket
admits anyone who holds it — treat it like a password.

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
