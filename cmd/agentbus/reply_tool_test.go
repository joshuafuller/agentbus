package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplyToolBoundary(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"reply","arguments":{"to":"operator","message":"DONE proof"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"reply","arguments":{"to":"operator\nother","message":"bad"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"reply","arguments":{"to":"operator","message":"bad\nmessage"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"reply","arguments":{"to":"operator","message":"bad","ticket":"override"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	calls := 0
	err := serveReplyTool(strings.NewReader(input), &out, func(to, msg string) error {
		calls++
		if to != "operator" || msg != "DONE proof" {
			t.Fatalf("unexpected dispatch %q %q", to, msg)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("responses %d: %s", len(lines), out.String())
	}
	for i, line := range lines {
		var response struct {
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if i >= 3 && !response.Result.IsError {
			t.Fatalf("bad input accepted: %s", line)
		}
	}
	if !strings.Contains(lines[1], `"name":"reply"`) {
		t.Fatal("reply not advertised")
	}
}

func TestReplyToolFailureAndCredentialPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reply.json")
	if err := saveReplyToolConfig(path, "private-admission", "rider"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("configuration must be private")
	}
	var out bytes.Buffer
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reply","arguments":{"to":"operator","message":"proof"}}}` + "\n"
	if err := serveReplyTool(strings.NewReader(input), &out, func(string, string) error { return errors.New("private-admission") }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"isError":true`) || strings.Contains(out.String(), "private-admission") {
		t.Fatal("failure leaked credentials or reported success")
	}
}
