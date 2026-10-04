package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
)

// Served is a tool this process offers as an MCP server.
type Served struct {
	Name        string
	Description string
	InputSchema map[string]any
	Run         func(ctx context.Context, args json.RawMessage) (string, bool)
}

// Serve answers MCP over newline-delimited JSON-RPC on r and w until r ends:
// enough of the protocol — initialize, ping, tools/list, tools/call — for a
// client such as Claude Code to use the tools.
func Serve(ctx context.Context, r io.Reader, w io.Writer, name string, tools []Served) error {
	byName := map[string]Served{}
	list := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": schema})
	}
	var mu sync.Mutex
	reply := func(id json.RawMessage, result any, rpcErr *rpcError) {
		msg := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr != nil {
			msg["error"] = rpcErr
		} else {
			msg["result"] = result
		}
		b, _ := json.Marshal(msg)
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(append(b, '\n'))
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue // notifications need no answer
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			v := p.ProtocolVersion
			if v == "" {
				v = protocolVersion
			}
			reply(req.ID, map[string]any{
				"protocolVersion": v,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": name, "version": "1"},
			}, nil)
		case "ping":
			reply(req.ID, map[string]any{}, nil)
		case "tools/list":
			reply(req.ID, map[string]any{"tools": list}, nil)
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			t, ok := byName[p.Name]
			if !ok {
				reply(req.ID, nil, &rpcError{Code: -32602, Message: "unknown tool " + p.Name})
				continue
			}
			out, isErr := t.Run(ctx, p.Arguments)
			reply(req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": out}},
				"isError": isErr,
			}, nil)
		default:
			reply(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
	return sc.Err()
}
