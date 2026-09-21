package cmd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// fakeUserResolver resolves names case-insensitively from a fixed map and
// records every lookup it was asked for.
type fakeUserResolver struct {
	users map[string]string
	asked []string
}

func (f *fakeUserResolver) ResolveUser(_ context.Context, input string) (string, error) {
	f.asked = append(f.asked, input)
	if id, ok := f.users[strings.ToLower(input)]; ok {
		return id, nil
	}
	return "", errors.New("cannot resolve user")
}

func newFakeUserResolver() *fakeUserResolver {
	return &fakeUserResolver{users: map[string]string{
		"alice":       "U01XYZ",
		"alice adams": "U01XYZ",
		"bob":         "U02MGR",
		"bob brown":   "U02MGR",
		"u03abc":      "U03ABC",
	}}
}

func TestSplitQueryTokens(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"deploy failed", []string{"deploy", "failed"}},
		{"  deploy   failed  ", []string{"deploy", "failed"}},
		{`"deploy failed" in:#general`, []string{`"deploy failed"`, "in:#general"}},
		{`from:"Alice Adams" skypilot`, []string{`from:"Alice Adams"`, "skypilot"}},
		{`from:@"Alice Adams"`, []string{`from:@"Alice Adams"`}},
		{`"unterminated quote`, []string{`"unterminated quote`}},
		{"tab\tsplit", []string{"tab", "split"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := splitQueryTokens(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitQueryTokens(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRewriteUserModifiers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no modifiers", "deploy failed", "deploy failed"},
		{"handle", "skypilot from:@alice", "skypilot from:<@U01XYZ>"},
		{"real name eats following words", "skypilot from:@Alice Adams", "skypilot from:<@U01XYZ>"},
		{"real name stops at next modifier", "skypilot from:@Alice Adams in:#general", "skypilot from:<@U01XYZ> in:#general"},
		{"longest resolving prefix wins", "from:@Alice Adams skypilot", "from:<@U01XYZ> skypilot"},
		{"handle followed by free text", "from:@alice skypilot", "from:<@U01XYZ> skypilot"},
		{"quoted with at inside", `from:"@Alice Adams" skypilot`, "from:<@U01XYZ> skypilot"},
		{"quoted with at outside", `from:@"Alice Adams" skypilot`, "from:<@U01XYZ> skypilot"},
		{"quoted without at", `from:"Alice Adams" skypilot`, "from:<@U01XYZ> skypilot"},
		{"quoted to without at", `to:"Bob Brown"`, "to:<@U02MGR>"},
		{"quoted in without at is a channel", `in:"Alice Adams"`, `in:"Alice Adams"`},
		{"to modifier", "to:@bob", "to:<@U02MGR>"},
		{"in modifier with user", "in:@Bob Brown x", "in:<@U02MGR> x"},
		{"in modifier with channel untouched", "in:#general in:general", "in:#general in:general"},
		{"negated modifier keeps dash", "-from:@alice x", "-from:<@U01XYZ> x"},
		{"uppercase key", "FROM:@alice", "FROM:<@U01XYZ>"},
		{"bare id rewritten to mention", "from:@U03ABC", "from:<@U03ABC>"},
		{"mention form untouched", "from:<@U01XYZ> x", "from:<@U01XYZ> x"},
		{"other modifiers untouched", "has:link after:2026-01-01 is:thread", "has:link after:2026-01-01 is:thread"},
		{"two users", "from:@Alice Adams to:@Bob Brown", "from:<@U01XYZ> to:<@U02MGR>"},
		{"span stops at quoted phrase", `from:@alice "exact phrase"`, `from:<@U01XYZ> "exact phrase"`},
		{"lone at untouched", "from:@ x", "from:@ x"},
		{"empty quotes untouched", `from:"" x`, `from:"" x`},
		{"lone quote untouched", `from:" x`, `from:" x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriteUserModifiers(context.Background(), newFakeUserResolver(), tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("rewriteUserModifiers(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRewriteUserModifiers_Unresolved(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantInput string
	}{
		{"unknown handle", "skypilot from:@carol", "@carol"},
		{"unknown multi-word reports whole span", "skypilot from:@Carol Chen in:#general", "@Carol Chen"},
		{"unknown quoted", `from:"@Carol Chen"`, "@Carol Chen"},
		{"unknown quoted without at", `from:"Carol Chen"`, "@Carol Chen"},
		{"dangling quote after at is a bad name, not a panic", `from:@" x`, `@" x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rewriteUserModifiers(context.Background(), newFakeUserResolver(), tt.in)
			var oe *output.Error
			if !errors.As(err, &oe) {
				t.Fatalf("expected *output.Error, got %v", err)
			}
			if oe.Err != "user_not_found" {
				t.Errorf("Err = %q, want user_not_found", oe.Err)
			}
			if oe.Input != tt.wantInput {
				t.Errorf("Input = %q, want %q", oe.Input, tt.wantInput)
			}
			if oe.Code != output.ExitGeneral {
				t.Errorf("Code = %d, want %d", oe.Code, output.ExitGeneral)
			}
			if !strings.Contains(oe.Hint, "<@U") {
				t.Errorf("hint should mention the <@Uxxx> form, got %q", oe.Hint)
			}
		})
	}
}

func TestRewriteUserModifiers_LongestPrefixLookupOrder(t *testing.T) {
	r := newFakeUserResolver()
	if _, err := rewriteUserModifiers(context.Background(), r, "from:@Alice Adams skypilot"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Alice Adams skypilot", "Alice Adams"}
	if !reflect.DeepEqual(r.asked, want) {
		t.Errorf("lookups = %#v, want %#v", r.asked, want)
	}
}
