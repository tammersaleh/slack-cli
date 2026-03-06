package api

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"

	utls "github.com/refraction-networking/utls"
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
			base:   chromeTLSTransport(extraCAs),
		},
	}
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

// chromeTLSTransport returns an http.Transport that uses utls to mimic
// Chrome's TLS fingerprint. Falls back to standard dialing for non-TLS.
func chromeTLSTransport(extraCAs *x509.CertPool) *http.Transport {
	return &http.Transport{
		DialTLSContext: func(_ context.Context, network, addr string) (net.Conn, error) {
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
			if extraCAs != nil {
				tlsConfig.RootCAs = extraCAs
			}

			uconn := utls.UClient(conn, tlsConfig, utls.HelloChrome_Auto)
			if err := uconn.Handshake(); err != nil {
				uconn.Close()
				return nil, err
			}

			return uconn, nil
		},
	}
}
