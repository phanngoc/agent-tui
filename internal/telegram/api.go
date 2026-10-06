package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The Bot API, as much of it as the bot uses.

// apiBase is Telegram's endpoint; a test points it at a fake.
var apiBase = "https://api.telegram.org"

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Name is how a person is shown.
func (u User) Name() string {
	n := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if n == "" {
		n = u.Username
	}
	if n == "" {
		n = fmt.Sprint(u.ID)
	}
	return n
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
	Caption   string `json:"caption,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// Button is an inline keyboard button.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// APIError is Telegram refusing a call.
type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram: %s (%d)", e.Description, e.Code) }

// isAPI says err is Telegram's answer with this code.
func isAPI(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == code
}

// client calls the Bot API for one token.
//
// Telegram's flood control is per token: after a 429 every call waits out
// retry_after, and a later, shorter 429 never brings that forward. A call
// that must land (a reply) waits and tries again; one that a newer one
// replaces (an edit of the progress message, the typing indicator) is
// dropped instead.
type client struct {
	token string
	hc    *http.Client
	mu    sync.Mutex
	gate  time.Time
}

func newClient(token string) *client {
	return &client{token: token, hc: &http.Client{Timeout: 70 * time.Second}}
}

var errSkipped = errors.New("telegram: skipped during flood wait")

// call runs a method. replaceable calls are skipped while flood control
// holds; the others wait for it, up to five minutes in all.
func (c *client) call(ctx context.Context, method string, params, out any, replaceable bool) error {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		c.mu.Lock()
		wait := time.Until(c.gate)
		c.mu.Unlock()
		if wait > 0 {
			if replaceable {
				return errSkipped
			}
			if time.Now().Add(wait).After(deadline) {
				return fmt.Errorf("telegram: flood wait of %s", wait.Round(time.Second))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		err := c.once(ctx, method, params, out)
		var e *APIError
		if errors.As(err, &e) && e.Code == http.StatusTooManyRequests {
			after := time.Duration(max(e.RetryAfter, 1)) * time.Second
			c.mu.Lock()
			if t := time.Now().Add(after); t.After(c.gate) {
				c.gate = t
			}
			c.mu.Unlock()
			if replaceable {
				return errSkipped
			}
			continue
		}
		return err
	}
}

func (c *client) once(ctx context.Context, method string, params, out any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/bot"+c.token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		// The URL holds the token; the error must not.
		return fmt.Errorf("telegram %s: %s", method, strings.ReplaceAll(err.Error(), c.token, "<token>"))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("telegram %s: %s", method, resp.Status)
	}
	if !env.OK {
		return &APIError{Code: env.ErrorCode, Description: env.Description, RetryAfter: env.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (c *client) getMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", map[string]any{}, &u, false)
	return u, err
}

func (c *client) getUpdates(ctx context.Context, offset int64) ([]Update, error) {
	var ups []Update
	err := c.once(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 50,
		"allowed_updates": []string{"message", "callback_query"}}, &ups)
	return ups, err
}

func keyboard(rows [][]Button) map[string]any {
	return map[string]any{"inline_keyboard": rows}
}

// send sends HTML text, falling back to plain text if Telegram cannot parse
// it; it returns the message id.
func (c *client) send(ctx context.Context, chat int64, html string, rows [][]Button) (int64, error) {
	p := map[string]any{"chat_id": chat, "text": html, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if rows != nil {
		p["reply_markup"] = keyboard(rows)
	}
	var m Message
	err := c.call(ctx, "sendMessage", p, &m, false)
	if isAPI(err, http.StatusBadRequest) && strings.Contains(err.Error(), "parse") {
		delete(p, "parse_mode")
		p["text"] = plain(html)
		err = c.call(ctx, "sendMessage", p, &m, false)
	}
	return m.MessageID, err
}

// edit replaces a message's text; "not modified" is not an error.
func (c *client) edit(ctx context.Context, chat, msg int64, html string, rows [][]Button, replaceable bool) error {
	p := map[string]any{"chat_id": chat, "message_id": msg, "text": html, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if rows != nil {
		p["reply_markup"] = keyboard(rows)
	} else {
		p["reply_markup"] = keyboard([][]Button{})
	}
	err := c.call(ctx, "editMessageText", p, nil, replaceable)
	if err != nil && strings.Contains(err.Error(), "not modified") {
		return nil
	}
	return err
}

func (c *client) delete(ctx context.Context, chat, msg int64) {
	_ = c.call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": msg}, nil, false)
}

func (c *client) typing(ctx context.Context, chat int64) {
	_ = c.call(ctx, "sendChatAction", map[string]any{"chat_id": chat, "action": "typing"}, nil, true)
}

func (c *client) answer(ctx context.Context, id, text string) {
	_ = c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil, true)
}
