// Package mdblocks converts a Markdown document into the Block Kit JSON a
// Slack draft accepts: exactly one top-level rich_text block whose elements
// are rich_text_section / rich_text_list / rich_text_quote /
// rich_text_preformatted containers, in source order.
//
// It is pure - no network, no Slack client. Mentions are accepted only in
// the explicit <@Uxxx> form; anything the supported subset cannot represent
// is an *Error naming the source line, never a silent drop.
package mdblocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// maxIndent is the deepest list nesting Slack renders (indent 0, 1, 2).
const maxIndent = 2

// Error is a conversion failure. Line is 1-based; 0 means the whole document
// (for example, empty input).
type Error struct {
	Line   int
	Detail string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Detail)
	}
	return e.Detail
}

// Convert renders src into a JSON array holding one rich_text block.
func Convert(src []byte) ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Linkify, extension.Table),
		goldmark.WithParserOptions(parser.WithInlineParsers(
			util.Prioritized(&tokenParser{}, 100),
		)),
	)
	doc := md.Parser().Parse(text.NewReader(src))
	c := &converter{src: src}
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if err := c.block(n); err != nil {
			return nil, err
		}
	}
	if len(c.containers) == 0 {
		return nil, &Error{Detail: "no content: the Markdown is empty"}
	}
	c.applyAbsorptionRule()
	return json.Marshal([]map[string]any{{
		"type":     "rich_text",
		"elements": c.containers,
	}})
}

type converter struct {
	src        []byte
	containers []map[string]any
}

// style is the inherited inline formatting at a point in the walk.
type style struct {
	bold, italic, strike, code bool
}

func (s style) json() map[string]any {
	m := map[string]any{}
	if s.bold {
		m["bold"] = true
	}
	if s.italic {
		m["italic"] = true
	}
	if s.strike {
		m["strike"] = true
	}
	if s.code {
		m["code"] = true
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// textStyle is the style a non-text inline (link, user, channel, broadcast)
// may carry: code is text-only.
func (s style) nonText() map[string]any {
	s.code = false
	return s.json()
}

func (c *converter) errAt(n ast.Node, format string, args ...any) error {
	return &Error{Line: c.lineOf(n), Detail: fmt.Sprintf(format, args...)}
}

// block converts one top-level (or quote/list-item child) block node.
func (c *converter) block(n ast.Node) error {
	switch v := n.(type) {
	case *ast.Paragraph:
		elems, err := c.inlines(v, style{})
		if err != nil {
			return err
		}
		c.push(section(elems))
	case *ast.Heading:
		// Slack has no heading element. A heading is a bold section that
		// always ends with "\n" so a blank line separates it from what
		// follows; the level is dropped.
		elems, err := c.inlines(v, style{bold: true})
		if err != nil {
			return err
		}
		c.push(section(ensureTrailingNewline(elems)))
	case *ast.List:
		return c.list(v, 0)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		// The fence language tag has nowhere to live in
		// rich_text_preformatted and is dropped.
		code := c.codeLines(v)
		if code == "" {
			return c.errAt(n, "an empty code block has no rich_text equivalent; Slack rejects an empty text inline")
		}
		c.push(map[string]any{
			"type":     "rich_text_preformatted",
			"elements": []map[string]any{{"type": "text", "text": code}},
		})
	case *ast.Blockquote:
		return c.quote(v)
	case *ast.ThematicBreak:
		return c.errAt(n, "a horizontal rule (---) has no rich_text equivalent; remove it")
	case *ast.HTMLBlock:
		return c.errAt(n, "HTML is not supported in draft Markdown")
	case *east.Table:
		return c.errAt(n, "a Markdown table is not supported in the body; pass --table <csv> to attach one instead")
	case *ast.LinkReferenceDefinition:
		// Consumed by the reference links that point at it; nothing to emit.
	default:
		return c.errAt(n, "unsupported Markdown construct %s", n.Kind())
	}
	return nil
}

func (c *converter) push(container map[string]any) {
	c.containers = append(c.containers, container)
}

func section(elems []map[string]any) map[string]any {
	return map[string]any{"type": "rich_text_section", "elements": elems}
}

// list emits a Markdown list as one or more rich_text_list containers at the
// given indent. A rich_text_list cannot hold a nested list, so a nested list
// splits its parent: the items before it flush as one container, the nested
// list follows at indent+1, and the items after it open a new container. An
// ordered list's later segments continue the numbering via offset.
func (c *converter) list(l *ast.List, indent int) error {
	if indent > maxIndent {
		return c.errAt(l, "lists nest at most %d levels deep", maxIndent+1)
	}
	var items []map[string]any
	emitted := 0
	flush := func() {
		if len(items) == 0 {
			return
		}
		container := map[string]any{
			"type":     "rich_text_list",
			"style":    "bullet",
			"indent":   indent,
			"elements": items,
		}
		if l.IsOrdered() {
			container["style"] = "ordered"
			container["offset"] = max(l.Start-1, 0) + emitted
		}
		c.push(container)
		emitted += len(items)
		items = nil
	}
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		gotText := false
		if item.FirstChild() == nil {
			// Slack rejects an empty text inline (invalid_message, verified
			// live 2026-10-16), so an empty bullet can be neither kept nor
			// silently dropped.
			return c.errAt(item, "an empty list item has no rich_text equivalent; give it text or remove it")
		}
		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			switch v := child.(type) {
			case *ast.TextBlock, *ast.Paragraph:
				if gotText {
					return c.errAt(child, "a list item holds one paragraph; put extra paragraphs after the list")
				}
				elems, err := c.inlines(v, style{})
				if err != nil {
					return err
				}
				items = append(items, section(elems))
				gotText = true
			case *ast.List:
				if !gotText {
					return c.errAt(child, "a list item needs its own text before a nested list; Slack rejects an empty item")
				}
				flush()
				if err := c.list(v, indent+1); err != nil {
					return err
				}
			default:
				return c.errAt(child, "a list item may hold text and a nested list only, not %s", child.Kind())
			}
		}
	}
	flush()
	return nil
}

// quote emits a blockquote as one rich_text_quote. Only paragraphs are
// allowed inside; they join with a blank line.
func (c *converter) quote(q *ast.Blockquote) error {
	var elems []map[string]any
	for child := q.FirstChild(); child != nil; child = child.NextSibling() {
		p, ok := child.(*ast.Paragraph)
		if !ok {
			return c.errAt(child, "a quote may hold paragraphs only, not %s; nested lists, code, and quotes are not supported", child.Kind())
		}
		inner, err := c.inlines(p, style{})
		if err != nil {
			return err
		}
		if len(elems) > 0 {
			elems = appendText(elems, "\n\n", style{})
		}
		elems = append(elems, inner...)
	}
	if len(elems) == 0 {
		return c.errAt(q, "an empty quote has no rich_text equivalent; Slack rejects an empty text inline")
	}
	c.push(map[string]any{"type": "rich_text_quote", "elements": mergeText(elems)})
	return nil
}

// textOf decodes a text segment the way goldmark's HTML renderer would:
// backslash escapes and HTML entities are resolved at render time, not parse
// time, so the raw segment still carries "\*" and "&amp;".
func (c *converter) textOf(t *ast.Text) string {
	raw := t.Segment.Value(c.src)
	if t.IsRaw() {
		return string(raw)
	}
	// A backslash hard break keeps its backslash in the segment; goldmark's
	// writer drops it at render time, so do the same. Only the backslash form
	// ends flush against the newline - a two-space break has its spaces
	// trimmed, so a backslash before them is literal and the segment stops
	// before the spaces.
	if t.HardLineBreak() && t.Segment.Stop < len(c.src) && (c.src[t.Segment.Stop] == '\n' || c.src[t.Segment.Stop] == '\r') {
		raw = bytes.TrimSuffix(raw, []byte{'\\'})
	}
	return decodeText(raw)
}

// decodeText resolves backslash escapes and HTML entities in one pass, the
// way goldmark's writer does. Running the two as separate passes would turn
// "\&amp;" into "&" (the unescape yields "&amp;", which the entity pass then
// resolves) when the author meant the literal text "&amp;".
func decodeText(raw []byte) string {
	var b strings.Builder
	run := 0 // start of the current unescaped run
	flush := func(end int) {
		if end > run {
			b.Write(resolveEntities(raw[run:end]))
		}
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) && util.IsPunct(raw[i+1]) {
			flush(i)
			b.WriteByte(raw[i+1])
			i++
			run = i + 1
		}
	}
	flush(len(raw))
	return b.String()
}

// resolveEntities decodes HTML entity names and numeric character references
// in one left-to-right scan, so a decoded reference is never re-read as the
// start of another one ("&#38;amp;" is the literal text "&amp;", not "&").
func resolveEntities(raw []byte) []byte {
	if !bytes.ContainsRune(raw, '&') {
		return raw
	}
	var out []byte
	for i := 0; i < len(raw); i++ {
		if raw[i] != '&' {
			out = append(out, raw[i])
			continue
		}
		semi := bytes.IndexByte(raw[i:], ';')
		if semi < 2 || semi > 40 {
			out = append(out, '&')
			continue
		}
		body := raw[i+1 : i+semi]
		if decoded, ok := decodeEntityBody(body); ok {
			out = append(out, decoded...)
			i += semi
			continue
		}
		out = append(out, '&')
	}
	return out
}

func decodeEntityBody(body []byte) ([]byte, bool) {
	if body[0] == '#' {
		var r rune
		var err error
		// CommonMark caps references at 7 decimal or 6 hex digits.
		if len(body) > 1 && (body[1] == 'x' || body[1] == 'X') {
			if len(body) > 8 {
				return nil, false
			}
			var v uint64
			v, err = strconv.ParseUint(string(body[2:]), 16, 32)
			r = rune(v)
		} else {
			if len(body) > 8 {
				return nil, false
			}
			var v uint64
			v, err = strconv.ParseUint(string(body[1:]), 10, 32)
			r = rune(v)
		}
		if err != nil || len(body) < 2 {
			return nil, false
		}
		if r == 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) {
			r = '\uFFFD'
		}
		return []byte(string(r)), true
	}
	if ent, ok := util.LookUpHTML5EntityByName(string(body)); ok {
		return ent.Characters, true
	}
	return nil, false
}

func (c *converter) codeLines(n ast.Node) string {
	var b bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(c.src))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// inlines flattens a block's inline children into Slack inline elements,
// merging adjacent text runs that share a style.
func (c *converter) inlines(parent ast.Node, st style) ([]map[string]any, error) {
	var out []map[string]any
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		var err error
		out, err = c.inline(out, n, st)
		if err != nil {
			return nil, err
		}
	}
	return mergeText(out), nil
}

func (c *converter) inline(out []map[string]any, n ast.Node, st style) ([]map[string]any, error) {
	switch v := n.(type) {
	case *ast.Text:
		out = appendText(out, c.textOf(v), st)
		if v.SoftLineBreak() || v.HardLineBreak() {
			out = appendText(out, "\n", st)
		}
	case *ast.String:
		out = appendText(out, string(v.Value), st)
	case *ast.Emphasis:
		child := st
		if v.Level == 2 {
			child.bold = true
		} else {
			child.italic = true
		}
		return c.children(out, v, child)
	case *east.Strikethrough:
		child := st
		child.strike = true
		return c.children(out, v, child)
	case *ast.CodeSpan:
		child := st
		child.code = true
		var b strings.Builder
		for t := v.FirstChild(); t != nil; t = t.NextSibling() {
			if tt, ok := t.(*ast.Text); ok {
				b.Write(tt.Segment.Value(c.src))
			}
		}
		out = appendText(out, strings.ReplaceAll(b.String(), "\n", " "), child)
	case *ast.Link:
		label, err := c.plainLabel(v)
		if err != nil {
			return nil, err
		}
		return c.appendLink(out, n, string(v.Destination), label, st)
	case *ast.AutoLink:
		url := string(v.URL(c.src))
		label := ""
		if v.AutoLinkType == ast.AutoLinkEmail {
			label = string(v.Label(c.src))
			if !strings.HasPrefix(strings.ToLower(url), "mailto:") {
				url = "mailto:" + url
			}
		}
		return c.appendLink(out, n, url, label, st)
	case *ast.Image:
		return nil, c.errAt(n, "an image has no rich_text equivalent; link to it instead")
	case *ast.RawHTML:
		return nil, c.errAt(n, "HTML is not supported in draft Markdown")
	case *token:
		return c.appendToken(out, v, st)
	default:
		return nil, c.errAt(n, "unsupported inline Markdown construct %s", n.Kind())
	}
	return out, nil
}

func (c *converter) children(out []map[string]any, parent ast.Node, st style) ([]map[string]any, error) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		var err error
		out, err = c.inline(out, n, st)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// plainLabel flattens a link's children to plain text. Slack link elements
// hold text only, so a mention or emoji inside the label is an error rather
// than a silently dropped element.
func (c *converter) plainLabel(link ast.Node) (string, error) {
	var b strings.Builder
	var walk func(n ast.Node) error
	walk = func(n ast.Node) error {
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			switch v := ch.(type) {
			case *ast.Text:
				b.WriteString(c.textOf(v))
				if v.SoftLineBreak() || v.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(v.Value)
			case *token:
				return c.errAt(link, "mentions, channels, and emoji are not supported inside link text")
			case *ast.Image:
				return c.errAt(link, "an image inside link text has no rich_text equivalent")
			default:
				if err := walk(ch); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(link); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (c *converter) appendLink(out []map[string]any, n ast.Node, url, label string, st style) ([]map[string]any, error) {
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "mailto:") {
		return nil, c.errAt(n, "link %q must be an absolute http, https, or mailto URL", url)
	}
	el := map[string]any{"type": "link", "url": url}
	if label != "" && label != url {
		el["text"] = label
	}
	if s := st.nonText(); s != nil {
		el["style"] = s
	}
	return append(out, el), nil
}

func (c *converter) appendToken(out []map[string]any, t *token, st style) ([]map[string]any, error) {
	var el map[string]any
	switch t.kind {
	case tokenUser:
		el = map[string]any{"type": "user", "user_id": t.value}
	case tokenChannel:
		el = map[string]any{"type": "channel", "channel_id": t.value}
	case tokenBroadcast:
		el = map[string]any{"type": "broadcast", "range": t.value}
	case tokenEmoji:
		// The emoji element has no style field.
		return append(out, map[string]any{"type": "emoji", "name": t.value}), nil
	case tokenBadMention:
		return nil, &Error{Line: c.lineOfOffset(t.offset), Detail: fmt.Sprintf("@%s is not resolvable here; mention people as <@Uxxx> (slack user info @name gives the id)", t.value)}
	default:
		return nil, &Error{Line: c.lineOfOffset(t.offset), Detail: "unknown token"}
	}
	if s := st.nonText(); s != nil {
		el["style"] = s
	}
	return append(out, el), nil
}

// appendText adds a text run, merging into the previous element when it is a
// text run with the same style.
func appendText(out []map[string]any, s string, st style) []map[string]any {
	if s == "" {
		return out
	}
	el := map[string]any{"type": "text", "text": s}
	if j := st.json(); j != nil {
		el["style"] = j
	}
	if n := len(out); n > 0 && sameTextStyle(out[n-1], el) {
		out[n-1]["text"] = out[n-1]["text"].(string) + s
		return out
	}
	return append(out, el)
}

func mergeText(elems []map[string]any) []map[string]any {
	var out []map[string]any
	for _, el := range elems {
		if n := len(out); n > 0 && el["type"] == "text" && sameTextStyle(out[n-1], el) {
			out[n-1]["text"] = out[n-1]["text"].(string) + el["text"].(string)
			continue
		}
		out = append(out, el)
	}
	return out
}

func sameTextStyle(a, b map[string]any) bool {
	if a["type"] != "text" || b["type"] != "text" {
		return false
	}
	as, _ := json.Marshal(a["style"])
	bs, _ := json.Marshal(b["style"])
	return bytes.Equal(as, bs)
}

// ensureTrailingNewline makes a section's element stream end with a text
// inline whose text ends in "\n", which is what stops a following list,
// quote, or code block from absorbing it (see validateBlockShapes). A
// trailing non-text inline gets a separate unstyled "\n" text element.
func ensureTrailingNewline(elems []map[string]any) []map[string]any {
	if n := len(elems); n > 0 && elems[n-1]["type"] == "text" {
		if t := elems[n-1]["text"].(string); strings.HasSuffix(t, "\n") {
			return elems
		}
		if elems[n-1]["style"] == nil {
			elems[n-1]["text"] = elems[n-1]["text"].(string) + "\n"
			return elems
		}
	}
	return append(elems, map[string]any{"type": "text", "text": "\n"})
}

// applyAbsorptionRule runs over the finished container sequence and
// terminates every rich_text_section that has a successor with a newline.
// For a following list, quote, or code block this is the absorption rule
// (the container would otherwise swallow the section). For a following
// section it preserves the Markdown paragraph break: Slack's own composer
// stores paragraph structure as "\n" inside text, so two bare adjacent
// sections are at best one line apart and at worst glued. Whether the
// trailing "\n" renders as a blank line or a line break in the compose box
// is pending a Desktop check (docs/draft-messages.md).
func (c *converter) applyAbsorptionRule() {
	for i := 0; i+1 < len(c.containers); i++ {
		if cur := c.containers[i]; cur["type"] == "rich_text_section" {
			cur["elements"] = ensureTrailingNewline(cur["elements"].([]map[string]any))
		}
	}
}

// lineOf finds the 1-based source line a node starts on: its first line
// segment, else its first descendant text, else the first non-blank line
// after the previous sibling (a thematic break owns no segment at all).
func (c *converter) lineOf(n ast.Node) int {
	if off, ok := firstOffset(n); ok {
		return c.lineOfOffset(off)
	}
	// The node starts at the first non-blank byte after whatever precedes
	// it: a previous sibling, or failing that an ancestor's previous sibling
	// (an empty first list item starts where the list starts). Asking the
	// parent for its own first offset would be wrong - that descends into a
	// later sibling of this node.
	for cur := n; cur != nil && cur.Kind() != ast.KindDocument; cur = cur.Parent() {
		for prev := cur.PreviousSibling(); prev != nil; prev = prev.PreviousSibling() {
			if end, ok := lastOffset(prev); ok {
				end = c.skipBlank(end)
				if _, fenced := prev.(*ast.FencedCodeBlock); fenced {
					// A fenced block's Lines() exclude the fences, so the
					// first non-blank line after them is the closing fence.
					end = c.skipBlank(c.skipLine(end))
				}
				return c.lineOfOffset(end)
			}
		}
	}
	return c.lineOfOffset(c.skipBlank(0))
}

func (c *converter) skipBlank(off int) int {
	for off < len(c.src) && (c.src[off] == ' ' || c.src[off] == '\t' || c.src[off] == '\n' || c.src[off] == '\r') {
		off++
	}
	return off
}

func (c *converter) skipLine(off int) int {
	for off < len(c.src) && c.src[off] != '\n' {
		off++
	}
	return off
}

func (c *converter) lineOfOffset(off int) int {
	if off > len(c.src) {
		off = len(c.src)
	}
	return 1 + bytes.Count(c.src[:off], []byte{'\n'})
}

func firstOffset(n ast.Node) (int, bool) {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Start, true
	case *token:
		return v.offset, true
	case *ast.RawHTML:
		if v.Segments.Len() > 0 {
			return v.Segments.At(0).Start, true
		}
	}
	if n.Type() == ast.TypeBlock {
		if lines := n.Lines(); lines != nil && lines.Len() > 0 {
			return lines.At(0).Start, true
		}
	}
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if off, ok := firstOffset(ch); ok {
			return off, true
		}
	}
	return 0, false
}

func lastOffset(n ast.Node) (int, bool) {
	for ch := n.LastChild(); ch != nil; ch = ch.PreviousSibling() {
		if off, ok := lastOffset(ch); ok {
			return off, true
		}
	}
	if n.Type() == ast.TypeBlock {
		if lines := n.Lines(); lines != nil && lines.Len() > 0 {
			return lines.At(lines.Len() - 1).Stop, true
		}
	}
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Stop, true
	}
	return 0, false
}
