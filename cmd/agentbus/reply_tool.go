package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshuafuller/agentbus/internal/bus"
)

type replyToolConfig struct {
	Ticket string `json:"ticket"`
	Name   string `json:"name"`
}

// Keep credentials outside the rider's writable working directory.
func saveReplyToolConfig(path, ticket, name string) error {
	b, err := json.Marshal(replyToolConfig{Ticket: ticket, Name: name})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".reply-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// runReplyTool is a stdio MCP server launched by Codex outside its command
// sandbox. Only the configured bus and rider are available to the model.
func runReplyTool(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read reply tool configuration")
	}
	var config replyToolConfig
	if json.Unmarshal(b, &config) != nil || !bus.ValidName(config.Name) {
		return fmt.Errorf("invalid reply tool configuration")
	}
	if _, _, err := parseTicket(config.Ticket); err != nil {
		return fmt.Errorf("invalid reply tool ticket")
	}
	return serveReplyTool(os.Stdin, os.Stdout, func(to, msg string) error {
		return runSend(config.Ticket, config.Name, to, msg)
	})
}

// ponytail: one sequential stdio tool; use an MCP SDK if more tools are added.
func serveReplyTool(in io.Reader, out io.Writer, send func(string, string) error) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 128*1024)
	enc := json.NewEncoder(out)
	for scan.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil || req.JSONRPC != "2.0" {
			if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "Invalid JSON-RPC request"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		} // notifications have no response
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "agentbus", "version": "1"}}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{map[string]any{
				"name": "reply", "description": "Send one addressed message to a rider on the operator-configured Agentbus, as this session's fixed rider identity. No URL, ticket, identity override or shell command is accepted.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"to": map[string]any{"type": "string", "description": "Recipient rider name"}, "message": map[string]any{"type": "string", "description": "One non-empty message line, at most 60 KiB"}}, "required": []string{"to", "message"}, "additionalProperties": false},
				"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
			}}}
		case "tools/call":
			var call struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			var args struct {
				To      string `json:"to"`
				Message string `json:"message"`
			}
			err := json.Unmarshal(req.Params, &call)
			if err == nil {
				dec := json.NewDecoder(strings.NewReader(string(call.Arguments)))
				dec.DisallowUnknownFields()
				err = dec.Decode(&args)
			}
			text := "Message accepted by Agentbus."
			failed := err != nil || call.Name != "reply" || !bus.ValidName(args.To) || strings.TrimSpace(args.Message) == "" || len(args.Message) > 60*1024 || strings.ContainsAny(args.Message, "\r\n\x00")
			if failed {
				text = "Invalid reply: supply a valid recipient and one non-empty message line (max 60 KiB)."
			} else if send(args.To, args.Message) != nil {
				failed = true
				text = "Agentbus delivery failed; no success receipt was received."
			}
			response["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": failed}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		if err := enc.Encode(response); err != nil {
			return err
		}
	}
	return scan.Err()
}
