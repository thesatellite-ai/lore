package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestServeRefusesANonLoopbackHost (devco D-331): the live `lore api`
// answers only a request whose Host names this machine by a loopback name
// (localhost, 127.0.0.1, [::1], with or without a port); any other name (a
// rebound DNS name, an empty Host) gets a plain 403 before the token
// check, even with the right bearer token, and the body never echoes the
// Host. Break-verified: with Serve using s.handler directly the made-up
// names reach the routes (200 with the token).
func TestServeRefusesANonLoopbackHost(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	s, err := New(f.reg, f, Options{Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	port := addr[strings.LastIndex(addr, ":")+1:]
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln, io.Discard, "") }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	get := func(host string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/v1/memories", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, h := range []string{"127.0.0.1:" + port, "localhost:" + port, "LOCALHOST:" + port, "[::1]:" + port, "localhost", "127.0.0.1", "[::1]"} {
		if code, body := get(h); code != http.StatusOK {
			t.Errorf("Host %q: %d %s, want 200", h, code, body)
		}
	}
	for _, h := range []string{"rebound.example:" + port, "rebound.example", "127.0.0.1.rebound.example", "localhost.rebound.example:" + port, "10.1.2.3:" + port} {
		code, body := get(h)
		if code != http.StatusForbidden || !strings.Contains(body, "localhost, 127.0.0.1 or [::1]") {
			t.Errorf("Host %q: %d %q, want 403 naming the accepted names", h, code, body)
		}
		if strings.Contains(body, "rebound") {
			t.Errorf("Host %q: the refusal echoed the Host: %q", h, body)
		}
	}
}

// TestHostAllowed pins the name rule, including an empty Host.
func TestHostAllowed(t *testing.T) {
	t.Parallel()
	for host, want := range map[string]bool{
		"127.0.0.1:7461": true, "localhost": true, "localhost.:7461": true, "[::1]": true, "[::1]:7461": true,
		"": false, "rebound.example": false, "127.0.0.2": false, "[::2]:7461": false, "0.0.0.0:7461": false,
	} {
		if got := hostAllowed(host); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}
