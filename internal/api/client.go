package api

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// Client wraps slack-go with configured retry and optional user token.
type Client struct {
	bot        *slack.Client
	user       *slack.Client
	httpClient *http.Client
	apiURL     string
	token      string
}

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	userToken string
	apiURL    string
	cookie    string
	tlsCAs    *x509.CertPool
}

// WithUserToken adds a user token client for APIs that require xoxp- tokens.
func WithUserToken(token string) Option {
	return func(c *clientConfig) { c.userToken = token }
}

// WithAPIURL overrides the Slack API base URL (for testing).
func WithAPIURL(url string) Option {
	return func(c *clientConfig) { c.apiURL = url }
}

// WithCookie sets a cookie value to send with every API request.
// Used for xoxc- token authentication which requires a d cookie.
func WithCookie(cookie string) Option {
	return func(c *clientConfig) { c.cookie = cookie }
}

// WithTLSCAs adds extra root CAs to the TLS transport. For testing only.
func WithTLSCAs(pool *x509.CertPool) Option {
	return func(c *clientConfig) { c.tlsCAs = pool }
}

// NewWithAPIURL is a convenience for creating a test client with a custom API URL.
func NewWithAPIURL(botToken, apiURL string) *Client {
	return New(botToken, WithAPIURL(apiURL))
}

// New creates a Client with the given bot token and options.
func New(botToken string, opts ...Option) *Client {
	cfg := &clientConfig{}
	for _, o := range opts {
		o(cfg)
	}

	var httpCl *http.Client
	if cfg.cookie != "" {
		httpCl = slackHTTPClient(cfg.cookie, cfg.tlsCAs)
	}

	var botOpts []slack.Option
	if cfg.apiURL != "" {
		botOpts = append(botOpts, slack.OptionAPIURL(cfg.apiURL))
	}
	if httpCl != nil {
		botOpts = append(botOpts, slack.OptionHTTPClient(httpCl))
	}

	apiURL := "https://slack.com/api/"
	if cfg.apiURL != "" {
		apiURL = cfg.apiURL
	}

	c := &Client{
		bot:        slack.New(botToken, botOpts...),
		httpClient: httpCl,
		apiURL:     apiURL,
		token:      botToken,
	}

	if cfg.userToken != "" {
		var userOpts []slack.Option
		if cfg.apiURL != "" {
			userOpts = append(userOpts, slack.OptionAPIURL(cfg.apiURL))
		}
		if cfg.cookie != "" {
			userOpts = append(userOpts, slack.OptionHTTPClient(slackHTTPClient(cfg.cookie, cfg.tlsCAs)))
		}
		c.user = slack.New(cfg.userToken, userOpts...)
	}

	return c
}


// Bot returns the bot token Slack client.
func (c *Client) Bot() *slack.Client { return c.bot }

// Token returns the primary token used by this client.
func (c *Client) Token() string { return c.token }

// User returns the user token Slack client, or nil if no user token was provided.
func (c *Client) User() *slack.Client { return c.user }

// PostInternal calls an internal Slack API method via raw HTTP POST with
// a JSON body. The token is included in the request body. Returns the raw
// JSON response body. Returns an error if the response has ok:false.
func (c *Client) PostInternal(ctx context.Context, method string, body map[string]any) (data json.RawMessage, err error) {
	start := time.Now()
	defer func() {
		TracerFrom(ctx).Event("internal_api", map[string]any{
			"endpoint":   method,
			"latency_ms": time.Since(start).Milliseconds(),
			"bytes":      len(data),
			"error":      errString(err),
		})
	}()

	// Clone to avoid mutating the caller's map.
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	out["token"] = c.token

	payload, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		err = fmt.Errorf("marshalling request: %w", marshalErr)
		return nil, err
	}

	url := c.apiURL + method
	req, reqErr := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if reqErr != nil {
		err = fmt.Errorf("creating request: %w", reqErr)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = doErr
		return nil, err
	}
	defer resp.Body.Close()

	body2, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		err = fmt.Errorf("reading response: %w", readErr)
		return nil, err
	}
	data = json.RawMessage(body2)

	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(data, &envelope); unmarshalErr != nil {
		err = fmt.Errorf("parsing response: %w", unmarshalErr)
		return nil, err
	}
	if !envelope.OK {
		err = slack.SlackErrorResponse{Err: envelope.Error}
		return nil, err
	}

	return data, nil
}

// PostInternalForm calls an internal Slack API method via form-encoded POST
// at the default API URL. Used by endpoints that expect
// application/x-www-form-urlencoded.
func (c *Client) PostInternalForm(ctx context.Context, method string, params map[string]string) (data json.RawMessage, err error) {
	start := time.Now()
	defer func() {
		TracerFrom(ctx).Event("internal_api", map[string]any{
			"endpoint":   method,
			"latency_ms": time.Since(start).Milliseconds(),
			"bytes":      len(data),
			"error":      errString(err),
		})
	}()

	form := url.Values{}
	form.Set("token", c.token)
	for k, v := range params {
		form.Set(k, v)
	}

	reqURL := c.apiURL + method
	req, reqErr := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(form.Encode()))
	if reqErr != nil {
		err = fmt.Errorf("creating request: %w", reqErr)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.token)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = doErr
		return nil, err
	}
	defer resp.Body.Close()

	body2, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		err = fmt.Errorf("reading response: %w", readErr)
		return nil, err
	}
	data = json.RawMessage(body2)

	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(data, &envelope); unmarshalErr != nil {
		err = fmt.Errorf("parsing response: %w", unmarshalErr)
		return nil, err
	}
	if !envelope.OK {
		err = slack.SlackErrorResponse{Err: envelope.Error}
		return nil, err
	}

	return data, nil
}

// AuthTestResult contains validated token metadata.
type AuthTestResult struct {
	URL    string
	Team   string
	TeamID string
	User   string
	UserID string
}

// AuthTest validates the bot token by calling auth.test.
func (c *Client) AuthTest(ctx context.Context) (*AuthTestResult, error) {
	resp, err := c.bot.AuthTestContext(ctx)
	if err != nil {
		return nil, err
	}
	return &AuthTestResult{
		URL:    resp.URL,
		Team:   resp.Team,
		TeamID: resp.TeamID,
		User:   resp.User,
		UserID: resp.UserID,
	}, nil
}
