package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Stopping would cancel the turns the gateway runs, so a gateway running some
// refuses, naming them, and nothing is marked stopped.
func TestStopRefusedWhileTurnsRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/gateway/shutdown" && r.URL.Query().Get("force") != "1" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"busy":["s1","s2"]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	err := Stop(strings.TrimPrefix(ts.URL, "http://"), false, true)
	var busy *BusyError
	if !errors.As(err, &busy) || len(busy.Sessions) != 2 {
		t.Fatalf("Stop = %v; want a BusyError naming two sessions", err)
	}
	if StoppedOnPurpose() {
		t.Fatal("a refused stop was remembered as a stop")
	}
}

// Starting on purpose finds a gateway already running instead of starting a
// second, and lifts a stop made on purpose.
func TestEnsureFindsTheRunningGatewayAndLiftsAStop(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	if err := WriteInfo(Info{Addr: addr, PID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := MarkStopped(); err != nil {
		t.Fatal(err)
	}
	got, started, err := Ensure()
	if err != nil || started || got != addr {
		t.Fatalf("Ensure = %q, %v, %v; want %q, false, nil", got, started, err, addr)
	}
	if StoppedOnPurpose() {
		t.Fatal("Ensure did not lift the stop")
	}
}
