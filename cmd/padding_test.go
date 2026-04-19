package cmd

import (
	"reflect"
	"testing"
)

func TestPadDraftTS(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"short fraction", "1709251200.12", "1709251200.1200000"},
		{"single decimal", "1709251200.1", "1709251200.1000000"},
		{"exactly seven", "1709251200.1234567", "1709251200.1234567"},
		{"over-length truncated", "1709251200.12345678", "1709251200.1234567"},
		{"much longer truncated", "1709251200.123456789012", "1709251200.1234567"},
		{"no fraction unchanged", "1709251200", "1709251200"},
		{"empty fraction padded", "1709251200.", "1709251200.0000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padDraftTS(tt.in)
			if got != tt.want {
				t.Errorf("padDraftTS(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeDestinationsForWrite(t *testing.T) {
	tests := []struct {
		name string
		in   []destination
		want []destination
	}{
		{
			name: "DM with both channel_id and user_ids strips user_ids",
			in: []destination{
				{ChannelID: "D01", UserIDs: []string{"U01"}},
			},
			want: []destination{
				{ChannelID: "D01"},
			},
		},
		{
			name: "channel-only destination unchanged",
			in: []destination{
				{ChannelID: "C01"},
			},
			want: []destination{
				{ChannelID: "C01"},
			},
		},
		{
			name: "user_ids-only destination preserved (new group DM)",
			in: []destination{
				{UserIDs: []string{"U01", "U02"}},
			},
			want: []destination{
				{UserIDs: []string{"U01", "U02"}},
			},
		},
		{
			name: "thread fields preserved",
			in: []destination{
				{ChannelID: "C01", ThreadTS: "123.456", Broadcast: true, UserIDs: []string{"U01"}},
			},
			want: []destination{
				{ChannelID: "C01", ThreadTS: "123.456", Broadcast: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDestinationsForWrite(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeDestinationsForWrite(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
