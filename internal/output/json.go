package output

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

// writeEnrichedJSON marshals v to indented JSON, enriching Slack timestamp
// fields with ISO 8601 siblings, and writes the result to w.
func writeEnrichedJSON(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}

	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}

	enrichTimestamps(data)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// enrichTimestamps walks a JSON value and adds _iso fields next to Slack
// timestamp fields. A field is considered a Slack timestamp if its key is
// exactly "ts" or ends in "_ts", and its value parses as "epoch.microseconds".
func enrichTimestamps(v any) {
	switch val := v.(type) {
	case map[string]any:
		enrichMap(val)
	case []any:
		for _, item := range val {
			enrichTimestamps(item)
		}
	}
}

func enrichMap(m map[string]any) {
	// Collect keys first to avoid modifying map during iteration.
	additions := map[string]string{}
	for k, v := range m {
		if isTimestampKey(k) {
			if s, ok := v.(string); ok {
				if iso, ok := slackTsToISO(s); ok {
					additions[k+"_iso"] = iso
				}
			}
		}
		enrichTimestamps(v)
	}
	for k, v := range additions {
		m[k] = v
	}
}

// isTimestampKey returns true for keys that represent Slack timestamps:
// "ts", or any key ending in "_ts".
func isTimestampKey(key string) bool {
	return key == "ts" || strings.HasSuffix(key, "_ts")
}

// slackTsToISO converts a Slack timestamp string ("1705312200.123456") to
// an ISO 8601 / RFC3339 UTC string. Returns ("", false) if the value doesn't
// parse as a Slack timestamp.
func slackTsToISO(ts string) (string, bool) {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) != 2 {
		return "", false
	}

	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", false
	}

	// Sanity check: reject values outside a reasonable epoch range
	// (year 2000 to year 2100).
	if sec < 946684800 || sec > 4102444800 {
		return "", false
	}

	// Validate the fractional part is numeric (Slack uses 6-digit microseconds).
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return "", false
	}

	t := time.Unix(sec, 0).UTC()
	return t.Format(time.RFC3339), true
}
