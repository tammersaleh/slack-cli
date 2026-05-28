package cmd

import "testing"

func TestParseMessageRefs_ChannelMode(t *testing.T) {
	refs, err := parseMessageRefs([]string{"C01ABC", "1709251200.000100", "1709251300.000200"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	for i, want := range []string{"1709251200.000100", "1709251300.000200"} {
		if refs[i].channel != "C01ABC" {
			t.Errorf("refs[%d].channel = %q, want C01ABC", i, refs[i].channel)
		}
		if refs[i].ts != want {
			t.Errorf("refs[%d].ts = %q, want %q", i, refs[i].ts, want)
		}
		if refs[i].input != want {
			t.Errorf("refs[%d].input = %q, want %q (bare ts echoes itself)", i, refs[i].input, want)
		}
	}
}

func TestParseMessageRefs_ChannelModeWithChannelURL(t *testing.T) {
	// A channel URL in the channel slot stays raw; ResolveChannel handles it.
	chURL := "https://acme.slack.com/archives/C01ABC"
	refs, err := parseMessageRefs([]string{chURL, "1709251200.000100"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 || refs[0].channel != chURL || refs[0].ts != "1709251200.000100" {
		t.Fatalf("got %+v, want one ref {channel:%q ts:1709251200.000100}", refs, chURL)
	}
}

func TestParseMessageRefs_URLMode(t *testing.T) {
	refs, err := parseMessageRefs([]string{"https://acme.slack.com/archives/C01ABC/p1709251200000100"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1", len(refs))
	}
	if refs[0].channel != "C01ABC" || refs[0].ts != "1709251200.000100" {
		t.Errorf("got {channel:%q ts:%q}, want {C01ABC 1709251200.000100}", refs[0].channel, refs[0].ts)
	}
}

func TestParseMessageRefs_URLModeCrossChannel(t *testing.T) {
	refs, err := parseMessageRefs([]string{
		"https://acme.slack.com/archives/C01ABC/p1709251200000100",
		"https://acme.slack.com/archives/C02DEF/p1709251300000200",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	if refs[0].channel != "C01ABC" || refs[1].channel != "C02DEF" {
		t.Errorf("channels = %q,%q, want C01ABC,C02DEF", refs[0].channel, refs[1].channel)
	}
}

func TestParseMessageRefs_Errors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"empty", nil},
		{"channel mode missing ts", []string{"C01ABC"}},
		{"url then bare ts (mixing)", []string{"https://acme.slack.com/archives/C01ABC/p1709251200000100", "1709251300.000200"}},
		{"channel then url (mixing)", []string{"C01ABC", "https://acme.slack.com/archives/C02DEF/p1709251300000200"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseMessageRefs(tt.args); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestParseThreadRef_ChannelMode(t *testing.T) {
	ref, err := parseThreadRef([]string{"C01ABC", "1709251200.000100"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.channel != "C01ABC" || ref.ts != "1709251200.000100" {
		t.Errorf("got {channel:%q ts:%q}, want {C01ABC 1709251200.000100}", ref.channel, ref.ts)
	}
}

func TestParseThreadRef_URLUsesParentThreadTS(t *testing.T) {
	// A link to a reply carries thread_ts (the parent). Thread list must
	// resolve to the parent thread, not the reply.
	url := "https://acme.slack.com/archives/C01ABC/p1709251300000200?thread_ts=1709251200.000100&cid=C01ABC"
	ref, err := parseThreadRef([]string{url})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.channel != "C01ABC" || ref.ts != "1709251200.000100" {
		t.Errorf("got {channel:%q ts:%q}, want parent {C01ABC 1709251200.000100}", ref.channel, ref.ts)
	}
}

func TestParseThreadRef_Errors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"empty", nil},
		{"channel mode missing ts", []string{"C01ABC"}},
		{"too many args in url mode", []string{
			"https://acme.slack.com/archives/C01ABC/p1709251200000100",
			"https://acme.slack.com/archives/C02DEF/p1709251300000200",
		}},
		{"channel mode extra args", []string{"C01ABC", "1709251200.000100", "1709251300.000200"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseThreadRef(tt.args); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}
