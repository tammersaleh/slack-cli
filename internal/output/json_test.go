package output

import "testing"

// The plausibility window in slackTsToISO is inclusive at both ends. Its job is
// to reject values that are not Slack timestamps at all (a port number, a count)
// rather than to narrow the range of real ones, so a ts sitting exactly on a
// bound must still convert.
func TestSlackTsToISO_EpochBounds(t *testing.T) {
	tests := []struct {
		name    string
		ts      string
		wantOK  bool
		wantISO string
	}{
		{
			name:    "lower bound is accepted",
			ts:      "946684800.000000",
			wantOK:  true,
			wantISO: "2000-01-01T00:00:00Z",
		},
		{
			name:   "one second below lower bound is rejected",
			ts:     "946684799.000000",
			wantOK: false,
		},
		{
			name:    "upper bound is accepted",
			ts:      "4102444800.000000",
			wantOK:  true,
			wantISO: "2100-01-01T00:00:00Z",
		},
		{
			name:   "one second above upper bound is rejected",
			ts:     "4102444801.000000",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iso, ok := slackTsToISO(tt.ts)
			if ok != tt.wantOK {
				t.Fatalf("slackTsToISO(%q) ok = %v, want %v", tt.ts, ok, tt.wantOK)
			}
			if ok && iso != tt.wantISO {
				t.Errorf("slackTsToISO(%q) = %q, want %q", tt.ts, iso, tt.wantISO)
			}
		})
	}
}
