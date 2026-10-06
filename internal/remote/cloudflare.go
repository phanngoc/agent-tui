package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The Cloudflare API, for the api tunnel mode.
var cfAPI = "https://api.cloudflare.com/client/v4"

// tunnelName is the tunnel agent-tui makes, one per hostname.
func tunnelName(host string) string { return "agent-tui-" + strings.ReplaceAll(host, ".", "-") }

type cfClient struct {
	token string
	hc    *http.Client
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c cfClient) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfAPI+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Success bool            `json:"success"`
		Errors  []cfError       `json:"errors"`
		Result  json.RawMessage `json:"result"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("cloudflare %s %s: %s", method, path, resp.Status)
	}
	if !env.Success {
		msgs := []string{}
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%s (%d)", e.Message, e.Code))
		}
		return fmt.Errorf("cloudflare %s: %s", what(method, path), strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// what names a call for an error a person reads: which permission is
// missing is the usual answer.
func what(method, path string) string {
	switch {
	case strings.Contains(path, "/dns_records"):
		return "the DNS record (the token needs Zone · DNS · Edit for this zone)"
	case strings.Contains(path, "/cfd_tunnel"):
		return "the tunnel (the token needs Account · Cloudflare Tunnel · Edit)"
	case strings.HasPrefix(path, "/zones"):
		return "the zones (the token needs Zone · Zone · Read)"
	}
	return method + " " + path
}

type cfZone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// Provision makes sure the api mode's tunnel exists, routes t.Hostname to
// the gateway at local, and has a DNS record; it returns the token
// cloudflared runs it with. Every step finds what an earlier run made before
// making anything, so running it again changes nothing.
func Provision(ctx context.Context, t Tunnel, local string) (string, error) {
	if t.APIToken == "" || t.Hostname == "" {
		return "", fmt.Errorf("the api mode needs a Cloudflare API token and a hostname in one of its zones")
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(t.Hostname), "."))
	c := cfClient{token: t.APIToken, hc: &http.Client{Timeout: 30 * time.Second}}

	var zones []cfZone
	if err := c.do(ctx, "GET", "/zones?per_page=50", nil, &zones); err != nil {
		return "", err
	}
	var zone *cfZone
	for i, z := range zones {
		if (host == z.Name || strings.HasSuffix(host, "."+z.Name)) && (zone == nil || len(z.Name) > len(zone.Name)) {
			zone = &zones[i]
		}
	}
	if zone == nil {
		names := []string{}
		for _, z := range zones {
			names = append(names, z.Name)
		}
		return "", fmt.Errorf("%s is in none of the token's zones (%s)", host, strings.Join(names, ", "))
	}
	acct := zone.Account.ID

	var tunnels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	name := tunnelName(host)
	if err := c.do(ctx, "GET", "/accounts/"+acct+"/cfd_tunnel?is_deleted=false&name="+url.QueryEscape(name), nil, &tunnels); err != nil {
		return "", err
	}
	id := ""
	if len(tunnels) > 0 {
		id = tunnels[0].ID
	} else {
		var made struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, "POST", "/accounts/"+acct+"/cfd_tunnel", map[string]any{"name": name, "config_src": "cloudflare"}, &made); err != nil {
			return "", err
		}
		id = made.ID
	}

	ingress := map[string]any{"config": map[string]any{"ingress": []map[string]any{
		{"hostname": host, "service": local},
		{"service": "http_status:404"},
	}}}
	if err := c.do(ctx, "PUT", "/accounts/"+acct+"/cfd_tunnel/"+id+"/configurations", ingress, nil); err != nil {
		return "", err
	}

	target := id + ".cfargotunnel.com"
	var recs []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := c.do(ctx, "GET", "/zones/"+zone.ID+"/dns_records?name="+url.QueryEscape(host), nil, &recs); err != nil {
		return "", err
	}
	rec := map[string]any{"type": "CNAME", "name": host, "content": target, "proxied": true, "comment": "agent-tui gateway tunnel"}
	switch {
	case len(recs) == 0:
		if err := c.do(ctx, "POST", "/zones/"+zone.ID+"/dns_records", rec, nil); err != nil {
			return "", err
		}
	case recs[0].Type == "CNAME" && recs[0].Content == target:
	case recs[0].Type == "CNAME" && strings.HasSuffix(recs[0].Content, ".cfargotunnel.com"):
		// An earlier tunnel of ours for this hostname.
		if err := c.do(ctx, "PUT", "/zones/"+zone.ID+"/dns_records/"+recs[0].ID, rec, nil); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("%s already has a %s record (%s); pick a hostname nothing uses", host, recs[0].Type, recs[0].Content)
	}

	var token string
	if err := c.do(ctx, "GET", "/accounts/"+acct+"/cfd_tunnel/"+id+"/token", nil, &token); err != nil {
		return "", err
	}
	return token, nil
}
