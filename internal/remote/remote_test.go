package remote

import (
	"strings"
	"testing"
	"time"
)

// A sign-in is good until it runs out, and only with the secret that signed
// it: a new key signs everyone out, and an edited cookie is nothing.
func TestSessionsAreSignedAndRunOut(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := Update(func(s *Settings) { s.Enabled = true })
	if err != nil || len(st.Key) < 20 || st.Secret == "" {
		t.Fatalf("settings %+v %v", st, err)
	}
	if !st.CheckKey(st.Key) || st.CheckKey("") || st.CheckKey(st.Key+"x") {
		t.Fatal("CheckKey")
	}
	now := time.Now()
	c := st.NewSession(now)
	if !st.ValidSession(c, now) || !st.ValidSession(c, now.Add(29*24*time.Hour)) {
		t.Fatal("a fresh sign-in is not valid")
	}
	if st.ValidSession(c, now.Add(SessionLife+time.Minute)) {
		t.Fatal("a sign-in outlived its life")
	}
	parts := strings.SplitN(c, ".", 2)
	if st.ValidSession("9999999999."+parts[1], now) {
		t.Fatal("a cookie with its expiry changed was accepted")
	}
	rot, _ := Rotate()
	if rot.ValidSession(c, now) || rot.CheckKey(st.Key) {
		t.Fatal("a new key left the old sign-ins and key working")
	}
	if !Load().Enabled || Load().Key != rot.Key {
		t.Fatal("the settings were not kept")
	}
}

func TestLimiterStopsAfterTooManyMisses(t *testing.T) {
	l := Limiter{Max: 3, Every: time.Minute}
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.Allow("a", now) {
			t.Fatalf("try %d refused", i)
		}
		l.Fail("a", now)
	}
	if l.Allow("a", now) || !l.Allow("b", now) || !l.Allow("a", now.Add(2*time.Minute)) {
		t.Fatal("the limiter is per address and per window")
	}
}
