package slackurl

import "testing"

func TestParse_Valid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Ref
	}{
		{
			name: "message permalink",
			in:   "https://acme.slack.com/archives/C0123ABCD/p1713300000123456",
			want: Ref{Kind: KindMessage, ChannelID: "C0123ABCD", TS: "1713300000.123456"},
		},
		{
			name: "thread reply permalink with thread_ts and cid",
			in:   "https://acme.slack.com/archives/C0123ABCD/p1713300500654321?thread_ts=1713300000.123456&cid=C0123ABCD",
			want: Ref{Kind: KindMessage, ChannelID: "C0123ABCD", TS: "1713300500.654321", ThreadTS: "1713300000.123456"},
		},
		{
			name: "channel archives",
			in:   "https://acme.slack.com/archives/C0123ABCD",
			want: Ref{Kind: KindChannel, ChannelID: "C0123ABCD"},
		},
		{
			name: "channel client workspace",
			in:   "https://app.slack.com/client/T0123ABCD/C0456WXYZ",
			want: Ref{Kind: KindChannel, ChannelID: "C0456WXYZ"},
		},
		{
			name: "channel client enterprise org",
			in:   "https://app.slack.com/client/E0123ABCD/C0456WXYZ",
			want: Ref{Kind: KindChannel, ChannelID: "C0456WXYZ"},
		},
		{
			name: "dm client",
			in:   "https://app.slack.com/client/T0123ABCD/D0456WXYZ",
			want: Ref{Kind: KindChannel, ChannelID: "D0456WXYZ"},
		},
		{
			name: "user team profile",
			in:   "https://acme.slack.com/team/U0123ABCD",
			want: Ref{Kind: KindUser, UserID: "U0123ABCD"},
		},
		{
			name: "user enterprise profile",
			in:   "https://acme.slack.com/user/@W0123ABCD",
			want: Ref{Kind: KindUser, UserID: "W0123ABCD"},
		},
		{
			name: "user client",
			in:   "https://app.slack.com/client/T0123ABCD/U0456WXYZ",
			want: Ref{Kind: KindUser, UserID: "U0456WXYZ"},
		},
		{
			name: "file",
			in:   "https://acme.slack.com/files/U0123ABCD/F0456WXYZ/report.pdf",
			want: Ref{Kind: KindFile, FileID: "F0456WXYZ", UserID: "U0123ABCD"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matched, err := Parse(tt.in)
			if !matched {
				t.Fatalf("matched = false, want true")
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParse_NotMatched(t *testing.T) {
	// Non-URL inputs must fall through (matched=false) so callers can apply
	// their existing ID/name/email handling.
	inputs := []string{
		"",
		"C0123ABCD",
		"#general",
		"general",
		"tammer@example.com",
		"@tammer",
		"1713300000.123456",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			_, matched, err := Parse(in)
			if matched {
				t.Errorf("matched = true, want false")
			}
			if err != nil {
				t.Errorf("err = %v, want nil for non-URL input", err)
			}
		})
	}
}

func TestParse_BadURL(t *testing.T) {
	// http(s)-prefixed inputs that are not usable Slack refs must report
	// matched=true with an error so callers fast-fail instead of paginating.
	inputs := []struct {
		name string
		in   string
	}{
		{"non-slack host", "https://example.com/archives/C0123ABCD"},
		{"unknown route", "https://acme.slack.com/foo/bar"},
		{"archives bad channel prefix", "https://acme.slack.com/archives/X0123ABCD"},
		{"archives no id", "https://acme.slack.com/archives"},
		{"message ts too short", "https://acme.slack.com/archives/C0123ABCD/p123"},
		{"message ts non-numeric", "https://acme.slack.com/archives/C0123ABCD/pabcdefghijklmnop"},
		{"cid mismatch", "https://acme.slack.com/archives/C0123ABCD/p1713300000123456?cid=C9999ZZZZ"},
		{"bad thread_ts", "https://acme.slack.com/archives/C0123ABCD/p1713300000123456?thread_ts=notats"},
		{"client too few segments", "https://app.slack.com/client/T0123ABCD"},
		{"client unsupported id", "https://app.slack.com/client/T0123ABCD/B0456WXYZ"},
		{"files missing file id", "https://acme.slack.com/files/U0123ABCD"},
		{"files bad file id prefix", "https://acme.slack.com/files/U0123ABCD/X0456WXYZ/report.pdf"},
	}
	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			_, matched, err := Parse(tt.in)
			if !matched {
				t.Errorf("matched = false, want true (input is URL-shaped)")
			}
			if err == nil {
				t.Errorf("err = nil, want error for bad Slack URL")
			}
		})
	}
}
