package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
)

// `agent-tui browser …` sets up and approves the Chrome extension from a
// shell, as the admin's Browser page does. The extension talks to the
// gateway, so these go through it.

const browserUsage = `usage: agent-tui browser [command]

  status          whether Chrome is connected, and who is asking (default)
  extension       write the extension's folder, for Chrome's "Load unpacked"
  approve CODE    let the extension showing this code connect
  reject CODE     turn it down
`

func browserCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	info, ok := gateway.ReadInfo()
	if !ok || info.Addr == "" {
		return errors.New("the gateway is not running: agent-tui gateway start")
	}
	base := "http://" + info.Addr
	call := func(method, path string, body, out any) error {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			var e struct {
				Error string `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&e)
			return errors.New(e.Error)
		}
		if out != nil {
			return json.NewDecoder(resp.Body).Decode(out)
		}
		return nil
	}
	switch sub {
	case "status":
		var st struct {
			Connected bool
			Clients   []struct{ ID, Name, Version string }
			Pending   []struct{ Code, Name, Version string }
			Dir       string `json:"extension_dir"`
			Written   bool   `json:"extension_written"`
		}
		if err := call("GET", "/api/browser/status", nil, &st); err != nil {
			return err
		}
		fmt.Printf("chrome: connected=%v\n", st.Connected)
		for _, c := range st.Clients {
			fmt.Printf("approved  %s · extension %s\n", c.Name, c.Version)
		}
		for _, p := range st.Pending {
			fmt.Printf("asking    code %s · %s · extension %s   (agent-tui browser approve %s)\n", p.Code, p.Name, p.Version, p.Code)
		}
		if !st.Written {
			fmt.Println("the extension's folder is not written yet: agent-tui browser extension")
		} else {
			fmt.Println("extension folder:", st.Dir)
		}
		return nil
	case "extension":
		var r struct{ Path string }
		if err := call("POST", "/api/browser/extension", map[string]any{}, &r); err != nil {
			return err
		}
		fmt.Println(r.Path)
		fmt.Println(`Load it in Chrome: chrome://extensions → Developer mode → Load unpacked → pick that folder. Then approve the code it shows: agent-tui browser approve CODE`)
		return nil
	case "approve", "reject":
		if len(args) == 0 {
			return fmt.Errorf("which code? agent-tui browser %s CODE", sub)
		}
		if err := call("POST", "/api/browser/"+sub, map[string]string{"code": args[0]}, nil); err != nil {
			return err
		}
		fmt.Println(sub + "d")
		return nil
	}
	fmt.Print(browserUsage)
	return nil
}
