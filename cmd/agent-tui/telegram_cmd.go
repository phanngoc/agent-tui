package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/telegram"
)

// `agent-tui telegram …` answers the bot's pairing requests from a shell, as
// the admin's Remote & Telegram page does.

const telegramUsage = `usage: agent-tui telegram [command]

  status          the bot, who may use it, and who is waiting (default)
  approve CODE    allow whoever the bot gave this pairing code
  reject CODE     turn that request down
`

func telegramCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "status":
		s := telegram.Load()
		fmt.Printf("bot: enabled=%v token=%v policy=%s\n", s.Enabled, s.Token != "", map[bool]string{true: s.Policy, false: "pairing"}[s.Policy != ""])
		for _, p := range s.Allowed {
			fmt.Printf("allowed  %-12d %s @%s\n", p.ID, p.Name, p.Username)
		}
		for _, p := range s.Pending {
			if time.Since(p.Created) < time.Hour {
				fmt.Printf("waiting  %s  %d %s @%s\n", p.Code, p.ID, p.Name, p.Username)
			}
		}
		return nil
	case "poll":
		// The gateway's own: it runs this and reads the updates it prints.
		fs := flag.NewFlagSet("telegram poll", flag.ExitOnError)
		offset := fs.Int64("offset", 0, "the first update to ask for")
		_ = fs.Parse(args)
		return telegram.Poll(context.Background(), os.Stdout, *offset)
	case "approve", "reject":
		if len(args) == 0 {
			return fmt.Errorf("which code? agent-tui telegram %s CODE", sub)
		}
		// Through the gateway when it runs, so the bot tells them.
		if info, ok := gateway.ReadInfo(); ok && info.Addr != "" {
			b, _ := json.Marshal(map[string]string{"code": args[0]})
			resp, err := http.Post("http://"+info.Addr+"/api/telegram/"+sub, "application/json", bytes.NewReader(b))
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					fmt.Println(sub + "d")
					return nil
				}
				var e struct {
					Error string `json:"error"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&e)
				return fmt.Errorf("%s", e.Error)
			}
		}
		if sub == "reject" {
			return telegram.Reject(args[0])
		}
		p, err := telegram.Approve(args[0], time.Now())
		if err == nil {
			fmt.Printf("approved %s (%d)\n", p.Name, p.ID)
		}
		return err
	}
	fmt.Print(telegramUsage)
	return nil
}
