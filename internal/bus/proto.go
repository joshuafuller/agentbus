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
	"strings"
)

// Hello formats the client greeting line (without newline).
func Hello(name string) string {
	return "HELLO " + name
}

// ParseHello extracts the peer name from a greeting line.
func ParseHello(line string) (name string, ok bool) {
	rest, ok := strings.CutPrefix(line, "HELLO ")
	if !ok {
		return "", false
	}
	name = strings.TrimSpace(rest)
	if name == "" || strings.ContainsAny(name, "[]") {
		return "", false
	}
	return name, true
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

// Notice formats a system notice line (without newline).
func Notice(text string) string {
	return "* " + text
}

// IsNotice reports whether a line is a system notice.
func IsNotice(line string) bool {
	return strings.HasPrefix(line, "* ")
}
