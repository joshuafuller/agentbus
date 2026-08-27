// Package bus implements the agentbus line protocol and hub relay.
//
// Wire protocol v0, all lines UTF-8 terminated by \n:
//
//	client -> host, first line:  HELLO <name>
//	client -> host, after:       <text>            (a message)
//	host   -> clients:           [<name>] <text>   (a relayed message)
//	host   -> clients:           * <text>          (a notice; never wakes agents)
package bus

import (
	"fmt"
	"regexp"
	"strings"
)

// validName matches names safe to embed in shell strings, file paths,
// and the wire protocol: letters, digits, dash, underscore, dot.
var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidName reports whether a participant name is safe to use. Names
// reach shell commands (wire --on-msg) and the filesystem (rider dirs),
// so anything outside a conservative charset is rejected at the edge.
func ValidName(name string) bool {
	return validName.MatchString(name)
}

// Hello formats a rider's greeting line (without newline).
func Hello(name string) string {
	return "HELLO " + name
}

// HelloOneshot formats the greeting of a fire-and-forget sender: it
// will write messages but must never receive relays nor displace a
// rider holding the same name.
func HelloOneshot(name string) string {
	return "HELLO " + name + " oneshot"
}

// ParseHello extracts the peer name and mode from a greeting line.
func ParseHello(line string) (name string, oneshot, ok bool) {
	rest, ok := strings.CutPrefix(line, "HELLO ")
	if !ok {
		return "", false, false
	}
	name = strings.TrimSpace(rest)
	if n, found := strings.CutSuffix(name, " oneshot"); found {
		name, oneshot = strings.TrimSpace(n), true
	}
	if name == "" || strings.ContainsAny(name, "[] ") {
		return "", false, false
	}
	return name, oneshot, true
}

// Message formats a relayed message line (without newline).
func Message(from, text string) string {
	return fmt.Sprintf("[%s] %s", from, text)
}

// ParseMessage splits a relayed message line into sender and text.
func ParseMessage(line string) (from, text string, ok bool) {
	if !strings.HasPrefix(line, "[") {
		return "", "", false
	}
	end := strings.Index(line, "] ")
	if end < 1 {
		return "", "", false
	}
	return line[1:end], line[end+2:], true
}

// Addressed formats a line addressed to one rider by name (without
// newline). The hub relays it only to peers holding that name; nobody
// else sees it, so an addressed line never wakes an uninvolved agent.
func Addressed(to, payload string) string {
	return "TO " + to + " " + payload
}

// ParseAddressed splits an addressed line into target name and payload.
func ParseAddressed(line string) (to, payload string, ok bool) {
	rest, found := strings.CutPrefix(line, "TO ")
	if !found {
		return "", "", false
	}
	to, payload, found = strings.Cut(rest, " ")
	if !found || to == "" || payload == "" {
		return "", "", false
	}
	return to, payload, true
}

// Envelope wraps an addressed payload for at-least-once delivery: the
// id is the hub's spool entry id, and the receiver ACKs it back once
// the payload is durably accepted. The hub keeps the entry (and
// redelivers) until then.
func Envelope(id, payload string) string {
	return "E:" + id + " " + payload
}

// ParseEnvelope splits an enveloped payload body into id and payload.
func ParseEnvelope(body string) (id, payload string, ok bool) {
	rest, found := strings.CutPrefix(body, "E:")
	if !found {
		return "", "", false
	}
	id, payload, found = strings.Cut(rest, " ")
	if !found || id == "" || payload == "" {
		return "", "", false
	}
	return id, payload, true
}

// Ack formats a receiver's acknowledgement of one envelope (client →
// hub, consumed by the hub, never relayed).
func Ack(id string) string {
	return "ACKD " + id
}

// ParseAck extracts the envelope id from an acknowledgement line.
func ParseAck(line string) (id string, ok bool) {
	rest, found := strings.CutPrefix(line, "ACKD ")
	if !found {
		return "", false
	}
	id = strings.TrimSpace(rest)
	// One token only: a multi-token "id" can never match a spool entry
	// and would loop through futile removes and redeliveries.
	if id == "" || strings.ContainsAny(id, " \t") {
		return "", false
	}
	return id, true
}

// Notice formats a system notice line (without newline).
func Notice(text string) string {
	return "* " + text
}

// IsNotice reports whether a line is a system notice.
func IsNotice(line string) bool {
	return strings.HasPrefix(line, "* ")
}
