# agent-tui

A terminal coding agent with Warp-style session blocks and a Sublime-style file
pane. Multi-session, keyboard-driven, and fast on large repositories.

```
┌ agent-tui · myrepo   [fix ranking] [new session]────────────────────────────┐
│ sessions │ transcript                        │ internal/search/rank.go  Go  │
│ ▸ fix…   │ ▎ you            14:02            │  10 │ func score(p string)…  │
│   audit… │ ▎ make search rank shorter paths  │  11 │     depth := strings…  │
│          │ ▎ agent          14:02            │  12 │     return base - de…  │
│          │ ▎ Looking at the ranking now.     │  13 │ }                      │
│          │   ✓ grep   "score"   8ms          │                              │
│          │   ✓ edit_file  rank.go   2ms      │                              │
└──────────┴───────────────────────────────────┴──────────────────────────────┘
❯ ask anything…
```

## Install

```sh
go install github.com/phanngoc/agent-tui/cmd/agent-tui@latest
# or, from a clone:
make build && ./bin/agent-tui
```

Requires Go 1.26+ and an Anthropic credential: either `ANTHROPIC_API_KEY` in the
environment, or a profile from `ant auth login` — the SDK finds both on its own.

```sh
agent-tui              # the current directory
agent-tui -C ~/code/x  # somewhere else
agent-tui -yes         # skip approval prompts for write and bash
```

## Engines

The same UI drives four different agents. Press `ctrl+r` to pick one per
session; sessions with different engines run side by side.

| Engine | How it runs | Asks before acting |
|---|---|---|
| `api` | built-in, talks to the Anthropic API in this process | yes |
| `claude` | `claude -p --output-format stream-json` | **yes** — see below |
| `codex` | `codex exec --json` | no — confined by `--sandbox` |
| `opencode` | `opencode run --format json` | no — its own config governs it |

Each CLI's events are normalised into the same transcript, so a tool call looks
the same whoever made it, and each session keeps the CLI's own session id so a
turn can be resumed later.

**Claude Code approvals really are intercepted.** Claude Code routes permission
prompts to an MCP tool; agent-tui re-executes itself as that tool
(`--permission-broker`), which forwards each request over a unix socket to the
running UI and answers with your verdict:

```
claude  ──MCP stdio──▶  agent-tui --permission-broker  ──unix socket──▶  the prompt you see
```

`codex exec` and `opencode run` are non-interactive and expose no approval
channel, so there is nothing to intercept. The engine picker says so per engine
rather than implying a gate that does not exist; `-perms` chooses between `ask`,
`workspace` and `bypass`.

## Panes

**Project explorer** — a lazily expanded tree of the directory the active
session's agent is actually working in — on the host, or inside a container.
`r` on a directory repoints that session: the tree follows, and so does the
`-C` / `--dir` the CLI is given.

**Transcript** — every turn is a block: who spoke, when, and the tool calls the
agent made, each with its status and how long it took. Failed and denied calls
show the first lines of their output inline.

**Preview** — a file viewer with line numbers, syntax highlighting, in-file
search with match highlighting, soft-wrap on demand, and an editor: `e` turns
the pane you are reading into a writable buffer.

In-file search draws its own highlights rather than using the viewport's.
Bubbles' `SetHighlights` walks the ANSI-stripped content to advance a byte
position but indexes the *unstripped* content to count newlines, so on
syntax-coloured text every position drifts by the number of escape bytes before
it — searching for `foo` highlighted `Sta` three lines away. Matches are
therefore tracked per line in display columns and spliced into the styled line
at render time, so there is no cross-line offset arithmetic to get wrong. It reloads itself when
the agent edits the file you are looking at.

**Sessions** — conversations run concurrently. Starting a turn in one session
does not block the others; a session that needs approval pulls itself to the
front, because its agent is waiting on you.

## Editing

`e` in the preview opens the file for editing; `ctrl+s` saves, `ctrl+z` and
`ctrl+y` undo and redo, `esc` closes. The title carries `editing ●` while there
is unsaved work, a second `esc` is required to throw it away, and `ctrl+c` will
not quit the app over a dirty buffer.

Saving goes through the session's filesystem, so editing a file in a
container-targeted session writes it *in* the container — the same file the
agent is looking at, not a same-named one on the host.

Two deliberate limits. The buffer drops syntax colour while you type, because a
textarea treats ANSI escapes as editable characters and you would be able to put
the cursor in the middle of one; colour comes back on save, when the file is
reloaded through the highlighter. And files over 5000 lines or 512 KB, and
anything that looks binary, open read-only rather than loading a buffer that
would be slow to edit and easy to corrupt.

Undo is ours, not the textarea's: bubbles has no undo stack, so the editor keeps
snapshots and coalesces a typing run into a single step — otherwise one `ctrl+z`
per character is not undo, it is a rewind.

## Project search

`ctrl+f` with no file open, or `ctrl+g` anywhere, searches every file under the
current directory and groups the hits the way Warp does: one header per file
with its name, its directory, and a count badge, and under it each matching line
with the match picked out and the line windowed around it so a hit in a long
line is still visible.

```
▾ main.go  cmd/agent-tui                          9
   43         model = flag.String("model", cfg.Model, "Claude model id")
   74     mode := agent.ParseMode(*modeFlag)
▾ agent.go  internal/agent                        7
  106     // Mode is how much the agent may do without asking on this turn.
```

`←` and `→` fold a file away and back, `enter` opens the hit in the preview at
its line, `alt+a` switches to exact case and `alt+r` to regex. Search runs on
the session's filesystem too, so a container-targeted session greps inside the
container.

`/new` starts an empty session and `/fork` branches the current one; `ctrl+t`
and `alt+t` are the same thing as shortcuts. Every command Tab-completes, and a
`/` on its own lists them all.

`ctrl+t` starts an empty session. `alt+t` **forks** the current one: the new
session inherits the transcript, and its first turn branches the agent's own
conversation rather than replaying it — `claude --resume <id> --fork-session`,
`codex exec fork <id>`, `opencode -s <id> --fork`. The session you forked from
is left exactly as it was, so you can try a second approach without losing the
first. With the session list focused, `n` and `f` do the same to whichever
session is highlighted.

## Host or container

`ctrl+d` points a session at the host or at any running container. Everything
follows the choice at once — the explorer, the preview, `ctrl+p`, `ctrl+f`, and
the agent itself, which is run with `docker exec` so it edits the files you are
looking at rather than same-named files on the host.

```
┌ host ─────────────────┐   ctrl+d   ┌ container ────────────────┐
│ tree   os.ReadDir     │  ───────▶  │ tree   docker exec find   │
│ find   fastwalk       │            │ find   docker exec find   │
│ grep   in-process     │            │ grep   docker exec grep   │
│ agent  runs here      │            │ agent  docker exec claude │
└───────────────────────┘            └───────────────────────────┘
```

Sessions carry their target independently, so one can work on the host while
another works inside a container. A container that has gone away falls back to
the host and says so rather than leaving the session pointed at nothing.

Two honest limits: approval interception is host-only, because the broker talks
over a unix socket a container cannot reach; and the agent CLI has to be
installed **in** the container for a container-targeted session to use it.

## Keys

| Key | |
|---|---|
| `enter` / `alt+enter` | send / newline |
| `ctrl+c` | stop the agent, or quit when idle |
| `ctrl+p` | fuzzy-find a file |
| `ctrl+f` / `ctrl+g` | search file contents, or find in the open file |
| `/`, `n`, `N` | find in file, next hit, previous hit |
| `e` | edit the open file — `ctrl+s` save, `ctrl+z` / `ctrl+y` undo / redo |
| `ctrl+t` / `ctrl+w` | new / close session |
| `alt+t` | fork this session — same history, separate branch |
| `ctrl+r` | choose the engine for this session |
| `r` / `R` | run this session in the selected directory / back to the root |
| `alt+1…9`, `alt+↑↓` | switch session |
| `tab` | cycle panes |
| `ctrl+b` / `ctrl+e` | toggle sidebar / preview |
| `g` / `G` / `w` | top / bottom / soft wrap in the preview |
| click | focus any pane, including the prompt |
| `ctrl+k` | background commands and their output |
| `shift+tab` | cycle mode: plan → ask → auto |
| `f1` | all shortcuts and commands |

## Modes

`shift+tab` cycles how much the agent may do. The mode belongs to the session,
persists with it, and is always in the status bar — a mode that acts without
asking should never be a surprise.

| Mode | | built-in | claude | codex | opencode |
|---|---|---|---|---|---|
| `plan` | reads and proposes; changes nothing | writing tools withheld | `--permission-mode plan` | `--sandbox read-only` | `--agent plan` |
| `ask` | confirms every write and command | approval prompt | `manual` + the broker | — | — |
| `auto` | **default** — acts inside the project, asks outside it | acts, asks outside | `acceptEdits` + the broker | `--sandbox workspace-write` | default |
| `full` | no confirmation, no project boundary | acts | `--dangerously-skip-permissions` | `--dangerously-bypass-…` | default |

Two things worth knowing. **`full` is not in the `shift+tab` cycle** — reaching
"no guards" by tapping a key twice is not a decision anyone makes on purpose, so
it is reachable only by name (`/mode full`, `-mode full`). And **codex and
opencode cannot ask**: their headless modes have no approval channel, so `ask`
falls back to confining them, and the status bar marks it with `⚠` rather than
implying a gate that does not exist.

Auto does not mean unattended. It means *inside the project*: work there goes
ahead, and anything that reaches outside the root stops and asks, saying why.
Without that, a write to `/tmp` in auto mode comes back denied by the CLI with
nobody having been asked — which looks like the agent failing rather than a
boundary doing its job.

Plan mode is enforced by withholding the tools rather than by asking the model
to hold back: a tool that is never offered cannot be called.

## When the agent needs you

**It can ask.** The built-in agent has an `ask_user` tool: when the answer
changes what it would do next and it cannot settle the question from the code,
it puts the options to you in a box. Pick with `1`-`9` or the arrows, or `esc`
to answer nothing and let it decide. Questions queue the way approvals do, and
one from a background session pulls that session to the front, because its
agent is blocked on you.

The agent CLIs have no equivalent: `AskUserQuestion` is not available in their
headless modes, so only the built-in engine can ask.

**It can run things in the background.** `bash` takes `run_in_background`, and
`task_output` / `task_stop` let the agent check on what it started. `ctrl+k` or
`/tasks` lists everything running, with its output; the status bar counts what
is still going.

Claude Code's own background commands land in the same list, decoded from its
event stream. One honest limitation there: `claude -p` kills its background
tasks when the turn ends, so those show as stopped rather than outliving the
conversation. Commands the built-in agent starts are detached from the turn and
keep running.

## Commands

Anything typed into the prompt starting with a single `/` is acted on rather
than sent to the agent. Tab completes them; `/` alone lists them.

| | |
|---|---|
| `/new` `/fork` `/close` | start, branch, or close a session |
| `/cd <dir>` | move this session to another directory |
| `/mode [name]` | plan, ask, auto or full |
| `/engine [name]` | choose the agent: `api`, `claude`, `codex`, `opencode` |
| `/target [name]` | work on the host or inside a container |
| `/files` `/search [text]` | open the file finder or content search |
| `/tasks` | background commands and their output |
| `/help` `/quit` | |

A message that merely mentions a path (`explain /etc/hosts`) still reaches the
agent, and `//` escapes a literal leading slash.

## Tools

The agent gets seven tools, sandboxed to the project root — a path that escapes
it is refused rather than resolved:

| Tool | Approval |
|---|---|
| `read_file`, `list_dir`, `find_files`, `grep` | no |
| `write_file`, `edit_file`, `bash` | yes |

At an approval prompt: `y` allows, `n` denies, `a` allows everything for the
rest of the run. A denial is reported back to the model, which is told not to
retry it.

## Configuration

`$XDG_CONFIG_HOME/agent-tui/config.json`, all fields optional:

```json
{
  "model": "claude-opus-5",
  "effort": "high",
  "engine": "api",
  "mode": "auto",
  "max_tokens": 32000,
  "max_file_kb": 2048,
  "index_limit": 200000,
  "workers": 8,
  "auto_approve": false
}
```

Sessions persist to `$XDG_DATA_HOME/agent-tui/sessions/` and are restored per
project root on the next start.

## How it stays fast

Measured on an M1, `make bench`:

| | |
|---|---|
| Index 400 files | 0.5 ms |
| Fuzzy find over the index | 60 µs |
| Full-content search, 3 MB corpus | 14 ms |
| Syntax highlight, 376 KB Go file | 5.5 ms, 9 allocations |
| Transcript repaint during streaming | 29 µs |

### Reading a filesystem that is not ours

Everything that touches files goes through one `vfs.FS`: the explorer, the
preview, the fuzzy index, content search, and the built-in agent's tools. There
are two implementations, so pointing a session somewhere else repoints all of
them at once.

The container backend is `docker exec`, and every command it runs was chosen to
behave the same under BusyBox and GNU coreutils, because a container is as
likely to be Alpine as Debian. That rules out `stat --printf`, `ls
--time-style` and `grep --exclude-dir` — none of which BusyBox implements — and
leaves `find -exec stat -c`, and `find -print0 | xargs -0 grep`.

One round trip costs about a quarter of a second, which decides the design:
a tree refresh reads **every open directory in a single exec** rather than one
per directory. Measured on five directories, 60 ms batched against 157 ms
one-at-a-time, and the gap widens as more of the tree is open.

### Staying current inside Docker and WSL

The explorer is correct by **polling**, not by filesystem events. Events are an
accelerator layered on top.

That is the right way round because inotify does not cross a Docker bind mount
and does not exist on WSL's `/mnt` drives — a tree that trusted events would
simply stop updating there, with nothing to show for it. Instead:

- only **open** directories are re-read, so the cost tracks what is on screen,
  not the size of the repository;
- each directory is fingerprinted by name, size and mtime, so an unchanged tree
  costs one `readdir` and no repaint;
- `fsnotify` watches the open directories and, where it works, collapses the
  latency to nothing;
- the environment is detected at startup, and the poll tightens from 900 ms to
  400 ms when events are known not to arrive. The pane title says `docker` or
  `wsl` so the behaviour is explicable rather than mysterious;
- a tree reading into a container is never watched at all — there are no events
  to receive — and polls at 1.2 s, paced for the round trip.

### The terminal's own cursor

The prompt draws no cursor of its own. It turns the bubbles virtual cursor off
and reports a real one through `View.Cursor`, positioned over the caret.

That is not cosmetic. A composing input method — Vietnamese Telex, Pinyin, Kana
— anchors its composition to the cursor the terminal reports. With the cursor
hidden there is no insertion point, composition never starts, and the
keystrokes arrive raw: typing `khoong` leaves `khoong` instead of `không`.
The same applies to the file finder and the content search, which take typed
text rather than single keys.

### Four decisions in the rest of it

- **The file list is built once.** A background `fastwalk` pass respects
  `.gitignore` and skips the directories that dominate a repo's file count.
  Nothing interactive touches the disk to enumerate files.
- **The highlighter is hand-written, not regex-driven.** Six token classes, one
  linear pass, every line written into a single buffer that the returned slices
  share. That is both smaller in the binary and roughly an order of magnitude
  faster than a general-purpose lexer.
- **Search walks each file once.** Line numbers are carried along incrementally
  instead of recomputed per hit, case-insensitive matching folds into a
  length-preserving scratch buffer so byte offsets stay exact, and the work is
  spread over one goroutine per CPU.
- **The transcript caches its committed half separately from the streaming
  tail.** A text delta arrives every few milliseconds; re-wrapping the whole
  session on each one would dominate the update loop.

## Layout

```
cmd/agent-tui      entry point, flags, and --permission-broker mode
internal/agent     event vocabulary, Engine interface, built-in Claude loop
internal/engine    engine registry, the three CLI adapters, approval broker
internal/explorer  lazy file tree, hybrid watcher, Docker/WSL detection
internal/ignore    .gitignore matching, shared by both filesystems
internal/vfs       the filesystem abstraction: host and docker exec backends
internal/fsx       background file indexer and .gitignore matching
internal/highlight one-pass syntax highlighter
internal/preview   file loading, binary detection, LRU of highlighted files
internal/search    parallel content search
internal/session   conversation model and on-disk persistence
internal/ui        Bubble Tea model, panes, overlays, the file editor
```

## Tests

`go test ./...` is offline and free. The CLI adapters are checked against
captured real output in `internal/engine/testdata/`, so a format change on their
side fails a test instead of blanking the transcript.

`AGENT_TUI_DOCKER=1 go test ./internal/vfs/` exercises the container backend
against whatever containers happen to be running: listing, byte-exact reads,
stat, walking, grep, and that batching really is one round trip.

`AGENT_TUI_LIVE=1 go test ./internal/engine/` additionally spawns the real
binaries — it costs real money and needs real credentials. It covers the full
approval round trip in both directions: that an approved write happens and a
denied one does not.

Built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea),
[Lip Gloss v2](https://github.com/charmbracelet/lipgloss) and the
[Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go).
