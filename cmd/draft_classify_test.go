package cmd

import "testing"

func TestClassifyRecipient(t *testing.T) {
	tests := []struct {
		in   string
		want recipientKind
	}{
		{"@alice", recipientUser},
		{"alice@example.com", recipientUser},
		{"U012ABC", recipientUser},
		{"W012ABC", recipientUser},
		{"https://app.slack.com/team/U012ABC", recipientUser},
		{"#general", recipientChannel},
		{"general", recipientChannel},
		{"C012ABC", recipientChannel},
		{"D012ABC", recipientChannel},
		{"G012ABC", recipientChannel},
		{"https://acme.slack.com/archives/C012ABC", recipientChannel},
	}
	for _, tt := range tests {
		if got := classifyRecipient(tt.in); got != tt.want {
			t.Errorf("classifyRecipient(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
