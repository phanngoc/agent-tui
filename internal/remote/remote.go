// Package remote is reaching the gateway from somewhere other than this
// machine: from a phone, through a Cloudflare tunnel.
//
// The gateway has no login of its own. On loopback none is needed: whoever
// can reach the port is already on this machine as this user. A tunnel
// changes that, because whoever has the URL can reach the port. So a request
// that did not come for a loopback name — one that came through the tunnel —
// is let in only with a sign-in:
//
//   - The access key is 128 random bits, made when remote access is first
//     turned on. It is shown only on this machine (the admin's Remote page,
//     as a link and a QR code to open on the phone) and is never sent
//     anywhere by the gateway.
//   - Signing in with it gives the browser a cookie: HttpOnly, Secure,
//     SameSite=Lax, signed with a secret that never leaves this machine,
//     good for 30 days.
//   - Making a new key also makes a new secret, which signs every browser
//     out.
//
// Remote access off — the default — means a non-loopback request is refused
// whatever it carries.
package remote

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// CookieName is the sign-in cookie.
const CookieName = "agent_tui_session"

// SessionLife is how long a sign-in lasts.
const SessionLife = 30 * 24 * time.Hour

// Settings is remote.json, readable by this user only.
type Settings struct {
	// Enabled lets requests through the tunnel in, signed in.
	Enabled bool `json:"enabled"`
	// Key is the access key; Secret signs the cookies.
	Key    string `json:"key,omitempty"`
	Secret string `json:"secret,omitempty"`
	// Tunnel is how the gateway is reached from outside.
	Tunnel Tunnel `json:"tunnel"`
}

// Tunnel is the cloudflared side.
type Tunnel struct {
	// Mode is one of:
	//   - quick: a new https://….trycloudflare.com address each time it
	//     starts; no account needed.
	//   - token: a tunnel made in the Cloudflare dashboard, run with its
	//     token, at the hostname given it there.
	//   - api: made by agent-tui with a Cloudflare API token (Account ·
	//     Cloudflare Tunnel · Edit, Zone · DNS · Edit): the tunnel, its
	//     route to the gateway and the hostname's DNS record.
	Mode     string `json:"mode,omitempty"`
	Token    string `json:"token,omitempty"`
	APIToken string `json:"api_token,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	// AutoStart starts the tunnel with the gateway.
	AutoStart bool `json:"auto_start,omitempty"`
}

var mu sync.Mutex

func path() string { return filepath.Join(config.Dir(), "remote.json") }

// Load reads the settings; missing means off.
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

// Update changes the settings under the lock and saves them. A key and a
// secret are made the first time either is wanted.
func Update(f func(*Settings)) (Settings, error) {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	f(&s)
	if s.Key == "" || s.Secret == "" {
		s.Key, s.Secret = newKey(), newKey()
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(path(), b, 0o600); err != nil {
		return s, err
	}
	return s, nil
}

// Rotate makes a new key and secret: the old link stops working and every
// browser is signed out.
func Rotate() (Settings, error) {
	return Update(func(s *Settings) { s.Key, s.Secret = newKey(), newKey() })
}

func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// CheckKey says key is the access key.
func (s Settings) CheckKey(key string) bool {
	key = strings.TrimSpace(key)
	return s.Key != "" && key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(s.Key)) == 1
}

// NewSession is a cookie value for a browser that just signed in.
func (s Settings) NewSession(now time.Time) string {
	payload := strconv.FormatInt(now.Add(SessionLife).Unix(), 10) + "." + newKey()
	return payload + "." + s.sign(payload)
}

// ValidSession says a cookie value was signed with this secret and has not
// run out.
func (s Settings) ValidSession(v string, now time.Time) bool {
	if s.Secret == "" {
		return false
	}
	i := strings.LastIndexByte(v, '.')
	if i < 0 {
		return false
	}
	payload, sig := v[:i], v[i+1:]
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return false
	}
	exp, err := strconv.ParseInt(strings.SplitN(payload, ".", 2)[0], 10, 64)
	return err == nil && now.Unix() < exp
}

func (s Settings) sign(payload string) string {
	m := hmac.New(sha256.New, []byte(s.Secret))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Limiter slows down guessing at the key: a few wrong tries from one address
// in a window, then nothing more from it until the window ends. The key is
// 128 bits, so this is about noise, not about the key being guessable.
type Limiter struct {
	mu    sync.Mutex
	tries map[string][]time.Time
	Max   int
	Every time.Duration
}

// Allow records a failed try from who, and says whether to answer it at all.
func (l *Limiter) Allow(who string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tries == nil {
		l.tries = map[string][]time.Time{}
	}
	keep := l.tries[who][:0]
	for _, t := range l.tries[who] {
		if now.Sub(t) < l.Every {
			keep = append(keep, t)
		}
	}
	l.tries[who] = keep
	return len(keep) < l.Max
}

// Fail records a wrong key from who.
func (l *Limiter) Fail(who string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tries == nil {
		l.tries = map[string][]time.Time{}
	}
	l.tries[who] = append(l.tries[who], now)
}

// ErrOff is a remote request while remote access is off.
var ErrOff = errors.New("remote access is off: turn it on in the admin, on this machine")

// LoginURL is the link that signs a browser in: the key rides in the
// fragment, which a browser never sends to any server, and the sign-in page
// posts it.
func LoginURL(base, key string) string {
	return fmt.Sprintf("%s/login#key=%s", strings.TrimRight(base, "/"), key)
}
