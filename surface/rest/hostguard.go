package rest

// hostguard.go — `lore api` answers only requests addressed to this
// machine by a loopback name (devco D-331).
//
// Why: the server listens on 127.0.0.1 only, but a web page on another
// site can re-point its own DNS name at 127.0.0.1 (DNS rebinding); the
// browser then sends that page's requests here with the page's own name as
// Host and lets the page read the answers as same-origin. Every route but
// the bearer check would see such a request, so it is refused first, on
// the Host header alone, before any route or the token check runs. The
// setup page has its own stricter check (setupweb checkHost: exactly
// 127.0.0.1:<port>).

import (
	"net"
	"net/http"
	"strings"
)

// hostRefusedText is the plain 403 body for a request whose Host is not a
// loopback name. It names the accepted names and never echoes the Host.
const hostRefusedText = "lore api answers only requests addressed to localhost, 127.0.0.1 or [::1]; open it as http://127.0.0.1:<port>"

// loopbackHosts are the Host names (port stripped, lower case) the server
// answers. hostAllowed also accepts BindHost, so a later bind flag only
// changes BindHost.
var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// hostAllowed reports whether a request's Host header names this machine
// by a loopback name, with or without a port. An empty Host (an HTTP/1.0
// request with none) is refused: a client of this server always sends it.
func hostAllowed(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		name = host[1 : len(host)-1] // [::1] without a port
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	return loopbackHosts[name] || name == BindHost
}

// guardHost answers 403 with hostRefusedText for a request whose Host is
// not a loopback name, and passes every other request to next.
func guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host) {
			w.Header().Set(headerNoSniff, noSniff)
			http.Error(w, hostRefusedText, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
