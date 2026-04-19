package cmd

import "testing"

func TestParsePageCursor(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{"empty defaults to page 1", "", 1, false},
		{"explicit 1", "1", 1, false},
		{"later page", "7", 7, false},
		{"negative rejected", "-1", 0, true},
		{"zero rejected", "0", 0, true},
		{"non-numeric rejected", "abc", 0, true},
		{"legacy base64 rejected", "cGFnZTox", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePageCursor(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsePageCursor(%q) = %d, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePageCursor(%q) returned unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parsePageCursor(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
