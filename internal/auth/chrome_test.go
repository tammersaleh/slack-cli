package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateChromeCredentials_Success(t *testing.T) {
	var gotAuth, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://acme.slack.com/",
			"team":    "Acme Corp",
			"team_id": "T01ABC",
			"user":    "tammer",
			"user_id": "U01XYZ",
		})
	}))
	defer srv.Close()

	ws, err := validateChromeCredentials(context.Background(), "xoxc-test-token", "xoxd-test-cookie", srv.URL+"/api/auth.test")
	if err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer xoxc-test-token" {
		t.Errorf("expected Authorization %q, got %q", "Bearer xoxc-test-token", gotAuth)
	}
	if gotCookie != "d=xoxd-test-cookie" {
		t.Errorf("expected Cookie %q, got %q", "d=xoxd-test-cookie", gotCookie)
	}
	if ws.BotToken != "xoxc-test-token" {
		t.Errorf("expected bot_token %q, got %q", "xoxc-test-token", ws.BotToken)
	}
	if ws.TeamID != "T01ABC" {
		t.Errorf("expected team_id %q, got %q", "T01ABC", ws.TeamID)
	}
	if ws.TeamName != "Acme Corp" {
		t.Errorf("expected team_name %q, got %q", "Acme Corp", ws.TeamName)
	}
	if ws.UserID != "U01XYZ" {
		t.Errorf("expected user_id %q, got %q", "U01XYZ", ws.UserID)
	}
}

func TestValidateChromeCredentials_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid_auth",
		})
	}))
	defer srv.Close()

	_, err := validateChromeCredentials(context.Background(), "xoxc-bad", "xoxd-bad", srv.URL+"/api/auth.test")
	if err == nil {
		t.Fatal("expected error for invalid auth")
	}
}
