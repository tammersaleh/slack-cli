package api

import (
	"context"
	"crypto/x509"
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

func TestNew_WithCookieOverTLS(t *testing.T) {
	var gotCookie, gotUA string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUA = r.Header.Get("User-Agent")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "url": "https://test.slack.com/", "team": "T",
			"team_id": "T1", "user": "u", "user_id": "U1",
		})
	}))
	defer srv.Close()

	// Trust the httptest server's self-signed CA.
	caPool := x509.NewCertPool()
	caPool.AddCert(srv.Certificate())

	c := New("xoxc-test", WithCookie("xoxd-tls-cookie"), WithTLSCAs(caPool), WithAPIURL(srv.URL+"/api/"))
	_, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "d=xoxd-tls-cookie" {
		t.Errorf("got Cookie %q, want %q", gotCookie, "d=xoxd-tls-cookie")
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

func TestPostInternal_Success(t *testing.T) {
	var gotContentType, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": "test-value",
		})
	}))
	defer srv.Close()

	c := New("xoxc-test-token", WithAPIURL(srv.URL+"/api/"))
	data, err := c.PostInternal(context.Background(), "saved.list", map[string]any{
		"count": 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotContentType != "application/json; charset=utf-8" {
		t.Errorf("expected JSON content-type, got %q", gotContentType)
	}
	if gotAuth != "Bearer xoxc-test-token" {
		t.Errorf("expected Bearer auth, got %q", gotAuth)
	}
	if gotBody["token"] != "xoxc-test-token" {
		t.Errorf("expected token in body, got %v", gotBody["token"])
	}
	if gotBody["count"] != float64(20) {
		t.Errorf("expected count=20 in body, got %v", gotBody["count"])
	}

	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["data"] != "test-value" {
		t.Errorf("expected data='test-value', got %v", resp["data"])
	}
}

func TestPostInternal_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "not_authed",
		})
	}))
	defer srv.Close()

	c := New("xoxc-test", WithAPIURL(srv.URL+"/api/"))
	_, err := c.PostInternal(context.Background(), "saved.list", map[string]any{})
	if err == nil {
		t.Fatal("expected error for ok:false response")
	}
}

func TestPostInternal_DoesNotMutateInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := New("xoxc-test", WithAPIURL(srv.URL+"/api/"))
	body := map[string]any{"count": 10}
	_, err := c.PostInternal(context.Background(), "saved.list", body)
	if err != nil {
		t.Fatal(err)
	}

	if _, hasToken := body["token"]; hasToken {
		t.Error("PostInternal should not mutate the input map")
	}
}
