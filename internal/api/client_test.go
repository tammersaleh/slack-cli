package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/slack-go/slack"
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
	var gotCookie, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotToken = r.PostFormValue("token")
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

	// Drive the request through User(), not AuthTest() - AuthTest() goes via the
	// bot client, so asserting on it only re-tests TestNew_WithCookie and says
	// nothing about how the user client was built.
	_, err := c.User().AuthTestContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "d=xoxd-cookie" {
		t.Errorf("got Cookie header %q, want %q", gotCookie, "d=xoxd-cookie")
	}
	// Proves the request really came from the user client, and that the user
	// client got WithAPIURL - without it this would have gone to slack.com.
	if gotToken != "xoxc-user" {
		t.Errorf("got token %q, want %q", gotToken, "xoxc-user")
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
	if gotUA != SlackDesktopUserAgent {
		t.Errorf("got User-Agent %q, want %q", gotUA, SlackDesktopUserAgent)
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
	if gotUA != SlackDesktopUserAgent {
		t.Errorf("got User-Agent %q, want %q", gotUA, SlackDesktopUserAgent)
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

func TestPostInternalForm_Success(t *testing.T) {
	var gotContentType, gotAuth, gotToken, gotName string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		_ = r.ParseForm()
		gotToken = r.FormValue("token")
		gotName = r.FormValue("name")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
			"channel_section_id": "S01ABC",
		})
	}))
	defer srv.Close()

	c := New("xoxc-test-token", WithAPIURL(srv.URL+"/api/"))
	data, err := c.PostInternalForm(context.Background(), "users.channelSections.create", map[string]string{
		"name": "Archive",
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("expected form content-type, got %q", gotContentType)
	}
	if gotAuth != "Bearer xoxc-test-token" {
		t.Errorf("expected Bearer auth, got %q", gotAuth)
	}
	if gotToken != "xoxc-test-token" {
		t.Errorf("expected token in form, got %q", gotToken)
	}
	if gotName != "Archive" {
		t.Errorf("expected name='Archive' in form, got %q", gotName)
	}

	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["channel_section_id"] != "S01ABC" {
		t.Errorf("expected channel_section_id='S01ABC', got %v", resp["channel_section_id"])
	}
}

func TestPostInternalForm_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "not_authed",
		})
	}))
	defer srv.Close()

	c := New("xoxc-test", WithAPIURL(srv.URL+"/api/"))
	_, err := c.PostInternalForm(context.Background(), "users.channelSections.list", nil)
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

// Both internal POST paths must hand response_metadata.messages to the
// caller on the typed error: for the lists.* methods those strings are the
// only argument documentation that exists.
func TestPostInternal_ErrorCarriesResponseMetadataMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments","response_metadata":{"messages":["[ERROR] missing required field: name"]}}`))
	}))
	defer srv.Close()
	c := New("xoxc-test", WithAPIURL(srv.URL+"/api/"))

	for name, call := range map[string]func() error{
		"PostInternal": func() error {
			_, err := c.PostInternal(context.Background(), "lists.create", map[string]any{})
			return err
		},
		"PostInternalForm": func() error {
			_, err := c.PostInternalForm(context.Background(), "lists.create", nil)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			var slackErr slack.SlackErrorResponse
			if !errors.As(err, &slackErr) {
				t.Fatalf("expected SlackErrorResponse, got %T: %v", err, err)
			}
			if slackErr.Err != "invalid_arguments" || len(slackErr.ResponseMetadata.Messages) != 1 || slackErr.ResponseMetadata.Messages[0] != "[ERROR] missing required field: name" {
				t.Errorf("error = %+v", slackErr)
			}
		})
	}
}

// postInternalBoth runs one scenario through both internal helpers, which
// share their response handling.
func postInternalBoth(t *testing.T, handler http.HandlerFunc, check func(t *testing.T, data json.RawMessage, err error)) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	c := New("xoxc-test", WithAPIURL(srv.URL+"/api/"))

	t.Run("json", func(t *testing.T) {
		data, err := c.PostInternal(context.Background(), "saved.list", map[string]any{})
		check(t, data, err)
	})
	t.Run("form", func(t *testing.T) {
		data, err := c.PostInternalForm(context.Background(), "users.channelSections.list", map[string]string{})
		check(t, data, err)
	})
}

// A rate-limited internal call must come back as *slack.RateLimitedError in
// every wire shape, because that type is what the shared retry loop and the
// exit-3 classification key on. Before this the helpers ignored the HTTP
// status and returned SlackErrorResponse{"ratelimited"}: no retry, exit 1.
func TestPostInternal_RateLimited(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		retryAfter string
		body       string
		wantWait   time.Duration
	}{
		{"429 with Retry-After", http.StatusTooManyRequests, "30", `{"ok":false,"error":"ratelimited"}`, 30 * time.Second},
		// slack-go drops a 429 without the header to a plain status error.
		// Here it is still a rate limit; the retry loop floors the wait.
		{"429 without Retry-After", http.StatusTooManyRequests, "", `{"ok":false,"error":"ratelimited"}`, 0},
		{"429 with a non-JSON body", http.StatusTooManyRequests, "2", `<html>slow down</html>`, 2 * time.Second},
		{"429 with an unparseable Retry-After", http.StatusTooManyRequests, "soon", ``, 0},
		{"200 with a ratelimited envelope", http.StatusOK, "", `{"ok":false,"error":"ratelimited"}`, 0},
		{"200 with a ratelimited envelope and Retry-After", http.StatusOK, "5", `{"ok":false,"error":"ratelimited"}`, 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			postInternalBoth(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}, func(t *testing.T, data json.RawMessage, err error) {
				var rlErr *slack.RateLimitedError
				if !errors.As(err, &rlErr) {
					t.Fatalf("expected *slack.RateLimitedError, got %T: %v", err, err)
				}
				if rlErr.RetryAfter != tt.wantWait {
					t.Errorf("RetryAfter = %v, want %v", rlErr.RetryAfter, tt.wantWait)
				}
				if data != nil {
					t.Errorf("expected no data on error, got %s", data)
				}
			})
		})
	}
}

// A non-200 whose body is not an envelope is an HTTP failure, not a parse
// failure: the parse_error hint (proxy, captive portal) is wrong for a 502.
func TestPostInternal_NonOKStatusWithoutEnvelope(t *testing.T) {
	postInternalBoth(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}, func(t *testing.T, _ json.RawMessage, err error) {
		var statusErr slack.StatusCodeError
		if !errors.As(err, &statusErr) || statusErr.Code != http.StatusBadGateway {
			t.Fatalf("expected slack.StatusCodeError 502, got %T: %v", err, err)
		}
	})
}

// A non-200 that does carry an envelope keeps Slack's own error code.
func TestPostInternal_NonOKStatusWithEnvelope(t *testing.T) {
	postInternalBoth(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"error":"team_is_restricted"}`))
	}, func(t *testing.T, _ json.RawMessage, err error) {
		if code, ok := SlackErrorCode(err); !ok || code != "team_is_restricted" {
			t.Fatalf("expected team_is_restricted, got %T: %v", err, err)
		}
	})
}

// A 200 that is not JSON stays a parse error.
func TestPostInternal_OKStatusNonJSON(t *testing.T) {
	postInternalBoth(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>login</html>"))
	}, func(t *testing.T, _ json.RawMessage, err error) {
		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Fatalf("expected a JSON syntax error, got %T: %v", err, err)
		}
	})
}
