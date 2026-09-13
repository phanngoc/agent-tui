// Package agent wires the Claude Messages API to the local workspace: it owns
// the tool definitions, the sandboxed executor, and the streaming loop that
// feeds the UI.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Approver is asked before any tool that changes state runs. Returning false
// denies the call; the model is told so and can adapt.
type Approver func(ctx context.Context, call session.ToolCall) bool

// Executor runs tool calls against the project, refusing anything outside root.
type Executor struct {
	// FS is where the tools act: the host, or a container this session targets.
	FS       vfs.FS
	Root     string
	Index    *fsx.Index
	MaxBytes int64
	Workers  int

	Approve Approver

	// Tasks holds commands started in the background, so they outlive the turn
	// that asked for them and the user can watch them.
	Tasks *task.Registry
	// Session labels tasks with whoever started them.
	Session string
}

// mutating lists the tools that change something outside this process.
var mutating = map[string]bool{
	"write_file": true, "edit_file": true, "bash": true, "task_stop": true,
}

// NeedsApproval reports whether a call must be confirmed first. Only "ask" mode
// confirms; the others either act freely or never reach a mutating tool at all.
func (e *Executor) NeedsApproval(name string, mode Mode) bool {
	return mode.Confirms() && mutating[name]
}

func str(desc string) param2 { return param2{"type": "string", "description": desc} }
func num(desc string) param2 { return param2{"type": "integer", "description": desc} }
func boolean(desc string) param2 {
	return param2{"type": "boolean", "description": desc}
}

type param2 = map[string]any

func tool(name, desc string, props map[string]any, required ...string) anthropic.ToolUnionParam {
	t := anthropic.ToolParam{
		Name:        name,
		Description: anthropic.String(desc),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: props,
			Required:   required,
		},
	}
	return anthropic.ToolUnionParam{OfTool: &t}
}

// Defs returns the tool schema sent on every request.
//
// Plan mode gets a shorter list rather than a warning: a tool that is not
// offered cannot be called, which is a stronger guarantee than asking the model
// to hold back.
func (e *Executor) Defs(mode Mode) []anthropic.ToolUnionParam {
	defs := []anthropic.ToolUnionParam{
		tool("read_file",
			"Read a UTF-8 text file from the project. Prefer a line range for large files.",
			param2{
				"path":       str("Path relative to the project root."),
				"start_line": num("First line to return, 1-based. Optional."),
				"end_line":   num("Last line to return, inclusive. Optional."),
			}, "path"),

		tool("list_dir",
			"List the entries of a directory in the project.",
			param2{"path": str("Directory relative to the project root. Defaults to the root.")}),

		tool("find_files",
			"Fuzzy-find files by path. Use this to locate a file when you only know part of its name.",
			param2{
				"query": str("Fuzzy query, e.g. 'intsearch' matches 'internal/search/search.go'."),
				"limit": num("Maximum results. Defaults to 30."),
			}, "query"),

		tool("grep",
			"Search file contents across the project. Returns matching lines with their locations.",
			param2{
				"query": str("Literal text, or a Go regular expression when regex is true."),
				"regex": boolean("Treat query as a regular expression. Defaults to false."),
				"path":  str("Restrict the search to this directory prefix. Optional."),
				"limit": num("Maximum matches. Defaults to 80."),
			}, "query"),

		// Asking changes nothing, so it is available in every mode — plan mode
		// especially, where a question may be the whole of the output.
		askUserDef(),
	}

	if !mode.Writes() {
		return defs
	}

	return append(defs,
		tool("write_file",
			"Create a file or overwrite it completely. Requires the user's approval.",
			param2{
				"path":    str("Path relative to the project root."),
				"content": str("The full new contents of the file."),
			}, "path", "content"),

		tool("edit_file",
			"Replace an exact substring in a file. old_string must appear exactly once unless replace_all is set. Requires the user's approval.",
			param2{
				"path":        str("Path relative to the project root."),
				"old_string":  str("Exact text to replace, including surrounding context to make it unique."),
				"new_string":  str("Replacement text."),
				"replace_all": boolean("Replace every occurrence instead of requiring a unique match."),
			}, "path", "old_string", "new_string"),

		tool("bash",
			"Run a shell command in the project root. Requires the user's approval.",
			param2{
				"command":     str("The command to run."),
				"timeout_sec": num("Timeout in seconds. Defaults to 60, maximum 600."),
				"run_in_background": boolean("Start it and return immediately, for anything " +
					"long-running like a build, a test suite or a dev server. Read its output " +
					"later with task_output."),
			}, "command"),

		tool("task_output",
			"Read the output of a background command started by bash.",
			param2{
				"task_id": str("The id bash returned."),
				"lines":   num("How many trailing lines to return. Defaults to 50."),
			}, "task_id"),

		tool("task_stop",
			"Stop a background command.",
			param2{"task_id": str("The id bash returned.")}, "task_id"),
	)
}

// Run dispatches a tool call and returns the text handed back to the model.
func (e *Executor) Run(ctx context.Context, name string, raw json.RawMessage) (string, bool) {
	switch name {
	case "read_file":
		return e.readFile(raw)
	case "list_dir":
		return e.listDir(raw)
	case "find_files":
		return e.findFiles(raw)
	case "grep":
		return e.grep(ctx, raw)
	case "write_file":
		return e.writeFile(raw)
	case "edit_file":
		return e.editFile(raw)
	case "bash":
		return e.bash(ctx, raw)
	case "task_output":
		return e.taskOutput(raw)
	case "task_stop":
		return e.taskStop(raw)
	}
	return "unknown tool: " + name, true
}

// resolve turns a model-supplied path into an absolute one, refusing anything
// that escapes the project root. This is the only place paths are trusted.
func (e *Executor) resolve(p string) (string, error) {
	if p == "" || p == "." {
		return e.Root, nil
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("path %q is outside the project root", p)
	}
	abs := p
	if !strings.HasPrefix(abs, "/") {
		abs = vfs.Join(e.Root, filepath.ToSlash(p))
	}
	if abs != e.Root && !strings.HasPrefix(abs, strings.TrimSuffix(e.Root, "/")+"/") {
		return "", fmt.Errorf("path %q is outside the project root", p)
	}
	return abs, nil
}

func (e *Executor) rel(abs string) string { return vfs.Rel(e.Root, abs) }

// ctx bounds one tool call. Remote filesystems make every read a round trip, so
// nothing is allowed to hang the turn indefinitely.
func toolCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 60*time.Second)
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func (e *Executor) readFile(raw json.RawMessage) (string, bool) {
	var in struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	abs, err := e.resolve(in.Path)
	if err != nil {
		return err.Error(), true
	}
	ctx, cancel := toolCtx(context.Background())
	defer cancel()

	st, err := e.FS.Stat(ctx, abs)
	if err != nil {
		return err.Error(), true
	}
	if st.Dir {
		return in.Path + " is a directory; use list_dir", true
	}
	if st.Size > e.MaxBytes {
		return fmt.Sprintf("file is %d bytes, over the %d byte limit; read a line range instead",
			st.Size, e.MaxBytes), true
	}
	b, _, err := e.FS.ReadFile(ctx, abs, e.MaxBytes)
	if err != nil {
		return err.Error(), true
	}
	if isBinary(b) {
		return fmt.Sprintf("%s looks like a binary file (%d bytes)", in.Path, st.Size), true
	}

	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	start, end := 1, len(lines)
	if in.StartLine > 0 {
		start = in.StartLine
	}
	if in.EndLine > 0 && in.EndLine < end {
		end = in.EndLine
	}
	if start > len(lines) {
		return fmt.Sprintf("start_line %d is past the end of the file (%d lines)", start, len(lines)), true
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (lines %d-%d of %d)\n", e.rel(abs), start, end, len(lines))
	for i := start; i <= end; i++ {
		fmt.Fprintf(&sb, "%6d\t%s\n", i, lines[i-1])
	}
	return sb.String(), false
}

func (e *Executor) listDir(raw json.RawMessage) (string, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	abs, err := e.resolve(in.Path)
	if err != nil {
		return err.Error(), true
	}
	ctx, cancel := toolCtx(context.Background())
	defer cancel()

	entries, err := e.FS.ReadDir(ctx, abs)
	if err != nil {
		return err.Error(), true
	}

	var dirs, files []string
	for _, en := range entries {
		if en.Dir {
			dirs = append(dirs, en.Name+"/")
			continue
		}
		files = append(files, fmt.Sprintf("%s (%s)", en.Name, humanSize(en.Size)))
	}
	sort.Strings(dirs)
	sort.Strings(files)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", e.rel(abs))
	for _, d := range dirs {
		sb.WriteString("  " + d + "\n")
	}
	for _, f := range files {
		sb.WriteString("  " + f + "\n")
	}
	if len(dirs)+len(files) == 0 {
		sb.WriteString("  (empty)\n")
	}
	return sb.String(), false
}

func (e *Executor) findFiles(raw json.RawMessage) (string, bool) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	if in.Limit <= 0 {
		in.Limit = 30
	}
	hits := e.Index.Find(in.Query, in.Limit)
	if len(hits) == 0 {
		return "no files match " + in.Query, false
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d match(es) for %q:\n", len(hits), in.Query)
	for _, h := range hits {
		sb.WriteString("  " + h.Path + "\n")
	}
	return sb.String(), false
}

func (e *Executor) grep(ctx context.Context, raw json.RawMessage) (string, bool) {
	var in struct {
		Query string `json:"query"`
		Regex bool   `json:"regex"`
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	if in.Limit <= 0 {
		in.Limit = 80
	}

	files := e.Index.Files()
	if in.Path != "" && in.Path != "." {
		prefix := strings.TrimSuffix(filepath.ToSlash(in.Path), "/") + "/"
		filtered := make([]string, 0, 64)
		for _, f := range files {
			if strings.HasPrefix(f, prefix) || f == strings.TrimSuffix(prefix, "/") {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}

	root := e.Root
	if in.Path != "" && in.Path != "." {
		var rerr error
		if root, rerr = e.resolve(in.Path); rerr != nil {
			return rerr.Error(), true
		}
	}

	hits, truncated, err := e.FS.Grep(ctx, root, vfs.GrepOptions{
		Query: in.Query, Regex: in.Regex, Limit: in.Limit,
		MaxFileBytes: e.MaxBytes, Workers: e.Workers, Files: files,
	})
	if err != nil {
		return err.Error(), true
	}
	if len(hits) == 0 {
		return fmt.Sprintf("no matches for %q", in.Query), false
	}

	seen := map[string]bool{}
	var sb strings.Builder
	for _, h := range hits {
		seen[h.Path] = true
		fmt.Fprintf(&sb, "%s:%d: %s\n", h.Path, h.Line, strings.TrimSpace(h.Text))
	}
	head := fmt.Sprintf("%d match(es) in %d file(s):\n", len(hits), len(seen))
	if truncated {
		sb.WriteString("(results truncated)\n")
	}
	return head + sb.String(), false
}

func (e *Executor) writeFile(raw json.RawMessage) (string, bool) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	abs, err := e.resolve(in.Path)
	if err != nil {
		return err.Error(), true
	}
	ctx, cancel := toolCtx(context.Background())
	defer cancel()

	_, serr := e.FS.Stat(ctx, abs)
	existed := serr == nil

	if err := e.FS.WriteFile(ctx, abs, []byte(in.Content)); err != nil {
		return err.Error(), true
	}
	verb := "created"
	if existed {
		verb = "overwrote"
	}
	return fmt.Sprintf("%s %s (%d bytes, %d lines)",
		verb, e.rel(abs), len(in.Content), strings.Count(in.Content, "\n")+1), false
}

func (e *Executor) editFile(raw json.RawMessage) (string, bool) {
	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	abs, err := e.resolve(in.Path)
	if err != nil {
		return err.Error(), true
	}
	ctx, cancel := toolCtx(context.Background())
	defer cancel()

	b, _, err := e.FS.ReadFile(ctx, abs, e.MaxBytes)
	if err != nil {
		return err.Error(), true
	}
	src := string(b)
	n := strings.Count(src, in.OldString)
	switch {
	case n == 0:
		return "old_string was not found in " + e.rel(abs), true
	case n > 1 && !in.ReplaceAll:
		return fmt.Sprintf("old_string appears %d times in %s; add surrounding context to make it unique, or set replace_all",
			n, e.rel(abs)), true
	}

	out := src
	if in.ReplaceAll {
		out = strings.ReplaceAll(src, in.OldString, in.NewString)
	} else {
		out = strings.Replace(src, in.OldString, in.NewString, 1)
	}
	if err := e.FS.WriteFile(ctx, abs, []byte(out)); err != nil {
		return err.Error(), true
	}
	replaced := 1
	if in.ReplaceAll {
		replaced = n
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", e.rel(abs), replaced), false
}

func (e *Executor) bash(ctx context.Context, raw json.RawMessage) (string, bool) {
	var in struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeout_sec"`
		Background bool   `json:"run_in_background"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	if strings.TrimSpace(in.Command) == "" {
		return "empty command", true
	}
	timeout := 60 * time.Second
	if in.TimeoutSec > 0 {
		timeout = time.Duration(min(in.TimeoutSec, 600)) * time.Second
	}
	if in.Background {
		return e.bashBackground(in.Command)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The command runs where the files are: locally for the host, inside the
	// container when the session targets one.
	cmd := e.FS.Command(cctx, e.Root, "/bin/sh", "-c", in.Command)
	if e.FS.IsLocal() {
		cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1", "CI=1")
	}
	out, err := cmd.CombinedOutput()

	const maxOut = 60 << 10
	text := string(out)
	if len(text) > maxOut {
		text = text[:maxOut] + "\n… (output truncated)"
	}
	if cctx.Err() == context.DeadlineExceeded {
		return text + fmt.Sprintf("\n(timed out after %s)", timeout), true
	}
	if err != nil {
		return text + "\n(exit: " + err.Error() + ")", true
	}
	if strings.TrimSpace(text) == "" {
		return "(no output, exit 0)", false
	}
	return text, false
}

func isBinary(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}

// askUserTool is answered by the user rather than executed. The agent
// intercepts it before it reaches the executor.
const askUserTool = "ask_user"

// askUserDef is offered alongside the acting tools so the model can put a
// decision to the user instead of guessing at one.
func askUserDef() anthropic.ToolUnionParam {
	return tool(askUserTool,
		"Ask the user to choose between options. Use it when the answer changes what you "+
			"would do next and you cannot settle it from the code or the request. Do not use "+
			"it for questions you can answer yourself.",
		param2{
			"question": str("The question, in one sentence."),
			"options": param2{
				"type":        "array",
				"description": "Two to four distinct choices.",
				"items": param2{
					"type": "object",
					"properties": param2{
						"label":       param2{"type": "string", "description": "Short label, one to five words."},
						"description": param2{"type": "string", "description": "What choosing this means."},
					},
					"required": []string{"label"},
				},
			},
		}, "question", "options")
}

// bashBackground starts a command and returns straight away.
//
// It is deliberately detached from the turn's context: cancelling a turn should
// not kill a build that was started precisely so the conversation could move on
// while it ran.
func (e *Executor) bashBackground(command string) (string, bool) {
	if e.Tasks == nil {
		return "background commands are not available here", true
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := e.FS.Command(ctx, e.Root, "/bin/sh", "-c", command)
	if e.FS.IsLocal() {
		cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1", "CI=1")
	}

	t := e.Tasks.Start(e.Root, firstLine(command), cmd, cancel, e.Session)
	return fmt.Sprintf("started in the background as %s; read it with task_output", t.ID), false
}

func (e *Executor) taskOutput(raw json.RawMessage) (string, bool) {
	var in struct {
		TaskID string `json:"task_id"`
		Lines  int    `json:"lines"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	if e.Tasks == nil {
		return "background commands are not available here", true
	}
	t := e.Tasks.Get(in.TaskID)
	if t == nil {
		return "no background command with id " + in.TaskID, true
	}
	if in.Lines <= 0 {
		in.Lines = 50
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %s after %s\n", t.ID, t.State(), t.Elapsed().Round(time.Millisecond))
	lines := t.Tail(in.Lines)
	if len(lines) == 0 {
		sb.WriteString("(no output yet)\n")
	}
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	return sb.String(), false
}

func (e *Executor) taskStop(raw json.RawMessage) (string, bool) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := decode(raw, &in); err != nil {
		return err.Error(), true
	}
	if e.Tasks == nil {
		return "background commands are not available here", true
	}
	t := e.Tasks.Get(in.TaskID)
	if t == nil {
		return "no background command with id " + in.TaskID, true
	}
	t.Stop()
	return "asked " + t.ID + " to stop", false
}

// firstLine trims a command down to something that fits on one transcript row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return strings.TrimSpace(s)
}

// ShouldAsk decides whether a call needs the user's word, and why.
//
// Ask mode confirms everything that changes state. Auto mode confirms only what
// falls outside the project, because that is the boundary the mode promises;
// acting outside it silently would be a different mode than the one on screen.
func (e *Executor) ShouldAsk(call session.ToolCall, mode Mode, trusted bool) (bool, string) {
	if trusted || !mutating[call.Name] {
		return false, ""
	}
	switch mode {
	case ModeAsk:
		return true, "ask mode confirms every change"
	case ModeAuto:
		if inside, decided := call.PathsInside(e.Root); decided && inside {
			return false, ""
		}
		if call.Name == "bash" {
			return false, "" // auto runs commands in the project; the shell is not a path
		}
		return true, "this is outside " + e.Root
	default:
		return false, ""
	}
}
