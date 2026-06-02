package cmd

import (
	"testing"

	"github.com/slack-go/slack"
)

func TestNormalizeFieldKey(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"Manager", "manager"},
		{"Employee ID", "employee_id"},
		{"GitHub Handle", "github_handle"},
		{"Cost Center", "cost_center"},
		{"  Mixed   Spaces  ", "mixed_spaces"},
		{"Non-ASCII: café", "non_ascii_caf"},
		{"", ""},
		{"---", ""},
	}
	for _, tt := range tests {
		if got := normalizeFieldKey(tt.label); got != tt.want {
			t.Errorf("normalizeFieldKey(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}

// profileWithFields builds a UserProfile carrying the given custom fields.
func profileWithFields(fields map[string]slack.UserProfileCustomField) *slack.UserProfile {
	p := &slack.UserProfile{}
	p.Fields.SetMap(fields)
	return p
}

func TestBuildCustomFields_Basic(t *testing.T) {
	p := profileWithFields(map[string]slack.UserProfileCustomField{
		"Xf01": {Label: "Manager", Value: "U09KU7J7TA5"},
		"Xf02": {Label: "Division", Value: "Technology"},
		"Xf03": {Label: "Employee ID", Value: "445"},
	})

	resolve := func(id string) (string, bool) {
		if id == "U09KU7J7TA5" {
			return "Jon Jones", true
		}
		return "", false
	}

	cf := buildCustomFields(p, resolve)

	mgr, ok := cf["manager"]
	if !ok {
		t.Fatalf("expected manager key, got keys %v", keysOf(cf))
	}
	if mgr.Value != "U09KU7J7TA5" || mgr.ID != "Xf01" || mgr.Label != "Manager" {
		t.Errorf("manager field wrong: %+v", mgr)
	}
	if mgr.ValueName != "Jon Jones" {
		t.Errorf("expected value_name resolved to Jon Jones, got %q", mgr.ValueName)
	}
	if cf["division"].Value != "Technology" {
		t.Errorf("division wrong: %+v", cf["division"])
	}
	// A plain text value that isn't a user ID must not get a value_name.
	if cf["employee_id"].ValueName != "" {
		t.Errorf("employee_id should have no value_name, got %q", cf["employee_id"].ValueName)
	}
}

// Two fields whose labels normalize to the same key must produce a
// deterministic suffix ordering (sorted by field ID), not flap with Go's
// random map iteration.
func TestBuildCustomFields_DeterministicCollision(t *testing.T) {
	p := profileWithFields(map[string]slack.UserProfileCustomField{
		"XfBBB": {Label: "Team", Value: "second"},
		"XfAAA": {Label: "Team", Value: "first"},
	})
	resolve := func(string) (string, bool) { return "", false }

	for range 20 {
		cf := buildCustomFields(p, resolve)
		// Lower field ID (XfAAA) wins the unsuffixed key.
		if cf["team"].Value != "first" || cf["team"].ID != "XfAAA" {
			t.Fatalf("expected team=XfAAA(first), got %+v", cf["team"])
		}
		if cf["team_2"].Value != "second" || cf["team_2"].ID != "XfBBB" {
			t.Fatalf("expected team_2=XfBBB(second), got %+v", cf["team_2"])
		}
	}
}

// A label that normalizes to empty falls back to field_<id>.
func TestBuildCustomFields_EmptyLabelFallback(t *testing.T) {
	p := profileWithFields(map[string]slack.UserProfileCustomField{
		"Xf99": {Label: "---", Value: "x"},
	})
	cf := buildCustomFields(p, func(string) (string, bool) { return "", false })
	if _, ok := cf["field_Xf99"]; !ok {
		t.Errorf("expected field_Xf99 fallback key, got %v", keysOf(cf))
	}
}

// Empty-valued custom fields are dropped (Slack returns empties for unset).
func TestBuildCustomFields_DropsEmpty(t *testing.T) {
	p := profileWithFields(map[string]slack.UserProfileCustomField{
		"Xf01": {Label: "Manager", Value: ""},
		"Xf02": {Label: "Division", Value: "Technology"},
	})
	cf := buildCustomFields(p, func(string) (string, bool) { return "", false })
	if _, ok := cf["manager"]; ok {
		t.Errorf("empty manager field should be dropped")
	}
	if len(cf) != 1 {
		t.Errorf("expected only division, got %v", keysOf(cf))
	}
}

func keysOf(m map[string]customField) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
