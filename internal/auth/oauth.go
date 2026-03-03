package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultSlackBaseURL = "https://slack.com"

	// Scopes requested for the bot token.
	botScopes = "channels:read,groups:read,im:read,mpim:read,users:read,reactions:read"

	// Scopes requested for the user token.
	userScopes = "search:read"
)

// Login runs the OAuth flow: starts a local server, opens the browser,
// waits for the callback, exchanges the code for tokens, and returns
// the workspace credentials.
func Login(ctx context.Context, clientID, clientSecret string) (*WorkspaceCredentials, error) {
	return login(ctx, defaultSlackBaseURL, clientID, clientSecret)
}

func login(ctx context.Context, baseURL, clientID, clientSecret string) (*WorkspaceCredentials, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting callback server: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state, err := randomState()
	if err != nil {
		return nil, err
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.Handle("/callback", callbackHandler(state, codeCh, errCh))
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	authURL := buildAuthorizeURL(baseURL, clientID, redirectURI, state)
	openBrowser(authURL)
	fmt.Printf("Opening browser for authentication...\nIf it doesn't open, visit:\n%s\n", authURL)

	// Wait for the callback or timeout.
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return exchangeToken(ctx, baseURL, clientID, clientSecret, code, redirectURI)
}

// callbackHandler returns an HTTP handler that validates the state parameter
// and extracts the authorization code from the OAuth callback. Only the first
// request is processed; subsequent requests get a simple acknowledgment.
func callbackHandler(expectedState string, codeCh chan<- string, errCh chan<- error) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			if r.URL.Query().Get("state") != expectedState {
				errCh <- fmt.Errorf("invalid state parameter (possible CSRF)")
				http.Error(w, "invalid state", http.StatusBadRequest)
				return
			}

			if errMsg := r.URL.Query().Get("error"); errMsg != "" {
				desc := r.URL.Query().Get("error_description")
				errCh <- fmt.Errorf("oauth error: %s (%s)", errMsg, desc)
				fmt.Fprint(w, "Authentication failed. You can close this tab.")
				return
			}

			code := r.URL.Query().Get("code")
			if code == "" {
				errCh <- fmt.Errorf("no authorization code in callback")
				http.Error(w, "missing code", http.StatusBadRequest)
				return
			}

			codeCh <- code
			fmt.Fprint(w, "Authentication successful! You can close this tab.")
		})
	})
}

func buildAuthorizeURL(baseURL, clientID, redirectURI, state string) string {
	v := url.Values{}
	v.Set("client_id", clientID)
	v.Set("scope", botScopes)
	v.Set("user_scope", userScopes)
	v.Set("redirect_uri", redirectURI)
	v.Set("state", state)
	return baseURL + "/oauth/v2/authorize?" + v.Encode()
}

// exchangeToken exchanges an authorization code for bot and user tokens,
// then calls auth.test to get workspace metadata.
func exchangeToken(ctx context.Context, baseURL, clientID, clientSecret, code, redirectURI string) (*WorkspaceCredentials, error) {
	// Exchange the code for tokens.
	tokenResp, err := postForm(ctx, baseURL+"/api/oauth.v2.access", url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	})
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	if ok, _ := tokenResp["ok"].(bool); !ok {
		errMsg, _ := tokenResp["error"].(string)
		return nil, fmt.Errorf("token exchange failed: %s", errMsg)
	}

	ws := &WorkspaceCredentials{}
	ws.BotToken, _ = tokenResp["access_token"].(string)

	if authedUser, ok := tokenResp["authed_user"].(map[string]any); ok {
		ws.UserToken, _ = authedUser["access_token"].(string)
		ws.UserID, _ = authedUser["id"].(string)
	}

	if team, ok := tokenResp["team"].(map[string]any); ok {
		ws.TeamID, _ = team["id"].(string)
		ws.TeamName, _ = team["name"].(string)
	}

	return ws, nil
}

func postForm(ctx context.Context, endpoint string, data url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}
