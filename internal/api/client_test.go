package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew(t *testing.T) {
	c := New("xoxb-test")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Bot() == nil {
		t.Fatal("expected non-nil bot client")
	}
}

func TestNew_WithUserToken(t *testing.T) {
	c := New("xoxb-test", WithUserToken("xoxp-test"))
	if c.User() == nil {
		t.Fatal("expected non-nil user client")
	}
}

func TestNew_WithoutUserToken(t *testing.T) {
	c := New("xoxb-test")
	if c.User() != nil {
		t.Error("expected nil user client when no user token provided")
	}
}

func TestAuthTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth.test" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://test-team.slack.com/",
			"team":    "Test Team",
			"team_id": "T123",
			"user":    "testbot",
			"user_id": "U456",
		})
	}))
	defer srv.Close()

	c := New("xoxb-test", WithAPIURL(srv.URL+"/api/"))
	result, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TeamID != "T123" {
		t.Errorf("got team_id=%q, want %q", result.TeamID, "T123")
	}
	if result.UserID != "U456" {
		t.Errorf("got user_id=%q, want %q", result.UserID, "U456")
	}
	if result.User != "testbot" {
		t.Errorf("got user=%q, want %q", result.User, "testbot")
	}
}

func TestNew_WithCookie(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://test.slack.com/",
			"team":    "Test",
			"team_id": "T1",
			"user":    "u",
			"user_id": "U1",
		})
	}))
	defer srv.Close()

	c := New("xoxc-test", WithCookie("xoxd-cookie-value"), WithAPIURL(srv.URL+"/api/"))
	_, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "d=xoxd-cookie-value" {
		t.Errorf("got Cookie header %q, want %q", gotCookie, "d=xoxd-cookie-value")
	}
}

func TestNew_WithCookieOnUserClient(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://test.slack.com/",
			"team":    "Test",
			"team_id": "T1",
			"user":    "u",
			"user_id": "U1",
		})
	}))
	defer srv.Close()

	c := New("xoxc-bot", WithUserToken("xoxc-user"), WithCookie("xoxd-cookie"), WithAPIURL(srv.URL+"/api/"))
	// Call auth.test via the user client path - we'll use Bot() since User() doesn't
	// have AuthTest, but both should have the cookie transport.
	_, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "d=xoxd-cookie" {
		t.Errorf("got Cookie header %q, want %q", gotCookie, "d=xoxd-cookie")
	}
}

func TestNew_UserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "url": "https://test.slack.com/", "team": "T",
			"team_id": "T1", "user": "u", "user_id": "U1",
		})
	}))
	defer srv.Close()

	c := New("xoxc-test", WithCookie("xoxd-cookie"), WithAPIURL(srv.URL+"/api/"))
	_, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != ChromeUserAgent {
		t.Errorf("got User-Agent %q, want %q", gotUA, ChromeUserAgent)
	}
}

func TestAuthTest_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid_auth",
		})
	}))
	defer srv.Close()

	c := New("xoxb-bad-token", WithAPIURL(srv.URL+"/api/"))
	_, err := c.AuthTest(context.Background())
	if err == nil {
		t.Error("expected error for invalid auth")
	}
}
