package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// SlackDesktopUserAgent is the user-agent string used for all API requests
// when cookie-based authentication is active. Matches the Slack Desktop
// client (Electron-based; "SSB" = Super Short Browser). Slack's server
// stamps draft `last_updated_client` based on this UA, so mirroring the
// Desktop UA keeps the stamp consistent with what Desktop writes itself.
const SlackDesktopUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Slack/4.48.100 Chrome/144.0.0.0 Electron/40.6.0 Safari/537.36 Slack_SSB/4.48.100"

// slackHTTPClient returns an *http.Client that injects a d cookie,
// Slack Desktop user-agent, and Chrome TLS fingerprint on every request.
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

// slackTransport injects the Slack d cookie and Slack Desktop user-agent into every request.
type slackTransport struct {
	cookie string
	base   http.RoundTripper
}

func (t *slackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Cookie", "d="+t.cookie)
	req.Header.Set("User-Agent", SlackDesktopUserAgent)
	return t.base.RoundTrip(req)
}

// chromeTLSTransport dials with Chrome's TLS fingerprint and delegates the
// HTTP layer to pooled transports. Both h2 and h1 transports are pre-wired
// with DialTLSContext hooks that return utls-fingerprinted connections, so
// each pools per-host and reuses conns across requests. Slack APIs negotiate
// h2 over ALPN; we try h2 first and fall back to h1 only when h2 refuses.
type chromeTLSTransport struct {
	extraCAs *x509.CertPool
	h1       *http.Transport
	h2       *http2.Transport
}

// errHTTP1Negotiated signals that the TLS handshake negotiated http/1.1
// via ALPN, so the caller should drop down to the h1 transport rather than
// try to speak h2 over the conn.
var errHTTP1Negotiated = errors.New("http2: peer negotiated http/1.1 via ALPN")

func newChromeTLSTransport(extraCAs *x509.CertPool) *chromeTLSTransport {
	t := &chromeTLSTransport{
		extraCAs: extraCAs,
	}
	// h2 Transport with utls dialer - pools ClientConns per host internally.
	// If the server picks http/1.1 via ALPN we reject the conn here, so
	// http2 never tries to frame a request over it; the caller falls back
	// to h1.
	t.h2 = &http2.Transport{
		DialTLSContext: func(_ context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			conn, err := t.dialTLS(network, addr)
			if err != nil {
				return nil, err
			}
			if conn.ConnectionState().NegotiatedProtocol != "h2" {
				conn.Close()
				return nil, errHTTP1Negotiated
			}
			return conn, nil
		},
	}
	// h1 fallback for hosts that don't negotiate h2.
	t.h1 = &http.Transport{
		DialTLSContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			return t.dialTLS(network, addr)
		},
	}
	return t
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
		return http.DefaultTransport.RoundTrip(req)
	}

	// Prefer h2: the transport pools ClientConns per host, so repeated
	// requests to slack.com reuse the same HTTP/2 multiplexed connection.
	resp, err := t.h2.RoundTrip(req)
	if err == nil {
		return resp, nil
	}
	// Fall back to h1 only when the server negotiated http/1.1 via ALPN.
	// Any other error - timeout, DNS, TLS, h2 application error - propagates
	// as-is; retrying over h1 would mask real failures.
	if errors.Is(err, errHTTP1Negotiated) {
		return t.h1.RoundTrip(req)
	}
	return nil, err
}
