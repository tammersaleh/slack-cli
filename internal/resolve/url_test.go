package resolve

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/api"
)

// noAPIClient returns a client whose every request fails the test, so a
// resolution that should be answered from the URL alone proves it made no
// network call.
func noAPIClient(t *testing.T) *api.Client {
	t.Helper()
	return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected API call: %s", r.URL.Path)
	}))
}

func TestResolveChannel_URL(t *testing.T) {
	r := NewResolver(noAPIClient(t), "", "")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"channel archives", "https://acme.slack.com/archives/C0123ABCD", "C0123ABCD"},
		{"client link", "https://app.slack.com/client/T0123ABCD/C0456WXYZ", "C0456WXYZ"},
		{"message permalink (ts ignored)", "https://acme.slack.com/archives/C0123ABCD/p1713300000123456", "C0123ABCD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveChannel(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveChannel_WrongKindOrBadURL(t *testing.T) {
	r := NewResolver(noAPIClient(t), "", "")

	inputs := []string{
		"https://acme.slack.com/team/U0123ABCD",            // user URL
		"https://acme.slack.com/files/U0123ABCD/F0456WXYZ/x", // file URL
		"https://acme.slack.com/archives/X0123ABCD",        // malformed
		"https://example.com/archives/C0123ABCD",           // non-slack host
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			_, err := r.ResolveChannel(context.Background(), in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrBadURL) {
				t.Errorf("error %v is not ErrBadURL", err)
			}
		})
	}
}

func TestResolveUser_URL(t *testing.T) {
	r := NewResolver(noAPIClient(t), "", "")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"team profile", "https://acme.slack.com/team/U0123ABCD", "U0123ABCD"},
		{"enterprise profile", "https://acme.slack.com/user/@W0123ABCD", "W0123ABCD"},
		{"client link", "https://app.slack.com/client/T0123ABCD/U0456WXYZ", "U0456WXYZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveUser(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveUser_WrongKindOrBadURL(t *testing.T) {
	// A file URL contains a user id, but it identifies a file - resolving it as
	// a user must fail fast without bulk-loading the workspace.
	r := NewResolver(noAPIClient(t), "", "")

	inputs := []string{
		"https://acme.slack.com/files/U0123ABCD/F0456WXYZ/x", // file URL (has a U!)
		"https://acme.slack.com/archives/C0123ABCD",          // channel URL
		"https://acme.slack.com/archives/C0123ABCD/p1713300000123456", // message URL
		"https://example.com/team/U0123ABCD",                 // non-slack host
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			_, err := r.ResolveUser(context.Background(), in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrBadURL) {
				t.Errorf("error %v is not ErrBadURL", err)
			}
		})
	}
}
