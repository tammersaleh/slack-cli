package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallbackHandler_Success(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	handler := callbackHandler("test-state", codeCh, errCh)

	req := httptest.NewRequest("GET", "/callback?code=test-auth-code&state=test-state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
	}

	select {
	case code := <-codeCh:
		if code != "test-auth-code" {
			t.Errorf("got code=%q, want %q", code, "test-auth-code")
		}
	default:
		t.Error("expected code on channel")
	}
}

func TestCallbackHandler_InvalidState(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	handler := callbackHandler("expected-state", codeCh, errCh)

	req := httptest.NewRequest("GET", "/callback?code=test-auth-code&state=wrong-state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusBadRequest)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error")
		}
		if !strings.Contains(err.Error(), "CSRF") {
			t.Errorf("expected CSRF error, got: %v", err)
		}
	default:
		t.Error("expected error on channel")
	}
}

func TestCallbackHandler_Error(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	handler := callbackHandler("test-state", codeCh, errCh)

	req := httptest.NewRequest("GET", "/callback?error=access_denied&error_description=user+denied&state=test-state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error")
		}
	default:
		t.Error("expected error on channel")
	}
}

func TestCallbackHandler_MissingCode(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	handler := callbackHandler("test-state", codeCh, errCh)

	req := httptest.NewRequest("GET", "/callback?state=test-state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error")
		}
	default:
		t.Error("expected error on channel")
	}
}

func TestExchangeToken(t *testing.T) {
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth.v2.access" {
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.FormValue("code") != "test-code" {
			t.Errorf("got code=%q, want %q", r.FormValue("code"), "test-code")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"access_token": "xoxb-bot-token",
			"authed_user": map[string]any{
				"id":           "U123",
				"access_token": "xoxp-user-token",
			},
			"team": map[string]any{
				"id":   "T456",
				"name": "Test Team",
			},
		})
	}))
	defer slackAPI.Close()

	ws, err := exchangeToken(context.Background(), slackAPI.URL, "test-client-id", "test-client-secret", "test-code", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	if ws.BotToken != "xoxb-bot-token" {
		t.Errorf("got bot_token=%q, want %q", ws.BotToken, "xoxb-bot-token")
	}
	if ws.UserToken != "xoxp-user-token" {
		t.Errorf("got user_token=%q, want %q", ws.UserToken, "xoxp-user-token")
	}
	if ws.TeamID != "T456" {
		t.Errorf("got team_id=%q, want %q", ws.TeamID, "T456")
	}
	if ws.TeamName != "Test Team" {
		t.Errorf("got team_name=%q, want %q", ws.TeamName, "Test Team")
	}
	if ws.UserID != "U123" {
		t.Errorf("got user_id=%q, want %q", ws.UserID, "U123")
	}
}

func TestExchangeToken_APIError(t *testing.T) {
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid_code",
		})
	}))
	defer slackAPI.Close()

	_, err := exchangeToken(context.Background(), slackAPI.URL, "id", "secret", "bad-code", "http://localhost/callback")
	if err == nil {
		t.Error("expected error for API failure")
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	url := buildAuthorizeURL("https://slack.com", "CLIENT123", "http://localhost:9999/callback", "test-state")
	if url == "" {
		t.Fatal("expected non-empty URL")
	}

	if !strings.Contains(url, "client_id=CLIENT123") {
		t.Errorf("URL missing client_id: %s", url)
	}
	if !strings.Contains(url, "state=test-state") {
		t.Errorf("URL missing state: %s", url)
	}
}
