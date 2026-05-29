package cmd

import "testing"

// The draft validators only type-check the shapes they understand. A table
// block nested inside a rich_text element is invalid Block Kit, but the CLI
// does shallow validation and leaves deep block semantics to Slack's API (see
// the "Validation" section in skills/slack-cli/SKILL.md). This documents that
// boundary: the top-level table/data_table rejection (which steers callers to
// attachments) must NOT fire for a table buried inside rich_text.elements -
// that payload passes local validation and is left for the API to reject.
func TestParseDraftPayload_TableNestedInRichTextNotRejectedLocally(t *testing.T) {
	for _, blocks := range []string{
		`[{"type":"rich_text","elements":[{"type":"table","rows":[[{"type":"raw_text","text":"A"}]]}]}]`,
		`[{"type":"rich_text","elements":[{"type":"data_table","caption":"c","rows":[]}]}]`,
	} {
		if _, err := parseDraftPayload([]byte(blocks)); err != nil {
			t.Errorf("table nested inside rich_text.elements should pass shallow validation, got %v\ninput: %s", err, blocks)
		}
	}
}
