// Package slackurl parses Slack web/app URLs into typed references. It is a
// pure parser: no network, no resolution, no CLI semantics. Callers decide what
// to do with a Ref based on its Kind.
package slackurl

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Kind classifies what a Slack URL points at.
type Kind int

const (
	KindUnknown Kind = iota
	KindChannel
	KindMessage
	KindUser
	KindFile
)

func (k Kind) String() string {
	switch k {
	case KindChannel:
		return "channel"
	case KindMessage:
		return "message"
	case KindUser:
		return "user"
	case KindFile:
		return "file"
	default:
		return "unknown"
	}
}

// Ref is a parsed Slack URL. Only the fields relevant to Kind are populated;
// a file URL also carries the uploader's UserID, and a message URL carries TS
// (and ThreadTS when the link points at a threaded reply).
type Ref struct {
	Kind      Kind
	ChannelID string
	UserID    string
	FileID    string
	TS        string
	ThreadTS  string
}

var (
	channelIDRE = regexp.MustCompile(`^[CDGM][A-Z0-9]+$`)
	userIDRE    = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
	fileIDRE    = regexp.MustCompile(`^F[A-Z0-9]+$`)
	// pTSRE matches the `p<digits>` message segment. The digits are the ts with
	// the dot removed; the last 6 are the microsecond fraction.
	pTSRE = regexp.MustCompile(`^p(\d{7,})$`)
	// dottedTSRE matches a Slack ts in its canonical `seconds.micros` form.
	dottedTSRE = regexp.MustCompile(`^\d+\.\d{6}$`)
)

// Parse interprets s as a Slack URL.
//
// It returns matched=false (and a nil error) when s is not an http(s) URL, so
// callers can fall through to their existing ID/name/email handling.
//
// When s is http(s)-shaped it always reports matched=true. A non-nil error then
// means s is URL-shaped but not a usable Slack reference (wrong host, unknown
// route, malformed id/ts, contradictory query) - callers should fast-fail
// rather than treat it as a name.
func Parse(s string) (Ref, bool, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return Ref{}, false, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, true, fmt.Errorf("not a valid URL: %w", err)
	}
	if !strings.HasSuffix(strings.ToLower(u.Hostname()), "slack.com") {
		return Ref{}, true, fmt.Errorf("not a Slack URL: host %q", u.Hostname())
	}

	segs := splitPath(u.Path)
	if len(segs) == 0 {
		return Ref{}, true, fmt.Errorf("no path in Slack URL")
	}

	switch segs[0] {
	case "archives":
		return parseArchives(segs, u.Query())
	case "client":
		return parseClient(segs)
	case "team":
		return parseTeam(segs)
	case "user":
		return parseUser(segs)
	case "files":
		return parseFiles(segs)
	default:
		return Ref{}, true, fmt.Errorf("unsupported Slack URL route %q", segs[0])
	}
}

// parseArchives handles /archives/<channel> and /archives/<channel>/p<ts>.
func parseArchives(segs []string, q url.Values) (Ref, bool, error) {
	if len(segs) < 2 {
		return Ref{}, true, fmt.Errorf("archives URL missing channel id")
	}
	channel := segs[1]
	if !channelIDRE.MatchString(channel) {
		return Ref{}, true, fmt.Errorf("not a channel id: %q", channel)
	}

	// No message segment: this is a channel link.
	if len(segs) < 3 {
		return Ref{Kind: KindChannel, ChannelID: channel}, true, nil
	}

	ts, err := tsFromPSegment(segs[2])
	if err != nil {
		return Ref{}, true, err
	}

	ref := Ref{Kind: KindMessage, ChannelID: channel, TS: ts}

	if cid := q.Get("cid"); cid != "" && cid != channel {
		return Ref{}, true, fmt.Errorf("cid %q does not match channel %q", cid, channel)
	}
	if tts := q.Get("thread_ts"); tts != "" {
		if !dottedTSRE.MatchString(tts) {
			return Ref{}, true, fmt.Errorf("malformed thread_ts %q", tts)
		}
		ref.ThreadTS = tts
	}
	return ref, true, nil
}

// parseClient handles /client/<team-or-org>/<id>. The middle segment is a
// workspace (T) or enterprise org (E) id and is ignored; the trailing id's
// prefix decides the kind.
func parseClient(segs []string) (Ref, bool, error) {
	if len(segs) < 3 {
		return Ref{}, true, fmt.Errorf("client URL missing id")
	}
	id := segs[2]
	switch {
	case channelIDRE.MatchString(id):
		return Ref{Kind: KindChannel, ChannelID: id}, true, nil
	case userIDRE.MatchString(id):
		return Ref{Kind: KindUser, UserID: id}, true, nil
	default:
		return Ref{}, true, fmt.Errorf("unsupported client id %q", id)
	}
}

// parseTeam handles /team/<user>.
func parseTeam(segs []string) (Ref, bool, error) {
	if len(segs) < 2 {
		return Ref{}, true, fmt.Errorf("team URL missing user id")
	}
	id := strings.TrimPrefix(segs[1], "@")
	if !userIDRE.MatchString(id) {
		return Ref{}, true, fmt.Errorf("not a user id: %q", id)
	}
	return Ref{Kind: KindUser, UserID: id}, true, nil
}

// parseUser handles the enterprise /user/@<user> profile route.
func parseUser(segs []string) (Ref, bool, error) {
	if len(segs) < 2 {
		return Ref{}, true, fmt.Errorf("user URL missing user id")
	}
	id := strings.TrimPrefix(segs[1], "@")
	if !userIDRE.MatchString(id) {
		return Ref{}, true, fmt.Errorf("not a user id: %q", id)
	}
	return Ref{Kind: KindUser, UserID: id}, true, nil
}

// parseFiles handles /files/<uploader>/<file>/... . The file id is the target;
// the uploader id is retained for context.
func parseFiles(segs []string) (Ref, bool, error) {
	if len(segs) < 3 {
		return Ref{}, true, fmt.Errorf("files URL missing file id")
	}
	uploader, file := segs[1], segs[2]
	if !fileIDRE.MatchString(file) {
		return Ref{}, true, fmt.Errorf("not a file id: %q", file)
	}
	ref := Ref{Kind: KindFile, FileID: file}
	if userIDRE.MatchString(uploader) {
		ref.UserID = uploader
	}
	return ref, true, nil
}

// tsFromPSegment converts a `p1713300000123456` segment to `1713300000.123456`.
func tsFromPSegment(seg string) (string, error) {
	m := pTSRE.FindStringSubmatch(seg)
	if m == nil {
		return "", fmt.Errorf("not a message timestamp: %q", seg)
	}
	digits := m[1]
	return digits[:len(digits)-6] + "." + digits[len(digits)-6:], nil
}

// splitPath returns the non-empty, unescaped path segments.
func splitPath(p string) []string {
	var out []string
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" {
			continue
		}
		if unescaped, err := url.PathUnescape(seg); err == nil {
			seg = unescaped
		}
		out = append(out, seg)
	}
	return out
}
