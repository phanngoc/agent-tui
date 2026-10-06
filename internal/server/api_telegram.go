package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/telegram"
)

// The Telegram bot (internal/telegram), run by the gateway.

// pollCommand runs the bot's long poll as a child of the gateway, so that
// whatever stops the poller does not stop the gateway (telegram/transport.go).
func pollCommand(offset int64) *exec.Cmd {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	return exec.Command(self, "telegram", "poll", "-offset", strconv.FormatInt(offset, 10))
}

// tgHost is the gateway as the bot sees it.
type tgHost struct{ s *Server }

func (h tgHost) Projects() []string {
	var out []string
	for _, p := range h.s.projectList() {
		if p.Exists {
			out = append(out, p.Root)
		}
	}
	return out
}

func (h tgHost) Sessions(root string) []gateway.Summary {
	var out []gateway.Summary
	for _, sum := range h.s.summaries() {
		if sum.Root == root && !sum.Closed && sum.SideOf == "" {
			out = append(out, sum)
		}
	}
	return out
}

func (h tgHost) Session(id string) (gateway.Summary, bool) {
	if id == "" {
		return gateway.Summary{}, false
	}
	sess, err := gateway.Load(id)
	if err != nil {
		return gateway.Summary{}, false
	}
	sum := gateway.SummaryOf(sess)
	if l, ok := h.s.Hub.LiveAll()[id]; ok {
		sum.Busy, sum.Status = l.Busy, l.Status
	}
	return sum, true
}

func (h tgHost) NewSession(root, prompt string) (string, error) {
	sess, err := h.s.Runner.NewSession(root, "", "", "", "", "", prompt)
	if err != nil {
		return "", err
	}
	return sess.ID, nil
}

func (h tgHost) Route(cmd gateway.Command) error {
	_, err := h.s.Hub.Route(cmd)
	return err
}

func (h tgHost) Subscribe() (<-chan gateway.Event, func()) {
	_, ch, cancel := h.s.Hub.Subscribe(h.s.Hub.Seq())
	return ch, cancel
}

func (h tgHost) Tunneled() bool { return h.s.remoteSettings().Enabled }

func (h tgHost) PublicURL() string {
	if st := h.s.remote.tunnel.Status(); st.State == "up" {
		return st.URL
	}
	return ""
}

func (s *Server) telegramView() map[string]any {
	st := telegram.Load()
	pending := []telegram.Pairing{}
	for _, p := range st.Pending {
		if time.Since(p.Created) < time.Hour {
			pending = append(pending, p)
		}
	}
	allowed := st.Allowed
	if allowed == nil {
		allowed = []telegram.Person{}
	}
	policy := st.Policy
	if policy == "" {
		policy = telegram.PolicyPairing
	}
	return map[string]any{
		"enabled":         st.Enabled,
		"has_token":       st.Token != "",
		"dm_policy":       policy,
		"allowed":         allowed,
		"pending":         pending,
		"quiet_schedules": st.QuietSchedules,
		"streaming":       telegram.ResolveStreaming(st.Streaming),
		"paused":          st.Paused,
		"status":          s.bot.Status(),
	}
}

func (s *Server) telegramRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/telegram", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.telegramView()) })
	m.HandleFunc("POST "+telegram.WebhookPath, s.bot.ServeWebhook)
	m.HandleFunc("PUT /api/telegram", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled        *bool   `json:"enabled"`
			Token          *string `json:"token"`
			Policy         *string `json:"dm_policy"`
			QuietSchedules *bool   `json:"quiet_schedules"`
			Streaming      *string `json:"streaming"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		restart := false
		_, err := telegram.Change(func(st *telegram.Settings) error {
			if in.Token != nil && strings.TrimSpace(*in.Token) != "" {
				tok := strings.TrimSpace(*in.Token)
				if !strings.Contains(tok, ":") {
					return errors.New("that is not a bot token: @BotFather gives one like 123456:ABC…")
				}
				if tok != st.Token {
					st.Token, st.Offset, restart = tok, 0, true
				}
			}
			if in.Enabled != nil && *in.Enabled != st.Enabled {
				st.Enabled, restart = *in.Enabled, true
				st.Paused = ""
			}
			if in.Policy != nil {
				switch *in.Policy {
				case telegram.PolicyPairing, telegram.PolicyAllowlist, telegram.PolicyDisabled:
					st.Policy = *in.Policy
				default:
					return errors.New("dm_policy is pairing, allowlist or disabled")
				}
			}
			if in.QuietSchedules != nil {
				st.QuietSchedules = *in.QuietSchedules
			}
			if in.Streaming != nil {
				if telegram.ResolveStreaming(*in.Streaming) != *in.Streaming {
					return errors.New("streaming is progress, partial, block or off")
				}
				st.Streaming = *in.Streaming
			}
			if st.Enabled && st.Token == "" {
				return errors.New("paste the bot's token first")
			}
			return nil
		})
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if restart {
			s.bot.Start()
		}
		s.changed("telegram", "")
		writeJSON(w, s.telegramView())
	})
	m.HandleFunc("POST /api/telegram/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code string `json:"code"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := telegram.Approve(in.Code, time.Now())
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.bot.SendTo(ctx, p.Chat, "✅ You are paired. /projects picks the project to work on; /help lists the rest.")
		s.changed("telegram", "")
		writeJSON(w, s.telegramView())
	})
	m.HandleFunc("POST /api/telegram/reject", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code string `json:"code"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := telegram.Reject(in.Code); err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		s.changed("telegram", "")
		writeJSON(w, s.telegramView())
	})
	m.HandleFunc("POST /api/telegram/revoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID int64 `json:"id"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := telegram.Revoke(in.ID); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed("telegram", "")
		writeJSON(w, s.telegramView())
	})
	m.HandleFunc("POST /api/telegram/test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := s.bot.Notify(ctx, "👋 agent-tui is connected. Send a prompt, or /help."); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}
