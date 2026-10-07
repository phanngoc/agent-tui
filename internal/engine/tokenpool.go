package engine

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// poolDir is where the Claude token pool lives, with what agent-tui keeps
// beside it: which conversation has which token, and what each has spent.
func poolDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "token-rotation")
}

// Only a child gets the selected credential. Neither the process environment
// nor a session/transcript ever contains it; what they get is its name.
func claudeTokenEnv(ctx context.Context, env []string, conversation string) ([]string, session.Credential, error) {
	token, cred, err := conversationToken(ctx, conversation)
	if err != nil || token == "" {
		return env, session.Credential{}, err
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN":
			continue
		}
		out = append(out, entry)
	}
	return append(out, "CLAUDE_CODE_OAUTH_TOKEN="+token), cred, nil
}

// usageFile is the record of what each pool token has spent: a line per
// turn, appended, so the terminal and the gateway can both write to it
// without taking turns.
const usageFile = "agent-tui-usage.jsonl"

type usageLine struct {
	At time.Time `json:"at"`
	session.Credential
	In         int64         `json:"in"`
	Out        int64         `json:"out"`
	CacheRead  int64         `json:"cache_read"`
	CacheWrite int64         `json:"cache_write"`
	Limits     []agent.Limit `json:"limits,omitempty"`
}

// recordUsage adds a turn's spending to the token it ran on. Losing a line
// costs a number on a dashboard, never the turn, so failures are dropped.
func recordUsage(dir string, c session.Credential, u agent.EvUsage, at time.Time) {
	if dir == "" || c.ID == "" {
		return
	}
	b, err := json.Marshal(usageLine{At: at, Credential: c, In: u.In, Out: u.Out, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Limits: u.Limits})
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, usageFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Tally is tokens spent over some stretch.
type Tally struct {
	Turns      int64 `json:"turns"`
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

func (t *Tally) add(l usageLine) {
	t.Turns++
	t.In += l.In
	t.Out += l.Out
	t.CacheRead += l.CacheRead
	t.CacheWrite += l.CacheWrite
}

// PoolToken is what is known of one token of the pool. A slot nothing has
// run on yet has no ID.
type PoolToken struct {
	session.Credential
	Conversations int           `json:"conversations"`
	Today         Tally         `json:"today"`
	Week          Tally         `json:"week"`
	Total         Tally         `json:"total"`
	LastUsed      time.Time     `json:"last_used,omitzero"`
	Limits        []agent.Limit `json:"limits,omitempty"`
	LimitsAt      time.Time     `json:"limits_at,omitzero"`
}

// PoolReport is the pool as a whole: whether there is one, its size, and
// each token in it.
type PoolReport struct {
	Enabled bool        `json:"enabled"`
	Size    int         `json:"size"`
	Tokens  []PoolToken `json:"tokens"`
}

// clixmlText reads an Export-Clixml file, which Windows PowerShell writes in
// UTF-16 and PowerShell 7 in UTF-8.
func clixmlText(b []byte) string {
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		return string(b)
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2+2*i:])
	}
	return string(utf16.Decode(u))
}

// PoolUsage reports what each token of the Claude pool has spent.
func PoolUsage() PoolReport { return poolUsage(poolDir(), time.Now()) }

func poolUsage(dir string, now time.Time) PoolReport {
	rep := PoolReport{Tokens: []PoolToken{}}
	if dir == "" {
		return rep
	}
	pool, err := os.ReadFile(filepath.Join(dir, "pool.xml"))
	if err != nil {
		return rep
	}
	rep.Enabled = true
	// Each credential has one user name; counting them sizes the pool
	// without decrypting it.
	rep.Size = strings.Count(clixmlText(pool), `<S N="UserName">`)

	byID := map[string]*PoolToken{}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	week := day.AddDate(0, 0, -6)
	if f, err := os.Open(filepath.Join(dir, usageFile)); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			var l usageLine
			if json.Unmarshal(sc.Bytes(), &l) != nil || l.ID == "" {
				continue
			}
			t := byID[l.ID]
			if t == nil {
				t = &PoolToken{}
				byID[l.ID] = t
			}
			// The latest line says where the token sits now.
			if !l.At.Before(t.LastUsed) {
				t.Credential = l.Credential
				t.LastUsed = l.At
			}
			if len(l.Limits) > 0 && !l.At.Before(t.LimitsAt) {
				t.Limits, t.LimitsAt = l.Limits, l.At
			}
			t.Total.add(l)
			if !l.At.Before(week) {
				t.Week.add(l)
			}
			if !l.At.Before(day) {
				t.Today.add(l)
			}
		}
		f.Close()
	}

	// Conversations hold their token for good; the assignments say how many
	// each has, by its full fingerprint.
	if b, err := os.ReadFile(filepath.Join(dir, "agent-tui-state.json")); err == nil {
		var st struct {
			Assignments map[string]string `json:"assignments"`
		}
		if json.Unmarshal(bytes.TrimPrefix(b, []byte("\uFEFF")), &st) == nil {
			for _, full := range st.Assignments {
				for id, t := range byID {
					if strings.HasPrefix(full, id) {
						t.Conversations++
					}
				}
			}
		}
	}

	taken := map[int]bool{}
	for _, t := range byID {
		if t.Of == rep.Size {
			taken[t.Slot] = true
		}
		rep.Tokens = append(rep.Tokens, *t)
	}
	for slot := 1; slot <= rep.Size; slot++ {
		if !taken[slot] {
			rep.Tokens = append(rep.Tokens, PoolToken{Credential: session.Credential{Slot: slot, Of: rep.Size}})
		}
	}
	slices.SortFunc(rep.Tokens, func(a, b PoolToken) int {
		return cmp.Or(cmp.Compare(a.Slot, b.Slot), b.LastUsed.Compare(a.LastUsed))
	})
	return rep
}
