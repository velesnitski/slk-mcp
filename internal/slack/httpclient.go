package slack

import (
	"net"
	"net/http"
	"time"
)

// Timeouts for the Web API transport. See ADR 121.
//
// slack-go defaults to a bare http.Client with no timeout, so a server
// that accepts the connection and never answers blocks a tool call for
// good. The obvious fix — http.Client.Timeout — bounds the whole exchange
// including the body, and the same clients download files: a huddle
// recording or a large PDF that streams for longer than the bound would
// fail mid-transfer. So the bounds sit on the transport instead, on the
// phases that only stall when something is wrong: connecting, the TLS
// handshake, and waiting for response headers. A body that keeps
// arriving is never cut off.
//
// Variables, not constants, so tests can shrink them.
var (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
)

// newHTTPClient returns the client both Web API identities share — one
// connection pool, the default transport's proxy and HTTP/2 settings,
// and the stall bounds above.
//
// When the process has replaced http.DefaultTransport with something
// else — a proxying or recording RoundTripper — that choice is kept and
// requests go through it, unbounded: silently routing around an
// installed transport would be worse than a missing timeout.
func newHTTPClient() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Transport: defaultTransport{}}
	}
	t := base.Clone()
	t.DialContext = (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = tlsHandshakeTimeout
	t.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Transport: t}
}

// defaultTransport defers to whatever http.DefaultTransport is at
// request time.
type defaultTransport struct{}

func (defaultTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(r)
}
