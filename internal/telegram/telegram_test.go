package telegram

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

func TestHTMLConvertsMarkdownAndEscapes(t *testing.T) {
	got := HTML("# Title\n**bold** and *it* and `a<b>` and [x](https://e.com)\n- one\n```go\nif a < b {}\n```\n| a | b |")
	for _, want := range []string{"<b>Title</b>", "<b>bold</b>", "<i>it</i>", "<code>a&lt;b&gt;</code>", `<a href="https://e.com">x</a>`, "• one",
		`<pre><code class="language-go">if a &lt; b {}</code></pre>`, "<pre>| a | b |</pre>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(HTML("snake_case_name and 2*3*4"), "<i>") {
		t.Error("underscores in names or arithmetic became italics")
	}
}

func TestChunksStayUnderTheLimitAndKeepCodeBlocksWhole(t *testing.T) {
	long := strings.Repeat("word ", 1500) + "\n\n```\n" + strings.Repeat("line of code\n", 600) + "```"
	chunks := Chunks(long)
	if len(chunks) < 3 {
		t.Fatalf("%d chunks", len(chunks))
	}
	for i, c := range chunks {
		if len(HTML(c)) > 4096 {
			t.Fatalf("chunk %d renders to %d chars", i, len(HTML(c)))
		}
		if strings.Count(c, "```")%2 != 0 {
			t.Fatalf("chunk %d leaves a code block open:\n%.200s", i, c)
		}
	}
}

func TestPairingCodesAndApproval(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Now()
	u := User{ID: 42, FirstName: "Ann"}
	code, fresh, err := RequestPairing(u, 42, now)
	if err != nil || !fresh || len(code) != 8 || strings.ContainsAny(code, "0O1I") {
		t.Fatalf("code %q fresh %v err %v", code, fresh, err)
	}
	if again, fresh, _ := RequestPairing(u, 42, now); again != code || fresh {
		t.Fatal("asking again gave a new code")
	}
	for i := int64(1); i <= 3; i++ {
		_, _, _ = RequestPairing(User{ID: 100 + i}, 100+i, now)
	}
	if c, _, _ := RequestPairing(User{ID: 999}, 999, now); c != "" {
		t.Fatal("more than three requests were kept waiting")
	}
	if _, err := Approve(code, now.Add(2*time.Hour)); err == nil {
		t.Fatal("an expired code was approved")
	}
	code, _, _ = RequestPairing(u, 42, now)
	if _, err := Approve(strings.ToLower(code), now); err != nil {
		t.Fatal(err)
	}
	if !Load().IsAllowed(42) || Load().IsAllowed(999) {
		t.Fatal("allowed list")
	}
	_ = Revoke(42)
	if Load().IsAllowed(42) {
		t.Fatal("revoke")
	}
}

// fakeTelegram is the Bot API: it hands out queued updates and records what
// the bot sends.
type fakeTelegram struct {
	mu      sync.Mutex
	updates []Update
	sent    []map[string]any
	nextID  int64
	// floodEdits answers that many edits with flood control (429).
	floodEdits int
}

func (f *fakeTelegram) push(u Update) {
	f.mu.Lock()
	f.nextID++
	u.UpdateID = f.nextID
	f.updates = append(f.updates, u)
	f.mu.Unlock()
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
	var p map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &p)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": v}) }
	switch method {
	case "getMe":
		reply(User{ID: 1, IsBot: true, Username: "test_bot"})
	case "getUpdates":
		off := int64(p["offset"].(float64))
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			var out []Update
			for _, u := range f.updates {
				if u.UpdateID >= off {
					out = append(out, u)
				}
			}
			f.mu.Unlock()
			if len(out) > 0 {
				reply(out)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		reply([]Update{})
	case "sendMessage":
		f.mu.Lock()
		p["method"] = method
		f.sent = append(f.sent, p)
		id := int64(len(f.sent))
		f.mu.Unlock()
		reply(Message{MessageID: id})
	case "editMessageText", "deleteMessage":
		f.mu.Lock()
		flood := method == "editMessageText" && f.floodEdits > 0
		if flood {
			f.floodEdits--
		}
		f.mu.Unlock()
		if flood {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`))
			return
		}
		f.mu.Lock()
		p["method"] = method
		f.sent = append(f.sent, p)
		f.mu.Unlock()
		reply(true)
	default:
		reply(true)
	}
}

// waitSent waits for a sent message whose text has want.
func (f *fakeTelegram) waitSent(t *testing.T, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, m := range f.sent {
			if s, _ := m["text"].(string); strings.Contains(s, want) && m["method"] == "sendMessage" {
				f.mu.Unlock()
				return m
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("nothing sent with %q; sent: %v", want, f.sent)
	return nil
}

type fakeHost struct {
	mu     sync.Mutex
	events chan gateway.Event
	roots  []string
	made   []string
	cmds   []gateway.Command
}

func (h *fakeHost) Projects() []string                { return h.roots }
func (h *fakeHost) Sessions(string) []gateway.Summary { return nil }
func (h *fakeHost) Session(id string) (gateway.Summary, bool) {
	return gateway.Summary{ID: id, Title: "t", Root: h.roots[0]}, id == "s1"
}
func (h *fakeHost) NewSession(root, prompt string) (string, error) {
	h.mu.Lock()
	h.made = append(h.made, root+"|"+prompt)
	h.mu.Unlock()
	return "s1", nil
}
func (h *fakeHost) Route(c gateway.Command) error {
	h.mu.Lock()
	h.cmds = append(h.cmds, c)
	h.mu.Unlock()
	return nil
}
func (h *fakeHost) Subscribe() (<-chan gateway.Event, func()) { return h.events, func() {} }
func (h *fakeHost) PublicURL() string                         { return "" }
func (h *fakeHost) Tunneled() bool                            { return false }

func ev(typ string, data any) gateway.Event { return gateway.New(typ, "s1", data) }

// A stranger is paired, picks a project, starts a conversation, watches the
// turn and approves a tool call, all from Telegram.
func TestBotPairsPromptsAndApproves(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg)
	defer srv.Close()
	prev := apiBase
	apiBase = srv.URL
	defer func() { apiBase = prev }()
	if _, err := Change(func(s *Settings) error { s.Enabled, s.Token = true, "1:abc"; return nil }); err != nil {
		t.Fatal(err)
	}
	host := &fakeHost{events: make(chan gateway.Event, 16), roots: []string{"/work/app", "/work/other"}}
	b := &Bot{Host: host}
	b.Start()
	defer b.Stop()

	ann := &User{ID: 7, FirstName: "Ann"}
	dm := Chat{ID: 7, Type: "private"}
	tg.push(Update{Message: &Message{MessageID: 1, From: ann, Chat: dm, Text: "hello"}})
	tg.waitSent(t, "Pairing code")
	if host.made != nil {
		t.Fatal("a stranger's message was run")
	}
	code := Load().Pending[0].Code
	if _, err := Approve(code, time.Now()); err != nil {
		t.Fatal(err)
	}

	tg.push(Update{Message: &Message{MessageID: 2, From: ann, Chat: dm, Text: "/projects"}})
	tg.waitSent(t, "Pick the project")
	tg.push(Update{CallbackQuery: &CallbackQuery{ID: "q1", From: *ann, Message: &Message{Chat: dm}, Data: "p:0"}})
	tg.waitSent(t, "Working on <b>app</b>")

	tg.push(Update{Message: &Message{MessageID: 3, From: ann, Chat: dm, Text: "fix the bug"}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		host.mu.Lock()
		n := len(host.made)
		host.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(host.made) != 1 || host.made[0] != "/work/app|fix the bug" {
		t.Fatalf("sessions made: %v", host.made)
	}

	host.events <- ev(gateway.EvTurnStarted, gateway.TurnData{Prompt: "fix the bug"})
	host.events <- ev(gateway.EvApprovalRequest, gateway.ApprovalData{ID: "ap1", Call: session.ToolCall{Name: "Bash", Input: json.RawMessage(`{"command":"rm -rf build"}`)}})
	ask := tg.waitSent(t, "rm -rf build")
	rows, _ := ask["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	allow := rows[0].([]any)[0].(map[string]any)["callback_data"].(string)
	tg.push(Update{CallbackQuery: &CallbackQuery{ID: "q2", From: *ann, Message: &Message{Chat: dm}, Data: allow}})
	deadline = time.Now().Add(5 * time.Second)
	for {
		host.mu.Lock()
		var got *gateway.Command
		for i := range host.cmds {
			if host.cmds[i].Type == gateway.CmdApprove {
				got = &host.cmds[i]
			}
		}
		host.mu.Unlock()
		if got != nil {
			if got.Session != "s1" || got.ID != "ap1" || got.Verdict != "allow" {
				t.Fatalf("approval %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tap was not passed on as an approval")
		}
		time.Sleep(20 * time.Millisecond)
	}

	host.events <- ev(gateway.EvMessage, gateway.MessageData{Message: session.Message{Role: session.RoleAssistant, Text: "Fixed: **tests pass**"}})
	host.events <- ev(gateway.EvTurnDone, gateway.TurnData{})
	tg.waitSent(t, "Fixed: <b>tests pass</b>")
}

// Telegram's posts are taken only with the webhook's secret, and one it
// sends twice is handled once.
func TestWebhookChecksTheSecretAndDropsRepeats(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = Change(func(s *Settings) error { s.WebhookSecret = "s3cret"; return nil })
	b := &Bot{}
	b.st.State = "up"
	b.ctx = t.Context()
	post := func(secret string, id int64) int {
		body := strings.NewReader(`{"update_id":` + strconv.FormatInt(id, 10) + `}`)
		r := httptest.NewRequest("POST", WebhookPath, body)
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		w := httptest.NewRecorder()
		b.ServeWebhook(w, r)
		return w.Code
	}
	if post("wrong", 1) != http.StatusForbidden || post("", 1) != http.StatusForbidden {
		t.Fatal("a post without the secret was taken")
	}
	if post("s3cret", 5) != http.StatusOK || post("s3cret", 5) != http.StatusOK {
		t.Fatal("Telegram's post was refused")
	}
	if len(b.seen) != 1 {
		t.Fatalf("seen %v; a repeat was handled again", b.seen)
	}
}

// A gateway that finds the mark of one that died with its bot connecting
// turns the bot off instead of dying the same way, and says why.
func TestGuardTurnsTheBotOffAfterADeathWhileConnecting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = Change(func(s *Settings) error { s.Enabled, s.Token = true, "1:abc"; return nil })
	b, _ := json.Marshal(mark{PID: 999999, At: time.Now()})
	if err := os.WriteFile(guardPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{Host: &fakeHost{}}
	bot.Start()
	defer bot.Stop()
	if s := Load(); s.Enabled || s.Paused == "" {
		t.Fatalf("settings after the guard: %+v", s)
	}
	if st := bot.Status(); st.State != "off" || !strings.Contains(st.Error, "CrowdStrike") {
		t.Fatalf("status %+v", st)
	}
	if _, err := os.Stat(guardPath()); err == nil {
		t.Fatal("the mark was left behind")
	}
}
