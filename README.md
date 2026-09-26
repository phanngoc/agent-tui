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

**A session can be handed from one agent to another, mid-conversation.** It
used to be impossible and the picker said so — each engine keeps its own
history on its own server, and there is no way to read one out or write one in.
That is still true, and it was never the whole story: the transcript on this
side is complete, and it is the thing both engines are talking about.

So a session remembers, per engine, that engine's own session id *and how far
it had got*. An id alone would not do — resuming Claude Code after Codex ran
ten turns gives you an agent that remembers the first half, has never heard of
the second, and will not say so. With both, a handoff is one operation: bring
the engine about to run from what it last saw up to where the conversation now
is. The built-in engine is handed the transcript itself; the three CLIs, whose
only channel is one string of text, are given the same gap as a briefing.

| | |
|---|---|
| back to an engine that ran here | it resumes its own session |
| …and someone else spoke meanwhile | it resumes, and is caught up on what it missed |
| an engine that has never run here | it gets a summary of the conversation |

The briefing renders tool calls as the one line the transcript already shows
them as, never their JSON — those are eleven times the size of the conversation
around them — and it is capped at 6 KB, because it travels as a single argv
entry and on Windows the whole command line has to fit `CreateProcessW`'s
32,767 characters, which a session aimed at WSL spends twice over.

What cannot travel is said rather than papered over: thinking signatures and
the prompt cache belong to one conversation with one server, so the reasoning
context is dropped and rebuilt from the record. The switch itself is refused
while a turn is running — sequential by construction — and it leaves a mark in
the transcript, the way `cd` does, because it is the same kind of event: after
it, the thing answering is not the thing that answered before.

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
session's agent is actually working in — on the host, inside a container, or
inside a WSL distribution.
`r` on a directory repoints that session: the tree follows, and so does the
`-C` / `--dir` the CLI is given.

**Transcript** — every turn is a block: who spoke, when, and the tool calls the
agent made, each with its status, what came back, and how long it took. Failed
and denied calls show the first lines of their output inline.

One turn is one block however many round trips it took. The agent answers,
calls a tool, answers again — each of those is a message, and heading every one
of them turned a turn that read eight files into eight blocks of a single line
each. The heading goes on the first; the rest continue it, down the same rail:

```
▎ you    15:41
▎ tìm resolvePfid

▎ agent  15:41
▎  … 15 earlier calls  ·  alt+o shows them
▎  ✓ Bash  cd ~/workspace && grep -n "名寄せ" docs/*.md   93 lines  120ms
▎  ✓ Bash  sed -n '1299,1400p' src/…/onboarding.service.ts  102 lines  80ms
▎ Nó ở onboarding.service.ts:1299.
```

An edit shows what it changed — the lines it removed and the lines it put
there, taken from the call's own arguments, tinted like any other diff. "edited
(1 replacement)" is the same report whether the right line was changed or the
wrong one, and the wrong one is the case worth catching.

A turn keeps its last five calls and folds the rest into that one line. The
folded ones already did their work: you watched them go past while the turn was
running, which is what they are for, and what they leave behind afterwards is a
wall between the question and the answer to it. Nothing is thrown away — `alt+o`
shows every call in every turn, and again puts them back.

An answer is markdown, and a terminal has no renderer for it, so the transcript
renders it rather than printing it. Headings become coloured rules, tables
become aligned columns with the header underlined, fenced code becomes an
indented block through the same highlighter the preview pane uses, and
emphasis, links, quotes and lists become the terminal's own equivalents. Half a
document renders too — the streaming tail is re-rendered on every delta, so an
unclosed fence shows the code it has so far instead of swallowing the rest.
What *you* typed is never reinterpreted: your asterisks are yours.

Opening a session — at startup or by switching into it — lands on the newest
exchange: the last thing you said, with the answer to it below. The bottom of a
long reply is the least useful line in it, and the top of a history you read
yesterday is not much better.

Everything that is running says how long it has been running: the turn in the
status bar, the call in the transcript, a `!` command under its own line. A
turn that has been thinking for seven minutes and one that has been thinking
for seven seconds read the same without it, and only one of them is worth
interrupting. The clock is coarse on purpose — `7m 52s`, not `472.318s` — and
a finished call goes back to reporting what it took.

A turn is something you watch rather than wait for. A tool call appears while
the model is still writing it, so a path fills in character by character and a
turn spent composing a large file to write looks like what it is; a running
command's output is forwarded as it prints, so a test run tells you which test
failed at the moment it fails; and a finished call says what came back, not
only that it returned:

```
  ✓ read_file  internal/ui/chat.go   lines 1-40 of 249    8ms
  ✓ edit_file  internal/ui/chat.go   edited (1 replacement(s))
  ⋯ bash       go test ./internal/
    ok   internal/agent   3.0s
    --- FAIL: TestFoo
```

This is the built-in engine only. The CLIs report a tool call when it is
finished and nothing before that, so there is nothing in between for agent-tui
to forward.

**Preview** — a file viewer with line numbers, syntax highlighting, in-file
search with match highlighting, soft-wrap on demand, and an editor: `e` turns
the pane you are reading into a writable buffer.

Long lines are clipped rather than wrapped, because code is columnar and
wrapping destroys the indentation that makes it readable — so `←` and `→`
(or `h` and `l`, or shift with the wheel) move sideways along them, `0` returns
to column one, and the title says which column the pane starts at while it is
not the first. The title fits itself rather than being cut off by its frame:
the directory gives up the room, because the file name is what says which file
this is and the column is what changes. `w` wraps instead, and clears the offset with it: wrapped text
has no horizontal axis to be scrolled along.

In-file search draws its own highlights rather than using the viewport's.
Bubbles' `SetHighlights` walks the ANSI-stripped content to advance a byte
position but indexes the *unstripped* content to count newlines, so on
syntax-coloured text every position drifts by the number of escape bytes before
it — searching for `foo` highlighted `Sta` three lines away. Matches are
therefore tracked per line in display columns and spliced into the styled line
at render time, so there is no cross-line offset arithmetic to get wrong. It reloads itself when
the agent edits the file you are looking at.

**Prompt** — the frame around it carries where the session stands, the way a
shell prompt does: the filesystem when it is not this machine's, then the
directory with home written as `~`. A session moves — `!cd`, `!wsl`, `r` in the
tree — and the answer to "where is this about to run" belongs next to the thing
you are about to run. A path too long for the frame loses its beginning, not
its end: half the directories in a repository end the same way once the front
is gone.

```
╭─ Ubuntu-24.04  ~/workspace/sbi-fpaas-be/src/modules ────────────────────╮
│❯ ask anything…                                                          │
╰─────────────────────────────────────────────────────────────────────────╯
```

**The layout is yours.** The widths used to be two formulas — a sixth of the
terminal for the sidebar, forty-five percent of the rest for the preview — which
is a reasonable guess and wrong for whatever you are actually doing: reading a
long answer wants the transcript wide, comparing two files wants the preview
wide, and neither is a sixth of anything.

| | |
|---|---|
| drag a divider | take hold of the line between two panes and move it |
| | (the two border columns, and no further: the column after one is the first column of a pane's text, and a press there belongs to the word under it) |
| `alt+←` `alt+→` | the arrow pushes the nearest divider that way |
| `ctrl+b` `ctrl+e` | open or close the sidebar and the preview |
| the switches | `▪ sessions  ▪ preview` in the header, click to toggle |

**The column to the right of the transcript holds one pane, not two.** The
preview is in it, and a side chat opened with `/btw` stands in its place. They
were made to sit side by side first, and four panes on a terminal is three
columns of forty with nothing readable in any of them; the two are also the
same kind of thing — something you consult beside the conversation — and you
are never consulting both at once.

So the aside borrows the column and gives it back when it closes, at the width
it had. Nothing has to remember whether the preview was open, because it was
never closed: it is standing aside. `ctrl+e` asks for it back, which asks the
aside to step out, and the conversation in it is kept either way. One divider
sizes the column whichever pane is standing in it.

The switches are in the header rather than on the panes because a closed pane
has no title bar to click: an × that can only close is half a switch, and the
other half would be a key you have to remember. Widths are kept in columns, not
as a fraction of the window — you size the sidebar to the longest path in the
project, and that length has nothing to do with how wide the terminal is — and
they are saved, along with which panes you closed, in
`$XDG_DATA_HOME/agent-tui/layout.json`.

**Watching more than one at a time** — `/split 2` or `/split 4`.

Sessions here have always run at the same time, which is what made a list of
them worth having; but only one was ever on screen, so watching two meant
switching between them and trusting your memory for whichever was not in front
of you. A split puts them side by side: two across, or four as a square.

One of them is the conversation you are talking to. That cell is the transcript
exactly as it always was — it scrolls, it selects, it takes what you type — and
the others are renderings of the newest part of each, which is what you glance
at a second pane for. Clicking one makes it the one you are talking to, and
that is the only gesture the arrangement needs: one prompt, one caret, one
conversation being addressed.

Asking for more cells than fit, or more than you have conversations open, folds
back to what there is room for and says which. `/split` on its own puts the
column back.

**Side chat** — `/btw` opens a pane beside the transcript and asks there.

A long turn is exactly when a question occurs to you, and the two places to put
it were both wrong: into the turn, where it waits and then lands in the context
of everything after it, or into a new session, where the agent knows nothing
about what you are both looking at. So `/btw` forks the conversation and puts
the branch in its own pane — a real session, running at the same time, because
sessions here already do.

The pane shows the aside and not the context it inherited: the agent is given
all of it, which is the point, but you have it already in the pane next to it.
The main transcript never learns the aside happened. One prompt serves both
panes and talks to whichever has the caret; `esc` closes the pane and keeps the
conversation, so reopening finds the same one. It is not in the session list —
it belongs to a conversation rather than standing beside them — and closing
that conversation takes it too.

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

Click a result and it opens, the same as `enter` on the same row — picking one
is a single decision whichever way you make it, and a mouse that only moved the
cursor would leave you reaching for the keyboard to finish a gesture you had
already finished. Clicking a file header folds it; the wheel scrolls the list
without moving the cursor, because reading past a result is not choosing it.
The same is true of `/recall`.

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

## Running a command

A prompt that starts with `!` is run rather than asked:

```
❯ !go test ./internal/search/
❯ !git log --oneline -5
```

It runs where the session works, in the session's directory, and the command
and its output are appended to the transcript as your turn — so the next thing
you ask starts with the failure already in context, instead of you describing
it. Output is capped at 40 KB, keeping the tail, which is where the failure is.

`!!` escapes, for the rare prompt that opens with an exclamation.

Two lines are acted on instead of executed, because executing them could not do
what they say. `cd` in a subshell moves a directory that dies with the
subshell; a bare `wsl` asks for a login shell, and there is no terminal to give
it. Both are requests to move the session, so they move it.

A move is recorded like the command it was. `!cd`, `!wsl`, `!exit`, `/cd` and
the tree's own `r` all change where every path after them resolves, and each
leaves a block in the transcript saying where the session went and where it
came from. Without it the transcript jumps from a command run in one directory
to a command run in another with nothing in between — a gap for whoever scrolls
back, and a worse one for the agent, which is handed the transcript and has no
other way to learn that the ground moved under it. A `cd` to somewhere that is
not there is recorded too, with a failing status: that path was usually the
agent's own suggestion.

## Pointing at a file, and pasting a picture

`@` names a file. The menu opens on the key and narrows as you keep typing, and
what lands in the prompt keeps the marker:

```
❯ tại sao @internal/ui/chat.go lại cache hai nửa riêng?
```

The reference is sent as written. Every engine here can open a file, so the
path is all the agent needs — nothing is inlined behind your back, and a
reference to a 4000-line file costs four words of prompt.

`ctrl+v` attaches the image on the clipboard; `/paste` does the same, for the
terminals that keep `ctrl+v` for themselves. A terminal cannot deliver an image
paste — an image on the clipboard is not part of one, which is why the key
appears to do nothing everywhere else — so the image is read from the operating
system directly: PowerShell on Windows and from inside WSL, `wl-paste` or
`xclip` on Linux, `pngpaste` on a Mac.

What happens to it then depends on what the engine can receive. The built-in
engine sends the image itself, so the model looks at it. A CLI engine is handed
a path and opens the file for itself — which means the file has to exist on its
side of whatever boundary the session is working across:

| Session works | What happens | What the CLI is told |
|---|---|---|
| on the host | nothing; it is already there | where it was saved |
| in WSL | nothing; the drive is mounted in there | the same file as `/mnt/c/…` |
| in a container | the bytes are written across | `/tmp/agent-tui/<session>/…` |

The copy goes through the session's own filesystem — a `docker exec … cat >`,
the same path every other write takes — so nothing here knows about Docker in
particular. If it fails, the paste says so: the built-in engine still sends the
image, but a CLI is about to be told nothing about it, which is worth knowing
before the turn rather than after. Repointing a session after pasting drops the
path for the same reason, and the image itself still goes.

Attached images show in the prompt box before you send and in the transcript
after, and `ctrl+u` drops the prompt and its attachments together.

**The command being waited on is written out in full.** A finished call stays
one line — it is history, and the summary is the part of it worth keeping,
which is why the calls fold at all. The one still running is not history: it
is the thing you are waiting on, and `echo "=== das…` does not say what for,
because the part that was cut is exactly the part that would have said. So it
wraps underneath, with an elbow to say the lines below belong to the line
above, capped at six lines and saying how many it dropped.

## Finding an old conversation

`/recall <text>` searches every conversation you have had, in every project.

The transcript is the only record here that is complete, durable and neutral
between engines — so it is the only thing worth searching, and searching it
must not mean opening each one as a session. `/recall` reads a narrow
projection of every file in the store: the header and the prose, which is eight
per cent of the bytes. Tool results are the other eighty-five, and they are
file dumps and JSON; the answer to "where did I ask about this" is in what was
said.

A hit is an address — a conversation and a message in it — so opening one loads
that conversation, whatever project it belongs to, and lands on the message the
match was found in. Coming from elsewhere retargets the file tree and the
index, which takes a moment, so it says so rather than looking like a glitch.

Everything is read once when the overlay opens and searched from memory as you
type. There is no index on disk: it would be a second source of truth for the
data `docs/mô-hình-vận-hành.md` §11 proposes to restructure, and a cache that
outlived the overlay would have to be invalidated against files another
instance of this program writes — for eleven milliseconds of gain.

## Reading the history

`/git` opens the history: commits down the left, the selected commit's diff
down the right, after Sublime Merge. It reads through the session's filesystem,
so a session pointed at WSL or a container browses that repository rather than
a same-named one on the host.

| | |
|---|---|
| `↑` `↓` | move, which loads the diff |
| `tab` `shift+tab` | move between the two panes |
| `[` `]` | previous / next file in the diff |
| `alt+↑` `alt+↓` | scroll the file summary when it holds more than it shows |
| `f` | show every file at once instead, which gives up the pinning |
| `pgup` `pgdn` | a page of whichever pane has the keyboard |
| `g` `G` | top, bottom |
| wheel, click | scroll the pane under the pointer; click a commit to select it |
| click a file | jump to that file's patch; the pointer underlines what it is over |
| `r` `esc` | reload, close |

The diff opens with what the commit did to the repository — `43 files changed
+896 -152` — then the first eight files with their counts flushed right into a
column you can run your eye down, then the patch. That summary is pinned: the
patch scrolls under it, so clicking a file lands you in its diff with the list
of the others still there to click next. It is a window rather than the whole
list — fifty names would be fifty rows of a pane that is meant to be showing a
patch — and `alt+↑` and `alt+↓` move the window, with a line at each end saying
how many are past it. `f` gives up the pinning and shows them all at once, for
when reading the list is the point. A commit that touches forty
files would otherwise open on forty lines of names, with the thing they
summarise pushed off the bottom; `f` shows the rest. Changed rows are tinted
end to end, gutter included, so a run of additions has a shape you can measure
without reading it.

A commit takes two rows — the subject, then who and when — which is what makes
one distinguishable from the next at a glance, and is also what the column and
its scrolling both count in. A page is a page of commits, the selection keeps a
commit of context past it, and the diff stops with its last line at the bottom
rather than scrolling on into empty space.

## Host, container, or WSL

`ctrl+d` points a session at the host, at any running container, or at a WSL
distribution — `!wsl` and `!exit` are the same move from the prompt. Everything
follows the choice at once: the explorer, the preview, `ctrl+p`, `ctrl+f`, `!`
itself, and the agent, which is run over there so it edits the files you are
looking at rather than same-named files somewhere else.

```
┌ host ─────────────────┐   ctrl+d   ┌ container ────────────────┐
│ tree   os.ReadDir     │  ───────▶  │ tree   docker exec find   │
│ find   fastwalk       │            │ find   docker exec find   │
│ grep   in-process     │            │ grep   docker exec grep   │
│ agent  runs here      │            │ agent  docker exec claude │
└───────────────────────┘            └───────────────────────────┘
        │                            ┌ wsl ──────────────────────┐
        └────────── !wsl ──────────▶ │ tree   wsl.exe find       │
                                     │ agent  wsl.exe claude     │
                                     └───────────────────────────┘
```

This matters most on Windows, where the two filesystems overlap without being
the same: `C:\src\app` and `/mnt/c/src/app` are one directory seen through two
namespaces, `/home/you/app` is only reachable from one of them, and an explorer
showing one while the agent edits the other is worse than no explorer.

`!wsl` lands in the distribution's **login home**, and the explorer shows that
tree immediately. This is deliberately not what bare `wsl.exe` does: given a
Windows working directory it translates it and starts under `/mnt`. That is the
wrong half of the choice here, because the reason to enter a distribution is
the work that only exists inside it — a checkout under `~`, a toolchain that
was never installed on the host. A session that wanted the `/mnt` view of a
Windows project never needed to leave the host to get it. `!exit` comes back
out, to the Windows spelling of where you were when there is one.

The move is **one** `wsl.exe` launch: the distribution names itself through
`WSL_DISTRO_NAME` and the same shell reports where `~` is, so the explorer
lands in about 260 ms warm instead of spending most of a second on three round
trips while still showing the directory you just left. Enumerating
distributions happens only on the path that has already failed, where naming
the ones that do exist is worth the trip. Typing `!wsl` when you are already
inside says so and stays put, rather than discarding wherever you had navigated
to.

Sessions carry their target independently, so one can work on the host while
another works inside a container or a distribution. A container or distribution
that has gone away falls back to the host and says so rather than leaving the
session pointed at nothing.

Two honest limits: approval interception is host-only, because the broker talks
over a unix socket a container cannot reach; and the agent CLI has to be
installed **over there** for a session targeting it to use it.

## Keys

| Key | |
|---|---|
| `enter` / `alt+enter` | send / newline |
| `!<cmd>` | run a command where this session works |
| `esc` | drop a selection — or close what is open, or stop the agent, or go back to the prompt |
| `ctrl+c` | copy a selection — or stop the agent, or quit when idle |
| drag / double-click | select text in a pane; releasing copies it |
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

## Models

`/model` picks the model, per session, so a question worth Opus and a rename
worth Haiku can run at the same time instead of one waiting on the other's
setting. With no argument it opens a picker; with one it takes a family name or
a full id, and a bare `sonnet` means the current Sonnet — which is what someone
who has not been following version numbers means by it.

| | |
|---|---|
| `opus` | 1M context · the default, and the strongest all-round coding model |
| `fable` | 1M context · the most capable, and the most expensive |
| `sonnet` | 1M context · faster and cheaper than Opus |
| `haiku` | 200K context · the cheapest |

Unlike switching engine, changing model keeps the conversation: the model reads
the transcript it is handed, where an engine is a different program with its
own server-side history.

What a request may contain is a property of the model rather than a constant in
the agent, because it is not uniform: the current models take adaptive thinking
and an effort level, while Haiku 4.5 takes a fixed thinking budget and rejects
an effort outright. A picker that did not know that would offer a model that
fails on the first prompt. An id the catalogue has never heard of — one from
your config file, or a model newer than the build — is still offered and still
run, on the assumption that naming it was deliberate.

`/model` reaches the built-in agent and Claude Code, which is passed `--model`.
`codex` and `opencode` drive other providers entirely and choose their own; the
picker says so rather than accepting a setting it knows will be ignored.

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

`esc` does whatever there is to get out of, nearest first: a task's output,
then an overlay, then an in-file search, then a running turn, then the focus.
Stopping comes before moving the focus because one of them is recoverable and
the other is a turn you are paying for; it comes after the overlays because
those are modes you opened yourself and expect `esc` to dismiss. While a turn
runs, the status bar says so.

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
| `/model [name]` | choose the model: `opus`, `sonnet`, `haiku`, `fable` |
| `/engine [name]` | choose the agent: `api`, `claude`, `codex`, `opencode` |
| `/target [name]` | work on the host, in a container, or in WSL |
| `/btw [question]` | ask beside this conversation, in a pane of its own |
| `/split [1\|2\|4]` | watch this many conversations side by side |
| `/recall [text]` | search every conversation, in every project |
| `/git` | browse the history and its diffs |
| `/paste` | attach the image on the clipboard |
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
  "theme": "onedark",
  "max_tokens": 32000,
  "max_file_kb": 2048,
  "index_limit": 200000,
  "workers": 8,
  "auto_approve": false
}
```

Sessions persist to `$XDG_DATA_HOME/agent-tui/sessions/` and are restored per
project root on the next start.

## Copy and paste

**Drag to select, release to copy.** Asking for the mouse takes the terminal's
own drag-to-select away — the drag arrives here instead, and the terminal has
nothing left to select with. Shift is the usual way back, and it is a
convention rather than a guarantee; telling someone to hold a modifier to reach
what their mouse could already do is not an answer. So the selection is ours:

| | |
|---|---|
| drag across a pane | select what it crosses; past the edge scrolls |
| release | it goes on the clipboard, and the status line says so |
| double-click | the word under the pointer — a path, an identifier, a hash |
| `ctrl+c` | copies the selection, and drops it, so a second press stops the turn as it always did |
| `esc` | drops the selection |

It works in the transcript, the side chat and the preview. The clipboard is
written twice over: through whatever tool the machine has — `Set-Clipboard`,
`pbcopy`, `wl-copy`, `xclip` — and through the terminal's own escape sequence,
which is what works over ssh where no local tool can reach the clipboard you
are actually looking at.

A selection is drawn as an overlay on top of a pane that has already been
rendered, rather than as part of its content. The transcript is cached and
rebuilt only when the conversation changes; a selection changes on every frame
of a drag. Keeping them apart means the selection knows nothing about
markdown, diffs or syntax — it colours cells — and the cache is left alone.
Its coordinates are rows the pane drew rather than lines of its content,
because the transcript soft-wraps: one message is many rows, and the row is the
only thing the renderer and the mouse both know.

**In the prompt** the same gestures work, and most of them already did without
being reachable. The textarea binds shift and the arrows to a selection, and
alt+shift to a word of one; what it also binds is `ctrl+g` to select-all, which
this program had taken for the file search before the prompt ever saw it — so
the first thing anyone tries opened a search box. It selects the prompt now,
and the search keeps `ctrl+f`, the binding it is actually reached by.

Clicking puts the caret where you clicked; dragging selects; `ctrl+c` copies,
the same as everywhere else. The drag is the keyboard's own selection driven by
how far the pointer moved, because the textarea offers no way to begin one at a
point — one selection with one set of rules, however it was begun.

**Pasting** lands wherever typing would have — the prompt, a search box, an
open buffer — which it did not before: every text field here is a component,
and a paste that is not routed to one disappears without a word.

`ctrl+v` is for images instead, which no terminal can paste; see above.

## What each conversation is doing

The session list marks every conversation with its state, in three ways at
once — a shape, a colour, and the word itself:

| | | |
|---|---|---|
| `◉` | **blocked** | it is waiting on you to approve something or answer a question |
| `⠋` | **working** | a turn, or a `!` command, is running |
| `●` | **done** | a turn finished while you were looking at a different one |
| `✓` | **idle** | finished, and you have seen it |
| `✗` | **failed** | the last turn ended badly |
| `○` | **new** | nothing said yet |

Three ways because one is not enough. The idea is borrowed from
[herdr](https://github.com/herdrdev/herdr), and so is the reason: its sidebar
first drew these as coloured dots and had to be changed, because blocked,
working and done were all a filled dot and to a colourblind reader those three
collapse into one. So every state gets its own shape, the colour is what it
means *on top of* that, and the word underneath says it a third time — a glyph
nobody has learned yet is a decoration.

**done** is the state that makes a list of conversations worth having: the
answer arrived somewhere you were not. It is runtime only, because what it
records is whether you have looked since it happened, and a new process has
not.

The list scrolls to keep the conversation that matters in it — the one the
cursor is over while you are moving through it, and otherwise the one the
prompt is talking to. It used to cut its rows at the height of the whole body
and hand the rest to a pane half that tall, which clipped them without a word:
with seven conversations the last two were simply not drawn, and the one
running was as likely to be among them as any other.

The tab strip at the top speaks the same vocabulary, but only when there is
something to say: `blocked`, `working` and `done` get their mark up there, and
`idle` and `new` get nothing. The sidebar shows every state because it is a
list you read; the strip is chrome you glance at, and a row of ticks across the
top is not a glance, it is wallpaper. On the active tab the mark keeps its
shape and loses its colour — that tab is already inverted, and you are looking
at it anyway.

The glyph column says what a conversation is doing; the row's background says
where you are standing. Those used to share the column — the spinner overwrote
the active mark, so a session that was both lost the one that said where you
were. There are now two backgrounds: one for the conversation the prompt is
talking to, one for the row the cursor is over, because they are often not the
same row.

## Chrome

The frame is ruled, not boxed.

A rounded box is a card — it says *this is a thing, sitting on a surface* — and
a terminal divided into panes is not a surface with things on it. It is one
surface, ruled into parts. So the corners are square, and panes share their
rules: the pane on the right drops its left border and leans on its
neighbour's, which is one line doing the job two were doing. The body sits
straight on the prompt, whose top rule is the line between them, and there is
no rule under the prompt at all — the status line is the end of the screen, and
a border drawn to separate the last thing from the edge is a row of the
conversation spent on nothing.

Padding went the same way. One column of gutter inside a panel rather than two
on top of the border's own; one column per level in the file tree rather than
two; a rule between the tabs rather than a gutter around each.

It is all pinned: nothing rounded, no two rules touching, and the frame exactly
the size of the terminal, at four sizes.

## Colours

Three palettes, set with `"theme"` in the config: `onedark` (the default),
`herdr` and `monokai`. An unknown name falls back to the default rather than
failing to start — a typo in a config file is not worth a dead terminal.

**onedark** is One Dark, as Atom shipped it and every editor since has copied
it: a blue-grey ground, a cool grey read on top of it, and bold a near-white
step above that. That step is the whole of the scheme's typography — emphasis
is not a colour, it is the plain foreground turned up, the way a terminal has
always done bold — which is why nothing here gives bold a hue of its own.

Four canonical values did not clear this package's floors and were lifted by
the smallest amount that does. They are listed rather than quietly changed,
because a palette not being the thing it is named after is worse than one that
says where it differs:

| | was | is | why |
|---|---|---|---|
| foreground | `#abb2bf` | `#b9c0cc` | 6.57 → 7.65; it is read continuously, so AAA |
| comment | `#7f848e` | `#8f96a3` | 3.73 → 4.71; below AA is where the last palette went wrong |
| red | `#e06c75` | `#e88891` | 4.38 → 5.57; it has to clear the deleted-row tint too |
| white | `#ffffff` | `#e4e8ef` | 14.00 → 11.39; near-white on near-black is a lamp |

**herdr** is Catppuccin Mocha, which is what
[herdr](https://herdr.dev/docs/configuration/) ships as its dark theme, with
the same roles kept apart: `#1e1e2e` for the window, `#181825` for the
sidebar, `#313244` for the active conversation and `#45475a` for the cursor.
Those being distinct is what lets the glyph column say what a conversation is
doing without also having to say where you are.

**monokai**, softened. The background is its warm grey rather than a near-black,
and the foreground comes down to meet it, which puts body text at about 9:1
instead of 14:1. What makes a screen hard to sit in front of is not the ratio
but the range: near-white on near-black measures beautifully and reads like a
lamp.

There are three greys and they have three jobs. **Fg** is emphasis and chrome —
a heading, a bolded phrase, a pane title. **Text** is the prose those sit in,
and it is a step below, because reading an answer should not be reading at the
brightest thing on the screen; what is brightest should be the part the answer
is pointing at. **Dim** is what stands beside the prose, a timestamp or a path.
The order is asserted, not just the ratios: a later edit cannot quietly make
body text the loudest thing again.

Everything that renders text clears WCAG AA (4.5:1) and the three tones read
continuously clear AAA (7:1), asserted in `internal/theme`'s tests along with a
ceiling — a palette can be wrong by being too bright as easily as by being too
dim, and the one this replaced was wrong in the first way after being fixed
from the second. The syntax colours sit at AA on purpose: Monokai's pink
keyword is 3.9:1 on its own background, and dragging it to AAA turns it pastel
and stops it being Monokai.

| | |
|---|---|
| background | `#272822` |
| text | body `#cac7bc` · emphasis `#e4e1d6` · dim `#bab7a8` · faint `#979383` |
| keyword | `#ff6188` |
| string | `#e6db74` |
| type, accent | `#66d9ef` |
| function, added | `#a6e22e` |
| number | `#bd9cff` |
| comment | `#9a9484` |

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
preview, the fuzzy index, content search, `!`, and the built-in agent's tools.
There are three implementations, so pointing a session somewhere else repoints
all of them at once.

Two of the three are one filesystem with a different launcher — a container
over `docker exec`, a distribution over `wsl.exe` — so the scripts live once in
`posixFS` and each backend supplies only how one is run. Every command was
chosen to behave the same under BusyBox and GNU coreutils, because a container
is as likely to be Alpine as Debian. That rules out `stat --printf`, `ls
--time-style` and `grep --exclude-dir` — none of which BusyBox implements — and
leaves `find -exec stat -c`, and `find -print0 | xargs -0 grep`.

One round trip costs about a quarter of a second, which decides the design:
a tree refresh reads **every open directory in a single exec** rather than one
per directory. Measured on five directories, 60 ms batched against 157 ms
one-at-a-time, and the gap widens as more of the tree is open.

`wsl.exe` adds a wrinkle of its own: it writes its diagnostics as UTF-16LE to a
redirected pipe, so an error arrives as ASCII interleaved with NULs. Launches
set `WSL_UTF8=1`, which fixes it at the source on builds that know the
variable, and older ones are decoded on the way in. Only wsl.exe's own output
is decoded — file contents are the bytes Linux wrote, and are passed through
untouched.

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
- a tree reading into a container or a WSL distribution is never watched at all
  — the events, where they exist, are raised in a namespace this process is not
  in — and polls at 1.2 s, paced for the round trip.

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
- **The cache lets go once a second, and only while a clock is running.** A
  running call's line lives in the cached half, so its clock would otherwise be
  drawn once, at zero, and stay there. A session with nothing running never
  redraws at all.
- **The transcript caches its committed half separately from the streaming
  tail.** A text delta arrives every few milliseconds; re-wrapping the whole
  session on each one would dominate the update loop. A running command's
  output goes in that tail for the same reason, which is also why it is drawn
  under the turn rather than under the line of the call it belongs to: putting
  it inside the cached half would rebuild the whole transcript every time a
  build printed a line.
- **A command's output is forwarded in whole lines, at most a few times a
  second.** A progress bar redrawing itself with carriage returns would
  otherwise be thousands of events, and half a line of a stack trace is not
  worth a frame.

## Layout

```
cmd/agent-tui      entry point, flags, and --permission-broker mode
internal/agent     event vocabulary, Engine interface, built-in Claude loop
internal/engine    engine registry, the three CLI adapters, approval broker
internal/explorer  lazy file tree, hybrid watcher, Docker/WSL detection
internal/clipboard the system clipboard, which is the only place an image is
internal/ignore    .gitignore matching, shared by every filesystem
internal/vfs       the filesystem abstraction: host, docker exec, wsl.exe
internal/fsx       background file indexer and .gitignore matching
internal/highlight one-pass syntax highlighter
internal/preview   file loading, binary detection, LRU of highlighted files
internal/search    parallel content search
internal/session   conversation model and on-disk persistence
internal/ui        Bubble Tea model, panes, overlays, the file editor,
                   the transcript's markdown renderer
```

## Tests

`go test ./...` is offline and free. The CLI adapters are checked against
captured real output in `internal/engine/testdata/`, so a format change on their
side fails a test instead of blanking the transcript.

`AGENT_TUI_DOCKER=1 go test ./internal/vfs/` exercises the container backend
against whatever containers happen to be running: listing, byte-exact reads,
stat, walking, grep, and that batching really is one round trip.

The WSL backend needs no flag: on a Windows machine with a distribution
registered it runs the same checks against it, and skips itself everywhere
else. `internal/ui` goes one further and drives the whole move — `!wsl`, then
`!exit` — asserting that the tree, the index and the directory the agent would
run in all end up in the same place, because the failure worth catching is not
that one of them is wrong but that they disagree.

`AGENT_TUI_LIVE=1 go test ./internal/engine/` additionally spawns the real
binaries — it costs real money and needs real credentials. It covers the full
approval round trip in both directions: that an approved write happens and a
denied one does not.

Built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea),
[Lip Gloss v2](https://github.com/charmbracelet/lipgloss) and the
[Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go).
