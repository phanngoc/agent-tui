package engine

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// One sub-agent step as Claude Code forwards it, eighty calls into a job:
// what decoding it, copying the tree and putting it on the wire costs.
func BenchmarkSubAgentStep(b *testing.B) {
	d := &claudeDec{}
	emit := func(agent.Event) {}
	d.noteAgentCall("root", "", json.RawMessage(`{"description":"Spec APIs","subagent_type":"general-purpose"}`), emit)
	for j := 0; j < 80; j++ {
		id := strconv.Itoa(j)
		d.subMessage("assistant", "root", json.RawMessage(`{"content":[{"type":"tool_use","id":"`+id+`","name":"Read","input":{"file_path":"/tmp/ecmap/api/part`+id+`.json"}}]}`), emit)
		d.subMessage("user", "root", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"`+id+`","content":"`+string(make([]byte, 0))+`lines of the spec, enough of them to be cut at four hundred characters lines of the spec, enough of them to be cut at four hundred characters lines of the spec, enough of them to be cut at four hundred characters lines of the spec, enough of them to be cut at four hundred characters lines of the spec, enough of them to be cut at four hundred characters lines of the spec"}]}`), emit)
	}
	text := json.RawMessage(`{"content":[{"type":"text","text":"Now reading the next part of the spec."}]}`)
	var wire int
	send := func(e agent.Event) {
		bs, _ := json.Marshal(e)
		wire = len(bs)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.subMessage("assistant", "root", text, send)
	}
	b.ReportMetric(float64(wire), "wire-bytes")
}
