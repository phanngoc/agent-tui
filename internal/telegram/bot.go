// Package telegram is a Telegram bot for the gateway: talk to a project's
// agent from a phone, approve what it asks, hear what the schedules found.
//
// It follows OpenClaw's Telegram channel where that fits a coding agent:
//   - long polling, with the offset kept so a restart neither loses nor
//     repeats a message, and one poller per token;
//   - strangers are paired with a code approved on the gateway's computer
//     (settings.go);
//   - a chat works on one project and one conversation at a time, chosen
//     with /projects and /sessions or started with /new; plain text is a
//     prompt to it;
//   - while a turn runs, one message shows its progress, edited at most
//     every 1.5 s, with the typing indicator renewed every 4 s; the answer
//     comes as new messages, Markdown made Telegram HTML, cut under 4096;
//   - an approval or a question comes with inline buttons;
//   - a scheduled run with something to report is sent to everyone allowed.
package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Host is what the bot needs of the gateway.
type Host interface {
	// Projects are the folders worked in, most recent first.
	Projects() []string
	// Sessions are a project's conversations, most recent first.
	Sessions(root string) []gateway.Summary
	Session(id string) (gateway.Summary, bool)
	NewSession(root, prompt string) (string, error)
	Route(cmd gateway.Command) error
	Subscribe() (<-chan gateway.Event, func())
	// PublicURL is the tunnel's address, when it is up.
	PublicURL() string
	// Tunneled says remote access is on: the bot waits for the tunnel's
	// address and takes its messages by webhook, rather than polling.
	Tunneled() bool
}

// Status is what the admin shows about the bot.
type Status struct {
	State    string `json:"state"` // off, starting, up, error
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
	// Transport is how updates arrive: webhook or polling.
	Transport string `json:"transport,omitempty"`
	// How the progress messages fare: edits that landed, skipped while
	// Telegram asked to wait, failed; the latest failure; Telegram's
	// flood control — how often, the latest wait, until when.
	Edits     int       `json:"edits"`
	Skipped   int       `json:"skipped"`
	Failed    int       `json:"failed"`
	LastError string    `json:"last_error,omitempty"`
	Floods    int       `json:"floods"`
	LastFlood string    `json:"last_flood,omitempty"`
	FloodTill time.Time `json:"flood_until,omitzero"`
}

// Bot runs the bot while the gateway serves.
type Bot struct {
	Host     Host
	OnChange func()
	// PollCommand, when set, runs the long poll in a process of its own
	// (transport.go); nil polls in this one.
	PollCommand func(offset int64) *exec.Cmd

	mu sync.Mutex
	st Status
	// edits, skipped, failed and lastErr are the progress messages' record.
	edits, skipped, failed int
	lastErr                string
	ctx                    context.Context // the running bot's, for webhook updates
	seen                   []int64         // update ids lately handled, against redelivery
	cancel                 context.CancelFunc
	done                   chan struct{}
	c                      *client
	turns                  map[string]*turnView // by chat:session
	refs                   map[string]*ref      // the pending buttons, by short id
	lists                  map[int64][]string   // the projects a chat was last shown
}

// ref is what an inline button stands for.
type ref struct {
	kind    string // approval, choice
	session string
	id      string
	chat    int64
	msg     int64
	text    string
}

// turnView is a turn shown in a chat: its progress (progress.go) and the
// message that shows it.
type turnView struct {
	chat      int64
	session   string
	p         *progress
	draft     int64
	dirty     bool
	sending   bool
	broken    bool
	failures  int
	lastEdit  time.Time
	lastBlock string
	// every is how often the message may be edited: from firstEvery,
	// slower each time Telegram asks to wait.
	every time.Duration
	stop  chan struct{}
}

// Status is the bot's state now.
func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.st
	if st.State == "" {
		st.State = "off"
	}
	st.Edits, st.Skipped, st.Failed, st.LastError = b.edits, b.skipped, b.failed, b.lastErr
	if b.c != nil {
		n, last, until := b.c.flood()
		st.Floods = n
		if last > 0 {
			st.LastFlood = last.String()
		}
		if until.After(time.Now()) {
			st.FloodTill = until
		}
	}
	return st
}

func (b *Bot) set(st Status) {
	b.mu.Lock()
	b.st = st
	b.mu.Unlock()
	if b.OnChange != nil {
		b.OnChange()
	}
}

// Start runs the bot with the saved settings, replacing a running one.
func (b *Bot) Start() {
	b.Stop()
	s := Load()
	if s.Enabled && tripped() {
		_, _ = Change(func(s *Settings) error { s.Enabled, s.Paused = false, PausedReason; return nil })
		b.set(Status{State: "off", Error: PausedReason})
		return
	}
	if !s.Enabled || s.Token == "" {
		b.set(Status{State: "off", Error: s.Paused})
		return
	}
	setMark()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.mu.Lock()
	b.cancel, b.done, b.ctx = cancel, done, ctx
	b.c = newClient(s.Token)
	b.turns, b.refs, b.lists = map[string]*turnView{}, map[string]*ref{}, map[int64][]string{}
	b.mu.Unlock()
	b.set(Status{State: "starting"})
	go func() {
		// Two minutes up and the bot is not what stops the gateway.
		select {
		case <-ctx.Done():
		case <-time.After(guardSpan):
			clearMark()
		}
	}()
	go func() {
		defer close(done)
		err := b.run(ctx)
		if ctx.Err() != nil {
			b.set(Status{State: "off"})
			return
		}
		b.set(Status{State: "error", Error: err.Error()})
	}()
}

// Stop ends the bot.
func (b *Bot) Stop() {
	b.mu.Lock()
	cancel, done := b.cancel, b.done
	b.cancel, b.done = nil, nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
		clearMark()
	}
}

var commands = []map[string]string{
	{"command": "new", "description": "Start a conversation in this project (/new <prompt>)"},
	{"command": "sessions", "description": "Pick a conversation of this project"},
	{"command": "projects", "description": "Pick the project to work on"},
	{"command": "status", "description": "What this chat is working on"},
	{"command": "stop", "description": "Stop the turn that is running"},
	{"command": "now", "description": "Stop the running turn and send this at once (/now <text>)"},
	{"command": "model", "description": "Change the model (/model opus)"},
	{"command": "whoami", "description": "Your Telegram user id"},
	{"command": "help", "description": "How to use this bot"},
}

func (b *Bot) run(ctx context.Context) error {
	me, err := b.c.getMe(ctx)
	if err != nil {
		if isAPI(err, http.StatusUnauthorized) || isAPI(err, http.StatusNotFound) {
			return errors.New("Telegram does not know that token: copy it again from @BotFather")
		}
		return err
	}
	_ = b.c.call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil, false)

	events, unsub := b.Host.Subscribe()
	defer unsub()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-events:
				if !ok {
					return
				}
				b.onEvent(ctx, e)
			}
		}
	}()

	return b.transport(ctx, me.Username)
}

// --- incoming ---------------------------------------------------------------

func (b *Bot) onUpdate(ctx context.Context, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram: update %d: %v", u.UpdateID, r)
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		b.onCallback(ctx, u.CallbackQuery)
	case u.Message != nil && u.Message.From != nil:
		b.onMessage(ctx, u.Message)
	}
}

func (b *Bot) reply(ctx context.Context, chat int64, html string, rows ...[]Button) {
	if _, err := b.c.send(ctx, chat, html, rows); err != nil {
		log.Printf("telegram: send: %v", err)
	}
}

func (b *Bot) onMessage(ctx context.Context, m *Message) {
	if m.Chat.Type != "private" {
		return
	}
	s := Load()
	from := *m.From
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if !s.IsAllowed(from.ID) {
		if s.policy() != PolicyPairing {
			return
		}
		code, fresh, err := RequestPairing(from, m.Chat.ID, time.Now())
		if err != nil || code == "" || !fresh {
			return
		}
		b.reply(ctx, m.Chat.ID, fmt.Sprintf("This bot runs a coding agent and does not know you yet.\n\nPairing code: <code>%s</code>\nYour Telegram id: <code>%d</code>\n\nOn the gateway's computer, approve it on the admin's <b>Remote &amp; Telegram</b> page, or run\n<code>agent-tui telegram approve %s</code>\nThe code lasts an hour.", code, from.ID, code))
		if b.OnChange != nil {
			b.OnChange()
		}
		return
	}
	if text == "" {
		b.reply(ctx, m.Chat.ID, "Send text: files and pictures are not passed on yet.")
		return
	}
	if strings.HasPrefix(text, "/") {
		b.command(ctx, m.Chat.ID, from, text)
		return
	}
	b.prompt(ctx, m.Chat.ID, text)
}

func (b *Bot) chatState(chat int64) ChatState {
	s := Load()
	if c := s.Chats[strconv.FormatInt(chat, 10)]; c != nil {
		return *c
	}
	return ChatState{}
}

func (b *Bot) setChat(chat int64, f func(c *ChatState)) {
	_, _ = Change(func(s *Settings) error { f(s.chat(chat)); return nil })
}

func (b *Bot) command(ctx context.Context, chat int64, from User, text string) {
	name, arg, _ := strings.Cut(text, " ")
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	arg = strings.TrimSpace(arg)
	cs := b.chatState(chat)
	switch name {
	case "start", "help":
		b.reply(ctx, chat, "<b>agent-tui</b> — your coding agent, from Telegram.\n\n"+
			"/projects — pick the project\n/sessions — pick a conversation\n/new &lt;prompt&gt; — start a conversation\n"+
			"/status — what this chat is on\n/stop — stop the running turn\n/model &lt;name&gt; — change the model\n\n"+
			"Anything else you send is a prompt to the current conversation.")
	case "whoami":
		b.reply(ctx, chat, fmt.Sprintf("You are %s, Telegram id <code>%d</code>.", esc(from.Name()), from.ID))
	case "projects":
		roots := b.Host.Projects()
		if len(roots) == 0 {
			b.reply(ctx, chat, "No projects yet: open one in agent-tui first.")
			return
		}
		roots = roots[:min(len(roots), 12)]
		b.mu.Lock()
		b.lists[chat] = roots
		b.mu.Unlock()
		var rows [][]Button
		for i, r := range roots {
			label := filepath.Base(r)
			if r == cs.Root {
				label = "● " + label
			}
			rows = append(rows, []Button{{Text: label, Data: "p:" + strconv.Itoa(i)}})
		}
		b.reply(ctx, chat, "Pick the project to work on:", rows...)
	case "sessions":
		if cs.Root == "" {
			b.reply(ctx, chat, "Pick a project first: /projects")
			return
		}
		var rows [][]Button
		for _, s := range b.Host.Sessions(cs.Root) {
			if len(rows) == 10 {
				break
			}
			label := s.Title
			if len([]rune(label)) > 48 {
				label = string([]rune(label)[:47]) + "…"
			}
			if s.ID == cs.Session {
				label = "● " + label
			} else if s.Busy {
				label = "⏳ " + label
			}
			rows = append(rows, []Button{{Text: label, Data: "s:" + s.ID}})
		}
		if len(rows) == 0 {
			b.reply(ctx, chat, "No conversations in "+esc(filepath.Base(cs.Root))+" yet: /new &lt;prompt&gt; starts one.")
			return
		}
		b.reply(ctx, chat, "Conversations in <b>"+esc(filepath.Base(cs.Root))+"</b>:", rows...)
	case "new":
		if cs.Root == "" {
			b.reply(ctx, chat, "Pick a project first: /projects")
			return
		}
		b.setChat(chat, func(c *ChatState) { c.Session = "" })
		if arg == "" {
			b.reply(ctx, chat, "A new conversation in <b>"+esc(filepath.Base(cs.Root))+"</b>: send its first prompt.")
			return
		}
		b.prompt(ctx, chat, arg)
	case "status":
		if cs.Root == "" {
			b.reply(ctx, chat, "No project picked: /projects")
			return
		}
		msg := "Project: <b>" + esc(filepath.Base(cs.Root)) + "</b>\n<code>" + esc(cs.Root) + "</code>"
		if sum, ok := b.Host.Session(cs.Session); ok && cs.Session != "" {
			state := "idle"
			if sum.Busy {
				state = "running" + map[bool]string{true: ": " + sum.Status, false: ""}[sum.Status != ""]
			}
			msg += fmt.Sprintf("\nConversation: %s\nEngine %s · model %s · %s", esc(sum.Title), esc(sum.Engine), esc(sum.Model), esc(state))
		} else {
			msg += "\nNo conversation yet: your next message starts one."
		}
		var rows [][]Button
		if u := b.Host.PublicURL(); u != "" && cs.Session != "" {
			rows = append(rows, []Button{{Text: "Open in the browser", URL: u + "/sessions?id=" + cs.Session}})
		}
		b.reply(ctx, chat, msg, rows...)
	case "now":
		if arg == "" {
			b.reply(ctx, chat, "/now &lt;text&gt; stops the running turn and sends the text at once.")
			return
		}
		b.send(ctx, chat, arg, true)
	case "stop":
		if cs.Session == "" {
			b.reply(ctx, chat, "Nothing is running here.")
			return
		}
		if err := b.Host.Route(gateway.Command{Type: gateway.CmdCancel, Session: cs.Session, From: "telegram"}); err != nil {
			b.reply(ctx, chat, "Could not stop it: "+esc(err.Error()))
			return
		}
		b.reply(ctx, chat, "Stopping…")
	case "model":
		if cs.Session == "" {
			b.reply(ctx, chat, "Start or pick a conversation first.")
			return
		}
		if arg == "" {
			sum, _ := b.Host.Session(cs.Session)
			b.reply(ctx, chat, "Model: <code>"+esc(sum.Model)+"</code>\nChange it with /model opus, /model sonnet, /model haiku or a model id.")
			return
		}
		model := arg
		if m, ok := agent.ResolveModel(arg); ok {
			model = m.ID
		}
		if err := b.Host.Route(gateway.Command{Type: gateway.CmdSettings, Session: cs.Session, Model: model, From: "telegram"}); err != nil {
			b.reply(ctx, chat, "Could not change it: "+esc(err.Error()))
			return
		}
		b.reply(ctx, chat, "From the next turn on: <code>"+esc(model)+"</code>")
	default:
		b.reply(ctx, chat, "I do not know /"+esc(name)+". /help lists what I do.")
	}
}

// prompt sends text to the chat's conversation, starting one if there is
// none.
func (b *Bot) prompt(ctx context.Context, chat int64, text string) { b.send(ctx, chat, text, false) }

// send is prompt, or with now set, a prompt that stops a running turn so it
// goes at once.
func (b *Bot) send(ctx context.Context, chat int64, text string, now bool) {
	cs := b.chatState(chat)
	if cs.Root == "" {
		roots := b.Host.Projects()
		if len(roots) == 1 {
			cs.Root = roots[0]
			b.setChat(chat, func(c *ChatState) { c.Root = roots[0] })
		} else {
			b.reply(ctx, chat, "Pick the project first: /projects")
			return
		}
	}
	if cs.Session != "" {
		if sum, ok := b.Host.Session(cs.Session); ok && sum.Busy {
			// Sent while the agent works: queued, and handed to it after its
			// running step when its engine can take it (Claude Code's way).
			if err := b.Host.Route(gateway.Command{Type: gateway.CmdPrompt, Session: cs.Session, Text: text, Now: now, From: "telegram"}); err != nil {
				b.reply(ctx, chat, "Could not queue it: "+esc(err.Error()))
				return
			}
			if now {
				b.reply(ctx, chat, "⏭ Stopping the turn to send it now.")
			} else {
				b.reply(ctx, chat, "📥 Queued — the agent gets it after its current step, or when this turn ends. /now &lt;text&gt; stops the turn and sends right away.")
			}
			return
		}
		b.watch(chat, cs.Session)
		if err := b.Host.Route(gateway.Command{Type: gateway.CmdPrompt, Session: cs.Session, Text: text, From: "telegram"}); err == nil {
			return
		} else if _, ok := b.Host.Session(cs.Session); ok {
			b.reply(ctx, chat, "Could not send it: "+esc(err.Error()))
			return
		}
		// The conversation is gone: start another.
	}
	id, err := b.Host.NewSession(cs.Root, text)
	if err != nil {
		b.reply(ctx, chat, "Could not start a conversation: "+esc(err.Error()))
		return
	}
	b.setChat(chat, func(c *ChatState) { c.Session = id })
	b.watch(chat, id)
}

func (b *Bot) onCallback(ctx context.Context, q *CallbackQuery) {
	s := Load()
	if !s.IsAllowed(q.From.ID) {
		b.c.answer(ctx, q.ID, "You are not allowed to use this bot.")
		return
	}
	chat := q.From.ID
	if q.Message != nil {
		chat = q.Message.Chat.ID
	}
	kind, rest, _ := strings.Cut(q.Data, ":")
	switch kind {
	case "p":
		i, _ := strconv.Atoi(rest)
		b.mu.Lock()
		list := b.lists[chat]
		b.mu.Unlock()
		if i < 0 || i >= len(list) {
			b.c.answer(ctx, q.ID, "That list is old: /projects again.")
			return
		}
		root := list[i]
		b.setChat(chat, func(c *ChatState) {
			if c.Root != root {
				c.Session = ""
			}
			c.Root = root
		})
		b.c.answer(ctx, q.ID, filepath.Base(root))
		b.reply(ctx, chat, "Working on <b>"+esc(filepath.Base(root))+"</b>. /sessions picks a conversation; or just send a prompt to start one.")
	case "s":
		sum, ok := b.Host.Session(rest)
		if !ok {
			b.c.answer(ctx, q.ID, "That conversation is gone.")
			return
		}
		b.setChat(chat, func(c *ChatState) { c.Root, c.Session = sum.Root, sum.ID })
		b.c.answer(ctx, q.ID, "")
		b.reply(ctx, chat, "Now on: <b>"+esc(sum.Title)+"</b>\nWhat you send next goes to it.")
		if sum.Busy {
			b.watch(chat, sum.ID)
		}
	case "a", "c":
		id, choice, _ := strings.Cut(rest, ":")
		b.mu.Lock()
		r := b.refs[id]
		b.mu.Unlock()
		if r == nil {
			b.c.answer(ctx, q.ID, "Already answered.")
			return
		}
		cmd := gateway.Command{Session: r.session, ID: r.id, From: "telegram"}
		label := ""
		if kind == "a" {
			cmd.Type = gateway.CmdApprove
			cmd.Verdict = map[string]string{"o": "allow", "a": "allow_all", "d": "deny"}[choice]
			label = map[string]string{"o": "✅ Allowed", "a": "✅ Allowed for this turn", "d": "⛔ Denied"}[choice]
		} else {
			cmd.Type = gateway.CmdChoose
			cmd.Index, _ = strconv.Atoi(choice)
			label = "✅ Answered"
		}
		if err := b.Host.Route(cmd); err != nil {
			b.c.answer(ctx, q.ID, err.Error())
			return
		}
		b.c.answer(ctx, q.ID, "")
		b.resolve(ctx, id, label)
	default:
		b.c.answer(ctx, q.ID, "")
	}
}

// --- outgoing ---------------------------------------------------------------

// watch shows chat the turns of session.
func (b *Bot) watch(chat int64, session string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := fmt.Sprint(chat, ":", session)
	if b.turns[k] == nil {
		b.turns[k] = &turnView{chat: chat, session: session}
	}
}

func (b *Bot) views(session string) []*turnView {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*turnView
	for _, v := range b.turns {
		if v.session == session {
			out = append(out, v)
		}
	}
	return out
}

func (b *Bot) onEvent(ctx context.Context, e gateway.Event) {
	if e.Type == "schedule.run" {
		b.onSchedule(ctx, e)
		return
	}
	if e.Session == "" {
		return
	}
	// A chat's current conversation is watched, whoever started the turn.
	if e.Type == gateway.EvTurnStarted {
		for _, chat := range Load().ChatsOn(e.Session) {
			b.watch(chat, e.Session)
		}
	}
	for _, v := range b.views(e.Session) {
		b.show(ctx, v, e)
	}
}

func (b *Bot) show(ctx context.Context, v *turnView, e gateway.Event) {
	switch e.Type {
	case gateway.EvTurnStarted:
		b.mu.Lock()
		if v.stop != nil {
			close(v.stop)
		}
		v.p = newProgress(Load().Streaming, time.Now())
		v.draft, v.failures, v.broken, v.dirty, v.lastBlock = 0, 0, false, false, ""
		v.lastEdit, v.every = time.Time{}, firstEvery
		v.stop = make(chan struct{})
		stop := v.stop
		b.mu.Unlock()
		go func() {
			// Once a second: the progress message when it has news (and its
			// clock every few seconds); the typing indicator every four.
			t := time.NewTicker(editEvery)
			defer t.Stop()
			for i := 0; ; i++ {
				b.mu.Lock()
				showing := v.draft != 0
				if v.p != nil && showing && time.Since(v.lastEdit) >= 5*time.Second {
					v.dirty = true // the headline's clock
				}
				b.mu.Unlock()
				// Typing until the progress message shows; after that the
				// message is the sign of life, and every call counts against
				// Telegram's limit of about one a second per chat.
				if !showing && i%4 == 0 {
					b.c.typing(ctx, v.chat)
				}
				b.flush(ctx, v)
				select {
				case <-ctx.Done():
					return
				case <-stop:
					return
				case <-t.C:
				}
			}
		}()
	case gateway.EvApprovalRequest:
		var d gateway.ApprovalData
		_ = json.Unmarshal(e.Data, &d)
		id := newRef()
		text := "🔐 <b>The agent asks to run</b>\n<pre>" + esc(clip(toolLine(d.Call), 900)) + "</pre>"
		if d.Reason != "" {
			text += "\n<i>" + esc(d.Reason) + "</i>"
		}
		rows := [][]Button{{{Text: "Allow", Data: "a:" + id + ":o"}, {Text: "Allow all this turn", Data: "a:" + id + ":a"}}, {{Text: "Deny", Data: "a:" + id + ":d"}}}
		msg, err := b.c.send(ctx, v.chat, text, rows)
		if err == nil {
			b.mu.Lock()
			b.refs[id] = &ref{kind: "approval", session: e.Session, id: d.ID, chat: v.chat, msg: msg, text: text}
			b.mu.Unlock()
		}
	case gateway.EvChoiceRequest:
		var d gateway.ChoiceData
		_ = json.Unmarshal(e.Data, &d)
		id := newRef()
		text := "❓ " + esc(d.Question)
		var rows [][]Button
		for i, o := range d.Options {
			rows = append(rows, []Button{{Text: clip(o.Label, 60), Data: fmt.Sprintf("c:%s:%d", id, i)}})
		}
		msg, err := b.c.send(ctx, v.chat, text, rows)
		if err == nil {
			b.mu.Lock()
			b.refs[id] = &ref{kind: "choice", session: e.Session, id: d.ID, chat: v.chat, msg: msg, text: text}
			b.mu.Unlock()
		}
	case gateway.EvApprovalDone, gateway.EvChoiceDone:
		var d gateway.ResolvedData
		_ = json.Unmarshal(e.Data, &d)
		b.mu.Lock()
		var found string
		for k, r := range b.refs {
			if r.session == e.Session && r.id == d.ID {
				found = k
			}
		}
		b.mu.Unlock()
		if found != "" && d.By != "telegram" {
			b.resolve(ctx, found, "✔️ Answered "+map[bool]string{true: "in " + d.By, false: "elsewhere"}[d.By != ""])
		}
	case gateway.EvTurnDone:
		var d gateway.TurnData
		_ = json.Unmarshal(e.Data, &d)
		b.finish(ctx, v, d.Error)
	default:
		b.mu.Lock()
		if v.p == nil {
			b.mu.Unlock()
			return
		}
		changed, block := v.p.apply(e, time.Now())
		if changed {
			v.dirty = true
		}
		b.mu.Unlock()
		if block != "" {
			// Block mode: said on the way, sent as it is said.
			for _, piece := range Chunks(block) {
				b.reply(ctx, v.chat, HTML(piece))
			}
			b.mu.Lock()
			v.lastBlock = block
			b.mu.Unlock()
		}
		if changed {
			b.flush(ctx, v)
		}
	}
}

// flush shows the progress message: sent once it is worth it, then edited
// at most once a second while there is news. Three failed edits in a row
// and it is left as it is, as OpenClaw does.
func (b *Bot) flush(ctx context.Context, v *turnView) {
	b.mu.Lock()
	now := time.Now()
	if v.stop == nil || v.p == nil || v.broken || v.sending || (!v.dirty && v.draft != 0) ||
		now.Sub(v.lastEdit) < v.every || (v.draft == 0 && !v.p.worth(now)) {
		b.mu.Unlock()
		return
	}
	text := v.p.render(now)
	draft := v.draft
	v.dirty, v.lastEdit, v.sending = false, now, true
	b.mu.Unlock()
	if draft == 0 {
		id, err := b.c.send(ctx, v.chat, text, nil)
		b.mu.Lock()
		v.sending = false
		if err != nil {
			v.failures++
			v.broken = v.failures >= 3
			b.failed++
			b.lastErr = err.Error()
			b.mu.Unlock()
			log.Printf("telegram: progress message: %v", err)
			return
		}
		if v.stop == nil {
			// The turn ended while this was on its way.
			b.mu.Unlock()
			b.c.delete(ctx, v.chat, id)
			return
		}
		v.draft = id
		b.mu.Unlock()
		return
	}
	err := b.c.edit(ctx, v.chat, draft, text, nil, true)
	b.mu.Lock()
	v.sending = false
	switch {
	case err == nil:
		v.failures = 0
		b.edits++
	case errors.Is(err, errSkipped):
		// Telegram asked to wait: the next tick tries again, and this turn
		// edits more slowly from now on.
		v.dirty = true
		v.every = min(v.every*2, slowestEvery)
		b.skipped++
	default:
		v.failures++
		v.broken = v.failures >= 3
		b.failed++
		b.lastErr = err.Error()
		log.Printf("telegram: editing the progress message: %v", err)
	}
	b.mu.Unlock()
}

// finish ends a turn in the chat: the progress message folds into what was
// done (or, streaming the answer, becomes it), and the answer follows.
func (b *Bot) finish(ctx context.Context, v *turnView, errText string) {
	b.mu.Lock()
	if v.stop != nil {
		close(v.stop)
		v.stop = nil
	}
	p, draft, broken, lastBlock := v.p, v.draft, v.broken, v.lastBlock
	v.p, v.draft = nil, 0
	b.mu.Unlock()
	if p == nil {
		return
	}
	now := time.Now()
	answer := strings.TrimSpace(p.answer)
	if answer == "" {
		answer = strings.TrimSpace(p.partial)
	}
	chunks := Chunks(answer)
	stopped := strings.Contains(errText, "context canceled")
	if p.mode == StreamPartial && draft != 0 && !broken && len(chunks) > 0 {
		// The answer streamed into the message: it ends there, with a line
		// of what it took; a long one carries on in more messages.
		head := strings.SplitN(p.summary(now, errText), "\n", 2)[0]
		first := "<i>" + plain(head) + "</i>\n\n" + HTML(chunks[0])
		if err := b.c.edit(ctx, v.chat, draft, first, nil, false); err != nil {
			b.c.delete(ctx, v.chat, draft)
			b.reply(ctx, v.chat, first)
		}
		for _, piece := range chunks[1:] {
			b.reply(ctx, v.chat, HTML(piece))
		}
	} else {
		if draft != 0 {
			if broken {
				b.c.delete(ctx, v.chat, draft)
			} else if err := b.c.edit(ctx, v.chat, draft, p.summary(now, errText), nil, false); err != nil {
				b.c.delete(ctx, v.chat, draft)
			}
		}
		if !(p.mode == StreamBlock && answer == strings.TrimSpace(lastBlock)) {
			for _, piece := range chunks {
				b.reply(ctx, v.chat, HTML(piece))
			}
		}
	}
	switch {
	case errText != "" && !stopped:
		b.reply(ctx, v.chat, "⚠️ "+esc(clip(errText, 1500)))
	case answer == "" && draft == 0 && !stopped:
		b.reply(ctx, v.chat, "✅ Done.")
	}
}

// resolve takes the buttons off a message once its question is answered.
func (b *Bot) resolve(ctx context.Context, id, label string) {
	b.mu.Lock()
	r := b.refs[id]
	delete(b.refs, id)
	b.mu.Unlock()
	if r != nil {
		_ = b.c.edit(ctx, r.chat, r.msg, r.text+"\n\n"+label, nil, false)
	}
}

// onSchedule sends a scheduled run's report to everyone allowed.
func (b *Bot) onSchedule(ctx context.Context, e gateway.Event) {
	s := Load()
	if s.QuietSchedules || len(s.Allowed) == 0 {
		return
	}
	var d struct {
		Name string `json:"name"`
		Run  struct {
			Status  string `json:"status"`
			Text    string `json:"text"`
			Error   string `json:"error"`
			Session string `json:"session"`
		} `json:"run"`
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return
	}
	var body string
	switch d.Run.Status {
	case "ok":
		if strings.TrimSpace(d.Run.Text) == "" {
			return
		}
		body = "⏰ <b>" + esc(d.Name) + "</b>\n\n" + HTML(clip(d.Run.Text, 3000))
	case "error":
		body = "⏰ <b>" + esc(d.Name) + "</b> failed\n<i>" + esc(clip(d.Run.Error, 1500)) + "</i>"
	default:
		return
	}
	var rows [][]Button
	if d.Run.Session != "" {
		row := []Button{{Text: "Reply in Telegram", Data: "s:" + d.Run.Session}}
		if u := b.Host.PublicURL(); u != "" {
			row = append(row, Button{Text: "Open", URL: u + "/sessions?id=" + d.Run.Session})
		}
		rows = append(rows, row)
	}
	ids := make([]int64, 0, len(s.Allowed))
	for _, p := range s.Allowed {
		ids = append(ids, p.ID) // a private chat's id is the person's
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		b.reply(ctx, id, body, rows...)
	}
}

// SendTo sends one message to one person, when the bot is running.
func (b *Bot) SendTo(ctx context.Context, id int64, html string) {
	b.mu.Lock()
	c, up := b.c, b.st.State == "up"
	b.mu.Unlock()
	if up && c != nil {
		_, _ = c.send(ctx, id, html, nil)
	}
}

// Notify sends a line to everyone allowed: the admin's test message.
func (b *Bot) Notify(ctx context.Context, html string) error {
	b.mu.Lock()
	c := b.c
	up := b.st.State == "up"
	b.mu.Unlock()
	if !up || c == nil {
		return errors.New("the bot is not running")
	}
	s := Load()
	if len(s.Allowed) == 0 {
		return errors.New("nobody is allowed yet: message the bot, then approve the code")
	}
	for _, p := range s.Allowed {
		if _, err := c.send(ctx, p.ID, html, nil); err != nil {
			return err
		}
	}
	return nil
}

func toolLine(c session.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)
	for _, k := range []string{"command", "file_path", "path", "pattern", "query", "url", "description", "prompt"} {
		if v, ok := in[k].(string); ok && v != "" {
			return c.Name + ": " + clip(strings.ReplaceAll(v, "\n", " "), 160)
		}
	}
	return c.Name
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func newRef() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
