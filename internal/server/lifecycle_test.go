package server

import (
	"net"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
)

// The gateway stops when asked, from any process, and says it is gone: the
// discovery file is withdrawn and the port closes. A stop made on purpose is
// remembered, so terminals do not start it again, until it is started on
// purpose.
func TestGatewayStopsWhenAskedAndStaysStopped(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := New(config.Default(), "test", "")
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(addr) }()
	deadline := time.Now().Add(5 * time.Second)
	for !gateway.Alive(addr) {
		if time.Now().After(deadline) {
			t.Fatal("the gateway never answered")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if a, ok := gateway.Find(); !ok || a != addr {
		t.Fatalf("Find = %q, %v; want %q", a, ok, addr)
	}

	if err := gateway.Stop(addr, false, true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned %v after a stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after the stop")
	}
	if _, ok := gateway.ReadInfo(); ok {
		t.Fatal("the discovery file outlived the gateway")
	}
	if !gateway.StoppedOnPurpose() {
		t.Fatal("a stop with hold was not remembered")
	}
	gateway.ClearStopped()
	if gateway.StoppedOnPurpose() {
		t.Fatal("ClearStopped did not lift it")
	}
}
