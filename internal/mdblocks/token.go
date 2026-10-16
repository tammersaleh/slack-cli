package mdblocks

import (
	"bytes"
	"unicode"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// tokenKind sorts the Slack-specific inline tokens the Markdown subset
// recognizes on top of CommonMark.
type tokenKind int

const (
	tokenUser       tokenKind = iota // <@U01XYZ>
	tokenChannel                     // <#C01ABC> or <#C01ABC|label>
	tokenBroadcast                   // @here, @channel, @everyone
	tokenEmoji                       // :wave:, :wave::skin-tone-2:
	tokenBadMention                  // @name or <@name> - needs an id, reported as an error
)

var kindToken = ast.NewNodeKind("SlackToken")

// token is the AST node for a Slack inline token. Parsing it as a goldmark
// inline (rather than regexing decoded text afterwards) keeps backslash
// escapes and code spans literal for free and gives every token a source
// offset for error lines.
type token struct {
	ast.BaseInline
	kind   tokenKind
	value  string
	offset int
}

func (t *token) Kind() ast.NodeKind { return kindToken }

func (t *token) Dump(source []byte, level int) {
	ast.DumpHelper(t, source, level, map[string]string{"value": t.value}, nil)
}

type tokenParser struct{}

func (tokenParser) Trigger() []byte { return []byte{'<', ':', '@'} }

func (tokenParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, seg := block.PeekLine()
	if len(line) == 0 {
		return nil
	}
	var n int
	var tok *token
	switch line[0] {
	case '<':
		tok, n = parseAngle(line)
	case ':':
		tok, n = parseEmoji(line, block.PrecendingCharacter())
	case '@':
		tok, n = parseAt(line, block.PrecendingCharacter())
	}
	if tok == nil {
		return nil
	}
	tok.offset = seg.Start
	block.Advance(n)
	return tok
}

// parseAngle handles <@ID>, <#ID>, <#ID|label>, and the <@name> mistake.
func parseAngle(line []byte) (*token, int) {
	if len(line) < 3 {
		return nil, 0
	}
	end := bytes.IndexByte(line, '>')
	if end < 2 {
		// No closing bracket, or "<>" with nothing inside.
		return nil, 0
	}
	body := line[1:end]
	switch body[0] {
	case '@':
		id := string(body[1:])
		if isSlackID(id, "UW") {
			return &token{kind: tokenUser, value: id}, end + 1
		}
		if len(id) > 0 && !bytes.ContainsAny(body, " \t") {
			return &token{kind: tokenBadMention, value: id}, end + 1
		}
	case '#':
		id := string(body[1:])
		if i := bytes.IndexByte(body, '|'); i > 0 {
			id = string(body[1:i])
		}
		if isSlackID(id, "CGD") {
			return &token{kind: tokenChannel, value: id}, end + 1
		}
	}
	return nil, 0
}

// parseEmoji handles :name: and :name::skin-tone-N:. Both outer neighbours
// must be non-word characters so 10:30:45 stays a time.
func parseEmoji(line []byte, prev rune) (*token, int) {
	if isWord(prev) {
		return nil, 0
	}
	n := emojiNameLen(line[1:])
	if n == 0 || len(line) <= 1+n || line[1+n] != ':' {
		return nil, 0
	}
	name := string(line[1 : 1+n])
	end := 2 + n
	// Optional skin-tone modifier glued on: :wave::skin-tone-2:
	const tone = ":skin-tone-"
	if rest := line[end:]; len(rest) >= len(tone)+2 && string(rest[:len(tone)]) == tone &&
		rest[len(tone)] >= '2' && rest[len(tone)] <= '6' && rest[len(tone)+1] == ':' {
		name += "::skin-tone-" + string(rest[len(tone)])
		end += len(tone) + 2
	}
	if end < len(line) && isWord(rune(line[end])) {
		return nil, 0
	}
	return &token{kind: tokenEmoji, value: name}, end
}

func emojiNameLen(b []byte) int {
	n := 0
	for n < len(b) {
		c := b[n]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '+' || c == '-' {
			n++
			continue
		}
		break
	}
	return n
}

// parseAt handles @here/@channel/@everyone and flags any other @word as a
// mention that needs an id. A word character before the @ means an email or
// a handle glued to text, which stays literal.
func parseAt(line []byte, prev rune) (*token, int) {
	// A '<' before the '@' is an angle form that parseAngle already declined
	// (unterminated, or escaped so goldmark skipped the '<'); keep it literal.
	if isWord(prev) || prev == '<' {
		return nil, 0
	}
	n := 1
	for n < len(line) && isWord(rune(line[n])) {
		n++
	}
	if n == 1 {
		return nil, 0
	}
	word := string(line[1:n])
	switch word {
	case "here", "channel", "everyone":
		return &token{kind: tokenBroadcast, value: word}, n
	}
	return &token{kind: tokenBadMention, value: word}, n
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isSlackID reports whether id is one of the given prefix letters followed by
// at least one uppercase letter or digit.
func isSlackID(id string, prefixes string) bool {
	if len(id) < 2 || !bytes.ContainsRune([]byte(prefixes), rune(id[0])) {
		return false
	}
	for _, c := range id[1:] {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
