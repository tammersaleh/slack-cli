package output

import (
	"strings"
	"testing"
)

// Each helper must: set Err to the expected code, populate Detail with the
// input verbatim, populate Hint with a runnable command that lets the agent
// self-recover, and set Code to ExitGeneral. These tests fail fast if any
// of those contracts regress.
func TestChannelNotFound(t *testing.T) {
	e := ChannelNotFound("ext-acme")
	if e.Err != "channel_not_found" {
		t.Errorf("Err=%q want channel_not_found", e.Err)
	}
	if !strings.Contains(e.Detail, "ext-acme") {
		t.Errorf("Detail=%q should echo input", e.Detail)
	}
	if !strings.Contains(e.Hint, "slack channel list") {
		t.Errorf("Hint=%q should suggest 'slack channel list'", e.Hint)
	}
	if e.Code != ExitGeneral {
		t.Errorf("Code=%d want ExitGeneral", e.Code)
	}
}

func TestUserNotFound(t *testing.T) {
	e := UserNotFound("@missing")
	if e.Err != "user_not_found" {
		t.Errorf("Err=%q", e.Err)
	}
	if !strings.Contains(e.Detail, "@missing") {
		t.Errorf("Detail=%q should echo input", e.Detail)
	}
	if !strings.Contains(e.Hint, "slack user list") {
		t.Errorf("Hint=%q should suggest 'slack user list'", e.Hint)
	}
}

func TestDraftNotFound(t *testing.T) {
	e := DraftNotFound("Dr01234")
	if e.Err != "draft_not_found" {
		t.Errorf("Err=%q", e.Err)
	}
	if !strings.Contains(e.Detail, "Dr01234") {
		t.Errorf("Detail=%q should echo input", e.Detail)
	}
	if !strings.Contains(e.Hint, "slack draft list") {
		t.Errorf("Hint=%q should suggest 'slack draft list'", e.Hint)
	}
}

func TestSectionNotFound(t *testing.T) {
	e := SectionNotFound("S02ABC")
	if e.Err != "section_not_found" {
		t.Errorf("Err=%q", e.Err)
	}
	if !strings.Contains(e.Detail, "S02ABC") {
		t.Errorf("Detail=%q should echo input", e.Detail)
	}
	if !strings.Contains(e.Hint, "slack section list") {
		t.Errorf("Hint=%q should suggest 'slack section list'", e.Hint)
	}
}

func TestInvalidTimestamp(t *testing.T) {
	e := InvalidTimestamp("--at", "not-a-date")
	if e.Err != "invalid_timestamp" {
		t.Errorf("Err=%q", e.Err)
	}
	if !strings.Contains(e.Detail, "--at") || !strings.Contains(e.Detail, "not-a-date") {
		t.Errorf("Detail=%q should reference both flag and value", e.Detail)
	}
	// Hint should enumerate accepted formats so agents learn from one failure.
	for _, fmt := range []string{"RFC 3339", "YYYY-MM-DD"} {
		if !strings.Contains(e.Hint, fmt) {
			t.Errorf("Hint=%q should mention %q", e.Hint, fmt)
		}
	}
}

func TestMissingBlocks(t *testing.T) {
	e := MissingBlocks()
	if e.Err != "missing_blocks" {
		t.Errorf("Err=%q", e.Err)
	}
	if !strings.Contains(e.Hint, "slack skill") {
		t.Errorf("Hint=%q should point agent at 'slack skill'", e.Hint)
	}
	// Minimal runnable example so an agent can copy-paste the first time.
	if !strings.Contains(e.Hint, "rich_text") {
		t.Errorf("Hint=%q should contain a minimal Block Kit example", e.Hint)
	}
}

func TestThreadNotFound_NoMessage(t *testing.T) {
	e := ThreadNotFoundNoMessage("C01", "1700000000.123456")
	if e.Err != "thread_not_found" {
		t.Errorf("Err=%q", e.Err)
	}
	if !strings.Contains(e.Detail, "1700000000.123456") {
		t.Errorf("Detail=%q should echo timestamp", e.Detail)
	}
	if !strings.Contains(e.Hint, "slack message list") {
		t.Errorf("Hint=%q should suggest 'slack message list' for discovery", e.Hint)
	}
}

func TestThreadNotFound_NoReplies(t *testing.T) {
	e := ThreadNotFoundNoReplies("1700000000.000100")
	if e.Err != "thread_not_found" {
		t.Errorf("Err=%q", e.Err)
	}
	if e.Input != "1700000000.000100" {
		t.Errorf("Input=%q should be the passed timestamp", e.Input)
	}
	if !strings.Contains(e.Hint, "--has-replies") {
		t.Errorf("Hint=%q should mention --has-replies for discovery", e.Hint)
	}
}

func TestError_AsItem(t *testing.T) {
	e := ChannelNotFound("#missing")
	m := e.AsItem()
	if m["input"] != "#missing" {
		t.Errorf("input=%v want '#missing' (pulled from Error.Input)", m["input"])
	}
	if m["error"] != "channel_not_found" {
		t.Errorf("error=%v want channel_not_found", m["error"])
	}
	if _, ok := m["detail"]; !ok {
		t.Error("detail should be present when Detail is non-empty")
	}
	if _, ok := m["hint"]; !ok {
		t.Error("hint should be present when Hint is non-empty")
	}
}

func TestError_AsItem_OmitsEmpty(t *testing.T) {
	e := &Error{Err: "some_error"}
	m := e.AsItem()
	if _, ok := m["detail"]; ok {
		t.Error("detail should be omitted when Detail is empty")
	}
	if _, ok := m["hint"]; ok {
		t.Error("hint should be omitted when Hint is empty")
	}
	if _, ok := m["input"]; ok {
		t.Error("input should be omitted when Input is empty")
	}
}
