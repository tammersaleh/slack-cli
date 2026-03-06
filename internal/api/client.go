package api

import (
	"context"
	"crypto/x509"

	"github.com/slack-go/slack"
)

// Client wraps slack-go with configured retry and optional user token.
type Client struct {
	bot  *slack.Client
	user *slack.Client
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

	var botOpts []slack.Option
	if cfg.apiURL != "" {
		botOpts = append(botOpts, slack.OptionAPIURL(cfg.apiURL))
	}
	if cfg.cookie != "" {
		botOpts = append(botOpts, slack.OptionHTTPClient(slackHTTPClient(cfg.cookie, cfg.tlsCAs)))
	}

	c := &Client{
		bot: slack.New(botToken, botOpts...),
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

// User returns the user token Slack client, or nil if no user token was provided.
func (c *Client) User() *slack.Client { return c.user }

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
