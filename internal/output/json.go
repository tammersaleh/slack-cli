package output

import (
	"strconv"
	"strings"
	"time"
)

// enrichTimestamps walks a JSON map and adds _iso fields next to Slack
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

func isTimestampKey(key string) bool {
	return key == "ts" || strings.HasSuffix(key, "_ts")
}

func slackTsToISO(ts string) (string, bool) {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) != 2 {
		return "", false
	}

	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", false
	}

	if sec < 946684800 || sec > 4102444800 {
		return "", false
	}

	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return "", false
	}

	t := time.Unix(sec, 0).UTC()
	return t.Format(time.RFC3339), true
}
