package slack

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 121: stalls are bounded on the transport, transfers are not.

// shrinkTimeouts scales the transport bounds down for one test.
func shrinkTimeouts(t *testing.T, header time.Duration) {
	t.Helper()
	oldDial, oldTLS, oldHeader := dialTimeout, tlsHandshakeTimeout, responseHeaderTimeout
	dialTimeout, tlsHandshakeTimeout, responseHeaderTimeout = time.Second, time.Second, header
	t.Cleanup(func() { dialTimeout, tlsHandshakeTimeout, responseHeaderTimeout = oldDial, oldTLS, oldHeader })
}

func newClientAt(t *testing.T, url string) *Client {
	t.Helper()
	// Credential-shaped values are assembled at runtime (ADR 082).
	cfg := &config.Config{UserToken: "xox" + "p-" + "timeout", APIURL: url + "/"}
	return New(cfg, svLogger())
}

func TestNew_AServerThatNeverAnswersFailsInsteadOfHanging(t *testing.T) {
	shrinkTimeouts(t, 100*time.Millisecond)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // accept, read the request, never write headers
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c := newClientAt(t, srv.URL)
	start := time.Now()
	_, err := c.Messages.MessageAt(context.Background(), "C1", "1.0")
	if err == nil {
		t.Fatal("want an error from a server that never answers")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v; the header timeout did not bound the call", d)
	}
}

func TestNew_ASlowButSteadyDownloadIsNotCutOff(t *testing.T) {
	// The body takes several header-timeouts to arrive. A client-wide
	// timeout of that length would abort it; the transport bound must not.
	shrinkTimeouts(t, 100*time.Millisecond)
	const chunks = 6
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fl.Flush()
		for i := 0; i < chunks; i++ {
			_, _ = io.WriteString(w, "chunk;")
			fl.Flush()
			time.Sleep(60 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	c := newClientAt(t, srv.URL)
	var buf bytes.Buffer
	if err := c.Messages.DownloadFile(context.Background(), srv.URL+"/file", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if got := strings.Count(buf.String(), "chunk;"); got != chunks {
		t.Fatalf("received %d of %d chunks", got, chunks)
	}
}

func TestNewHTTPClient_HasNoClientWideTimeout(t *testing.T) {
	hc := newHTTPClient()
	if hc.Timeout != 0 {
		t.Fatalf("Client.Timeout = %v; it would cap whole downloads", hc.Timeout)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", hc.Transport)
	}
	if tr.ResponseHeaderTimeout != responseHeaderTimeout || tr.TLSHandshakeTimeout != tlsHandshakeTimeout || tr.DialContext == nil {
		t.Fatalf("transport bounds not applied: header=%v tls=%v dial=%v", tr.ResponseHeaderTimeout, tr.TLSHandshakeTimeout, tr.DialContext != nil)
	}
	if tr.Proxy == nil {
		t.Fatal("the default transport's proxy setting was dropped")
	}
}

type recordingRT struct{ hits *int }

func (r recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	*r.hits++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}, Request: req}, nil
}

func TestNewHTTPClient_AReplacedDefaultTransportIsRespected(t *testing.T) {
	hits := 0
	old := http.DefaultTransport
	http.DefaultTransport = recordingRT{&hits}
	t.Cleanup(func() { http.DefaultTransport = old })

	hc := newHTTPClient()
	resp, err := hc.Get("https://example.invalid/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	if hits != 1 {
		t.Fatalf("the installed transport saw %d requests, want 1 — it was routed around", hits)
	}
}
