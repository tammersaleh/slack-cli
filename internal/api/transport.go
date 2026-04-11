package api

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// ChromeUserAgent is the user-agent string used for all API requests when
// cookie-based authentication is active. Matches Chrome to avoid Slack's
// anomaly detection on Enterprise Grid.
const ChromeUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36"

// slackHTTPClient returns an *http.Client that injects a d cookie,
// Chrome user-agent, and Chrome TLS fingerprint on every request.
// extraCAs adds additional root CAs (for testing with httptest TLS servers).
func slackHTTPClient(cookie string, extraCAs *x509.CertPool) *http.Client {
	return &http.Client{
		Transport: &slackTransport{
			cookie: cookie,
			base:   newChromeTLSTransport(extraCAs),
		},
	}
}

// ChromeTLSClient returns an *http.Client with Chrome's TLS fingerprint.
// Used by the auth package for validation calls during desktop login.
func ChromeTLSClient() *http.Client {
	return &http.Client{Transport: newChromeTLSTransport(nil)}
}

// slackTransport injects the Slack d cookie and Chrome user-agent into every request.
type slackTransport struct {
	cookie string
	base   http.RoundTripper
}

func (t *slackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Cookie", "d="+t.cookie)
	req.Header.Set("User-Agent", ChromeUserAgent)
	return t.base.RoundTrip(req)
}

// chromeTLSTransport handles TLS connections with Chrome's fingerprint.
// It negotiates ALPN and delegates to http2.Transport for h2 connections
// or falls back to http.Transport for http/1.1.
type chromeTLSTransport struct {
	extraCAs  *x509.CertPool
	h1        *http.Transport
	h2        *http2.Transport
	h2Once    sync.Once
}

func newChromeTLSTransport(extraCAs *x509.CertPool) *chromeTLSTransport {
	t := &chromeTLSTransport{
		extraCAs: extraCAs,
	}
	// HTTP/1.1 transport with utls DialTLSContext for non-h2 connections.
	t.h1 = &http.Transport{
		DialTLSContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			return t.dialTLS(network, addr)
		},
	}
	return t
}

func (t *chromeTLSTransport) getH2() *http2.Transport {
	t.h2Once.Do(func() {
		t.h2 = &http2.Transport{}
	})
	return t.h2
}

func (t *chromeTLSTransport) dialTLS(network, addr string) (*utls.UConn, error) {
	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		conn.Close()
		return nil, err
	}

	tlsConfig := &utls.Config{
		ServerName: host,
	}
	if t.extraCAs != nil {
		tlsConfig.RootCAs = t.extraCAs
	}

	uconn := utls.UClient(conn, tlsConfig, utls.HelloChrome_Auto)
	if err := uconn.Handshake(); err != nil {
		uconn.Close()
		return nil, err
	}

	return uconn, nil
}

func (t *chromeTLSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		// Plain HTTP - use default transport.
		return http.DefaultTransport.RoundTrip(req)
	}

	// Dial TLS to discover the negotiated protocol.
	addr := req.URL.Host
	if !hasPort(addr) {
		addr = addr + ":443"
	}

	uconn, err := t.dialTLS("tcp", addr)
	if err != nil {
		return nil, err
	}

	alpn := uconn.ConnectionState().NegotiatedProtocol

	if alpn == "h2" {
		// Use http2.Transport with the already-established connection.
		h2 := t.getH2()
		h2Conn, err := h2.NewClientConn(uconn)
		if err != nil {
			uconn.Close()
			return nil, fmt.Errorf("h2 client conn: %w", err)
		}
		return h2Conn.RoundTrip(req)
	}

	// HTTP/1.1: put the connection back into the h1 transport's pool
	// by using a single-use dialer.
	resp, err := singleConnRoundTrip(uconn, req)
	if err != nil {
		uconn.Close()
		return nil, err
	}
	return resp, nil
}

// singleConnRoundTrip performs an HTTP/1.1 request over an existing TLS connection.
func singleConnRoundTrip(conn net.Conn, req *http.Request) (*http.Response, error) {
	t := &http.Transport{
		DisableKeepAlives: true,
		DialTLSContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return conn, nil
		},
	}
	return t.RoundTrip(req)
}

func hasPort(s string) bool {
	_, _, err := net.SplitHostPort(s)
	return err == nil
}
