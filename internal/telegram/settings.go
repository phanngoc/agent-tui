package telegram

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Who may talk to the bot, as OpenClaw has it:
//
//   - pairing (the default): someone the bot does not know gets a code and
//     nothing else; their message is not run. The code is approved on the
//     gateway's computer — the admin's Remote & Telegram page, or
//     `agent-tui telegram approve CODE` — and from then on they are allowed.
//     A code is 8 letters without the ones that read alike (0 O 1 I), lasts
//     an hour, and at most three wait at once.
//   - allowlist: only those already allowed; strangers get no answer.
//   - disabled: nobody.
//
// Only private chats are served: a bot in a group would run the agent for
// whoever is in the group.

// Policies.
const (
	PolicyPairing   = "pairing"
	PolicyAllowlist = "allowlist"
	PolicyDisabled  = "disabled"
)

const (
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	codeLen      = 8
	codeLife     = time.Hour
	maxPending   = 3
)

// Settings is telegram.json, readable by this user only.
type Settings struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"`
	Policy  string `json:"dm_policy,omitempty"`
	// Allowed are the people who may use the bot.
	Allowed []Person  `json:"allowed,omitempty"`
	Pending []Pairing `json:"pending,omitempty"`
	// Chats is what each chat is working on, by chat id.
	Chats map[string]*ChatState `json:"chats,omitempty"`
	// QuietSchedules keeps scheduled runs' reports out of Telegram.
	QuietSchedules bool `json:"quiet_schedules,omitempty"`
	// Streaming is how a running turn shows (progress.go): progress (the
	// default), partial, block or off.
	Streaming string `json:"streaming,omitempty"`
	// Offset is the next update to ask for, so a restart neither misses
	// nor repeats messages.
	Offset int64 `json:"offset,omitempty"`
	// Paused says why the bot was turned off for the user (guard.go).
	Paused string `json:"paused,omitempty"`
	// WebhookSecret is the header Telegram sends with each update it posts.
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// Person is someone allowed.
type Person struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name,omitempty"`
	Username string    `json:"username,omitempty"`
	Since    time.Time `json:"since"`
}

// Pairing is a stranger's request to be allowed.
type Pairing struct {
	Code     string    `json:"code"`
	ID       int64     `json:"id"`
	Name     string    `json:"name,omitempty"`
	Username string    `json:"username,omitempty"`
	Chat     int64     `json:"chat"`
	Created  time.Time `json:"created"`
}

// ChatState is the project and conversation a chat is working on.
type ChatState struct {
	Root    string `json:"root,omitempty"`
	Session string `json:"session,omitempty"`
}

func (s Settings) policy() string {
	if s.Policy == "" {
		return PolicyPairing
	}
	return s.Policy
}

// IsAllowed says id may use the bot.
func (s Settings) IsAllowed(id int64) bool {
	if s.policy() == PolicyDisabled {
		return false
	}
	for _, p := range s.Allowed {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (s *Settings) chat(id int64) *ChatState {
	if s.Chats == nil {
		s.Chats = map[string]*ChatState{}
	}
	k := strconv.FormatInt(id, 10)
	if s.Chats[k] == nil {
		s.Chats[k] = &ChatState{}
	}
	return s.Chats[k]
}

// ChatsOn are the chats whose current conversation is session.
func (s Settings) ChatsOn(session string) []int64 {
	var out []int64
	for k, c := range s.Chats {
		if c != nil && c.Session == session {
			if id, err := strconv.ParseInt(k, 10, 64); err == nil {
				out = append(out, id)
			}
		}
	}
	return out
}

var mu sync.Mutex

func path() string { return filepath.Join(config.Dir(), "telegram.json") }

// Load reads the settings.
func Load() Settings {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() Settings {
	var s Settings
	if b, err := os.ReadFile(path()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Change changes the settings under the lock and saves them.
func Change(f func(*Settings) error) (Settings, error) {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	if err := f(&s); err != nil {
		return s, err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return s, os.WriteFile(path(), b, 0o600)
}

func newCode() string {
	var b strings.Builder
	for i := 0; i < codeLen; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String()
}

// prune drops pairing requests that ran out.
func (s *Settings) prune(now time.Time) {
	keep := s.Pending[:0]
	for _, p := range s.Pending {
		if now.Sub(p.Created) < codeLife {
			keep = append(keep, p)
		}
	}
	s.Pending = keep
}

// RequestPairing gives a stranger a code, the one they already have if it
// is still good. fresh says it is new, which is when the bot says it; ""
// means too many are waiting.
func RequestPairing(u User, chat int64, now time.Time) (code string, fresh bool, err error) {
	_, err = Change(func(s *Settings) error {
		s.prune(now)
		for _, p := range s.Pending {
			if p.ID == u.ID {
				code = p.Code
				return nil
			}
		}
		if len(s.Pending) >= maxPending {
			return nil
		}
		code, fresh = newCode(), true
		s.Pending = append(s.Pending, Pairing{Code: code, ID: u.ID, Name: u.Name(), Username: u.Username, Chat: chat, Created: now})
		return nil
	})
	return code, fresh, err
}

// Approve allows whoever asked with code.
func Approve(code string, now time.Time) (Pairing, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	var got Pairing
	_, err := Change(func(s *Settings) error {
		s.prune(now)
		for i, p := range s.Pending {
			if p.Code == code {
				got = p
				s.Pending = append(s.Pending[:i], s.Pending[i+1:]...)
				if !s.IsAllowed(p.ID) {
					s.Allowed = append(s.Allowed, Person{ID: p.ID, Name: p.Name, Username: p.Username, Since: now})
				}
				return nil
			}
		}
		return errors.New("no pairing request has that code (they last an hour)")
	})
	return got, err
}

// Reject drops a pairing request.
func Reject(code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	_, err := Change(func(s *Settings) error {
		for i, p := range s.Pending {
			if p.Code == code {
				s.Pending = append(s.Pending[:i], s.Pending[i+1:]...)
				return nil
			}
		}
		return errors.New("no pairing request has that code")
	})
	return err
}

// Revoke takes someone off the allowed list.
func Revoke(id int64) error {
	_, err := Change(func(s *Settings) error {
		keep := s.Allowed[:0]
		for _, p := range s.Allowed {
			if p.ID != id {
				keep = append(keep, p)
			}
		}
		s.Allowed = keep
		delete(s.Chats, strconv.FormatInt(id, 10))
		return nil
	})
	return err
}
