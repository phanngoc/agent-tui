package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
)

// The permission broker lets an external CLI ask *this* UI before it runs a
// tool.
//
// Claude Code routes permission prompts to an MCP tool named by
// --permission-prompt-tool. MCP servers are spawned by the CLI, not by us, so
// agent-tui re-executes itself in broker mode: that child speaks MCP over stdio
// to the CLI and forwards each request over a unix socket to the running UI,
// which shows the same approval prompt the built-in agent uses.
//
//	claude  --(MCP stdio)-->  agent-tui --permission-broker  --(unix socket)-->  UI

// BrokerToolName is the MCP tool the CLI is told to call.
const BrokerToolName = "approve"

// brokerServerName is the MCP server key; the CLI addresses the tool as
// mcp__<server>__<tool>.
const brokerServerName = "agenttui"

// askRequest is what the broker child forwards to the UI.
type askRequest struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// askReply is the UI's answer.
type askReply struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

// broker is the UI-side half: a unix socket that turns forwarded requests into
// approval events on the turn's event channel.
type broker struct {
	dir  string
	path string
	self string // path to this binary, re-executed in broker mode
	ln   net.Listener
	wg   sync.WaitGroup
}

// startBroker listens on a fresh unix socket. ask is called for every request
// and must block until the user answers.
func startBroker(ask func(session.ToolCall) bool) (*broker, error) {
	dir, err := os.MkdirTemp("", "agent-tui-")
	if err != nil {
		return nil, err
	}
	// Unix socket paths are limited to ~104 bytes on macOS, so keep it short.
	path := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	b := &broker{dir: dir, path: path, ln: ln}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				defer conn.Close()
				b.serve(conn, ask)
			}()
		}
	}()
	return b, nil
}

func (b *broker) serve(conn net.Conn, ask func(session.ToolCall) bool) {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var req askRequest
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		allow := ask(session.ToolCall{
			ID:    req.ToolUseID,
			Name:  req.ToolName,
			Input: req.Input,
		})
		reply, _ := json.Marshal(askReply{Allow: allow})
		if _, err := conn.Write(append(reply, '\n')); err != nil {
			return
		}
	}
}

// MCPConfig is the --mcp-config payload that points the CLI at this broker.
func (b *broker) MCPConfig() string {
	self := b.self
	cfg := map[string]any{
		"mcpServers": map[string]any{
			brokerServerName: map[string]any{
				"command": self,
				"args":    []string{"--permission-broker", b.path},
			},
		},
	}
	out, _ := json.Marshal(cfg)
	return string(out)
}

// ToolRef is the fully qualified MCP tool name the CLI should call.
func (b *broker) ToolRef() string {
	return fmt.Sprintf("mcp__%s__%s", brokerServerName, BrokerToolName)
}

func (b *broker) Close() {
	_ = b.ln.Close()
	b.wg.Wait()
	_ = os.RemoveAll(b.dir)
}

// ---- broker child process ---------------------------------------------------

// RunBroker is the entry point for `agent-tui --permission-broker <socket>`.
// It speaks just enough MCP to expose one tool and forwards every call to the
// UI over sock. It is deliberately tolerant: any failure denies the call rather
// than hanging the CLI that is waiting on it.
func RunBroker(sock string) error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 32<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	respond := func(id any, result any) {
		if id == nil {
			return
		}
		b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		if err != nil {
			return
		}
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}

	for in.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "agent-tui", "version": "1"},
			})
		case "tools/list":
			respond(req.ID, map[string]any{"tools": []any{map[string]any{
				"name":        BrokerToolName,
				"description": "Ask the agent-tui user to approve a tool call.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool_name": map[string]any{"type": "string"},
						"input":     map[string]any{"type": "object"},
					},
					"required": []string{"tool_name", "input"},
				},
			}}})
		case "tools/call":
			respond(req.ID, brokerCall(sock, req.Params))
		case "ping":
			respond(req.ID, map[string]any{})
		default:
			// Notifications carry no id and need no reply.
			respond(req.ID, map[string]any{})
		}
	}
	return in.Err()
}

// brokerCall forwards one approval request and shapes the verdict the way
// Claude Code expects: a single text block holding {"behavior": ...}.
func brokerCall(sock string, params json.RawMessage) map[string]any {
	var p struct {
		Arguments struct {
			ToolName  string          `json:"tool_name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)

	verdict := map[string]any{
		"behavior": "deny",
		"message":  "agent-tui could not reach the approval prompt",
	}
	if allow, err := askUI(sock, askRequest{
		ToolName:  p.Arguments.ToolName,
		Input:     p.Arguments.Input,
		ToolUseID: p.Arguments.ToolUseID,
	}); err == nil {
		if allow {
			input := p.Arguments.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			verdict = map[string]any{"behavior": "allow", "updatedInput": input}
		} else {
			verdict = map[string]any{
				"behavior": "deny",
				"message":  "The user denied this tool call. Do not retry it.",
			}
		}
	}
	body, _ := json.Marshal(verdict)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(body)}}}
}

func askUI(sock string, req askRequest) (bool, error) {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	line, err := json.Marshal(req)
	if err != nil {
		return false, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return false, err
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return false, err
		}
		return false, errors.New("approval connection closed")
	}
	var reply askReply
	if err := json.Unmarshal(sc.Bytes(), &reply); err != nil {
		return false, err
	}
	return reply.Allow, nil
}

// ctxDone is a tiny helper used by the CLI runner to stop waiting on an
// approval when the turn is cancelled.
func ctxDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
