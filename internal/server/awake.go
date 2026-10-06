package server

import (
	"context"
	"fmt"
	"time"

	"github.com/phanngoc/agent-tui/internal/awake"
	"github.com/phanngoc/agent-tui/internal/config"
)

// Keeping the computer awake while the agent works (internal/awake): every
// few seconds, and whenever the setting changes, the gateway decides
// whether it is wanted now and holds or lets go.

func (s *Server) watchAwake(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	s.checkAwake()
	for {
		select {
		case <-ctx.Done():
			s.awake.Close()
			return
		case <-t.C:
			s.checkAwake()
		}
	}
}

// checkAwake decides, from the setting and what is happening.
func (s *Server) checkAwake() {
	p := config.LoadPrefs()
	mode := awake.ResolveMode(p.KeepAwake)
	busy := 0
	for _, l := range s.Hub.LiveAll() {
		if l.Busy {
			busy++
		}
	}
	jobs := 0
	if mode == awake.ModeSchedules {
		if list, err := s.Sched.Store.List(); err == nil {
			for _, j := range list {
				if j.Enabled {
					jobs++
				}
			}
		}
	}
	want, reason := false, ""
	switch {
	case mode == awake.ModeOff:
	case mode == awake.ModeAlways:
		want, reason = true, "always, while the gateway runs"
	case busy > 0:
		want, reason = true, plural(busy, "turn")+" running"
	case jobs > 0:
		want, reason = true, plural(jobs, "scheduled job")+" on"
	}
	s.awake.Set(mode, want, p.KeepDisplay, reason)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
