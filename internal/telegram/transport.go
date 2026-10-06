package telegram

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// How updates reach the bot.
//
//   - webhook, whenever the gateway has a public address (the Cloudflare
//     tunnel is up): Telegram posts each update to
//     <address>/api/telegram/webhook, with a secret header only it and the
//     gateway know. Nothing waits on Telegram.
//   - long polling otherwise: getUpdates, the offset kept across restarts.
//     With PollCommand set it runs in a child process, so that whatever
//     happens to the poller — a crash, or security software on the computer
//     stopping a program that polls a bot (it happens: such programs look
//     like malware's remote control) — happens to it, not to the gateway.
//     The bot then says so, and that the tunnel's webhook avoids polling.

// WebhookPath is where Telegram posts updates.
const WebhookPath = "/api/telegram/webhook"

func (b *Bot) transport(ctx context.Context, username string) error {
	if base := b.Host.PublicURL(); base != "" {
		return b.webhook(ctx, username, base)
	}
	if b.Host.Tunneled() {
		// The tunnel is coming (or will be started): its address restarts
		// the bot on the webhook. Polling meanwhile is what security
		// software stops.
		b.set(Status{State: "starting", Username: username, Transport: "webhook", Error: "waiting for the Cloudflare tunnel's address"})
		<-ctx.Done()
		return ctx.Err()
	}
	if err := b.c.call(ctx, "deleteWebhook", map[string]any{}, nil, false); err != nil {
		return err
	}
	b.set(Status{State: "up", Username: username, Transport: "polling"})
	if b.PollCommand != nil {
		return b.pollChild(ctx, username)
	}
	return b.pollHere(ctx)
}

func (b *Bot) webhook(ctx context.Context, username, base string) error {
	st, err := Change(func(s *Settings) error {
		if s.WebhookSecret == "" {
			s.WebhookSecret = newRef() + newRef() + newRef() + newRef()
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = b.c.call(ctx, "setWebhook", map[string]any{
		"url":             base + WebhookPath,
		"secret_token":    st.WebhookSecret,
		"allowed_updates": []string{"message", "callback_query"},
		"max_connections": 4,
	}, nil, false)
	if err != nil {
		return fmt.Errorf("setting the webhook to %s: %w", base, err)
	}
	b.set(Status{State: "up", Username: username, Transport: "webhook"})
	<-ctx.Done()
	// Not deleted on the way out: a restart sets it again, and updates sent
	// meanwhile wait at Telegram, which retries.
	return ctx.Err()
}

// ServeWebhook takes an update Telegram posted. It answers at once; the
// update is handled after.
func (b *Bot) ServeWebhook(w http.ResponseWriter, r *http.Request) {
	secret := Load().WebhookSecret
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var u Update
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&u); err != nil {
		http.Error(w, "bad update", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	ctx, up := b.ctx, b.st.State == "up"
	dup := false
	for _, id := range b.seen {
		if id == u.UpdateID {
			dup = true
		}
	}
	if !dup {
		b.seen = append(b.seen, u.UpdateID)
		if len(b.seen) > 200 {
			b.seen = b.seen[len(b.seen)-200:]
		}
	}
	b.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	if !up || dup || ctx == nil {
		return
	}
	go b.onUpdate(ctx, u)
}

func (b *Bot) pollHere(ctx context.Context) error {
	offset := Load().Offset
	backoff := time.Second
	for {
		ups, err := b.c.getUpdates(ctx, offset)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if err := pollFatal(err); err != nil {
				return err
			}
			log.Printf("telegram: %v", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			offset = u.UpdateID + 1
			b.onUpdate(ctx, u)
		}
		if len(ups) > 0 {
			saveOffset(offset)
		}
	}
}

func saveOffset(o int64) { _, _ = Change(func(s *Settings) error { s.Offset = o; return nil }) }

func pollFatal(err error) error {
	switch {
	case isAPI(err, http.StatusConflict):
		return errors.New("another program is reading this bot's messages (one bot token, one poller): stop it, or make a new bot")
	case isAPI(err, http.StatusUnauthorized):
		return errors.New("Telegram no longer accepts the token")
	}
	return nil
}

// pollChild runs the poll as `agent-tui telegram poll`, reading the updates
// it prints, one JSON object a line.
func (b *Bot) pollChild(ctx context.Context, username string) error {
	quick := 0 // child exits in a row that came before any update
	for {
		offset := Load().Offset
		cmd := b.PollCommand(offset)
		if cmd == nil {
			return b.pollHere(ctx)
		}
		hideWindow(cmd)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		var stderr tail
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
		started, got := time.Now(), 0
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			var u Update
			if json.Unmarshal(sc.Bytes(), &u) != nil {
				continue
			}
			got++
			b.onUpdate(ctx, u)
			saveOffset(u.UpdateID + 1)
		}
		werr := cmd.Wait()
		stop()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := stderr.String()
		if msg != "" && (containsAny(msg, "another program", "no longer accepts")) {
			return errors.New(msg)
		}
		if got == 0 && time.Since(started) < 30*time.Second {
			quick++
		} else {
			quick = 0
		}
		if quick >= 3 {
			code := ""
			if werr != nil {
				code = " (" + werr.Error() + ")"
			}
			return fmt.Errorf("the Telegram poller keeps being stopped%s%s. Security software on this computer may be stopping programs that poll Telegram bots: start the Cloudflare tunnel, and the bot takes its messages by webhook instead", code, map[bool]string{true: ": " + msg, false: ""}[msg != ""])
		}
		log.Printf("telegram: poller exited: %v %s", werr, msg)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(quick+1) * 2 * time.Second):
		}
		b.set(Status{State: "up", Username: username, Transport: "polling"})
	}
}

// Poll is the child's side: long-poll with the saved token from offset and
// print each update as a line of JSON, until an error it cannot ride out.
func Poll(ctx context.Context, w io.Writer, offset int64) error {
	s := Load()
	if s.Token == "" {
		return errors.New("no bot token")
	}
	c := newClient(s.Token)
	enc := json.NewEncoder(w)
	backoff := time.Second
	for {
		ups, err := c.getUpdates(ctx, offset)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if err := pollFatal(err); err != nil {
				return err
			}
			time.Sleep(backoff)
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			if err := enc.Encode(u); err != nil {
				return err
			}
			offset = u.UpdateID + 1
		}
	}
}

// tail keeps the last of a child's stderr.
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(bytesTrim(t.b)) }

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if len(x) > 0 && len(s) >= len(x) && indexOf(s, x) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
