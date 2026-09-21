package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// userResolver is the slice of *resolve.Resolver the query rewrite needs.
type userResolver interface {
	ResolveUser(ctx context.Context, input string) (string, error)
}

// userModifierPattern matches the search modifiers whose value names a
// person: from:, to:, and the DM form of in:. Group 1 is an optional
// negation, 2 the key as typed, 3 the raw value.
var userModifierPattern = regexp.MustCompile(`^(-?)((?i:from|to|in)):(.+)$`)

// modifierShapedPattern matches any token that starts a new search modifier
// (`key:value`), which ends an unquoted multi-word name.
var modifierShapedPattern = regexp.MustCompile(`^-?[A-Za-z_]+:\S`)

// rewriteUserModifiers resolves every `@name` value of a from:/to:/in:
// modifier to Slack's `<@Uxxx>` mention form before the query ships.
//
// Slack's search API accepts `from:@handle` and `from:<@Uxxx>` but silently
// matches nothing on `from:@Real Name`, `from:@Uxxx`, or `from:"Real Name"`
// (verified live 2026-09-21), and the CLI's help advertises `from:@user`
// without saying which. Resolving through the user cache accepts handle,
// display name, real name, email, and ID alike and turns a name Slack
// would drop on the floor into either the working form or a loud
// user_not_found. A quoted from:/to: value (`from:"Alice Adams"`) is a
// name whether or not it carries the `@`; a quoted in: value without `@`
// may be a channel and passes through.
//
// An unquoted name runs from the `@word` to the next modifier-shaped or
// quoted token; the longest prefix of that span that resolves wins, so
// `from:@Alice Adams skypilot` works without quotes. Quoted values
// (`from:"@Alice Adams"`, `from:@"Alice Adams"`) are taken whole. Values
// not starting with `@` (`in:#general`, `from:<@U01XYZ>`) pass through.
// Tokens are re-joined with single spaces; Slack search ignores the
// difference.
func rewriteUserModifiers(ctx context.Context, r userResolver, query string) (string, error) {
	toks := splitQueryTokens(query)
	out := make([]string, 0, len(toks))
	for i := 0; i < len(toks); i++ {
		m := userModifierPattern.FindStringSubmatch(toks[i])
		if m == nil {
			out = append(out, toks[i])
			continue
		}
		neg, key, val := m[1], m[2], m[3]

		// Both `"@name"` and `@"name"` are quoted names.
		quoted := false
		if n := len(val); n >= 2 && val[n-1] == '"' {
			switch {
			case val[0] == '"':
				val, quoted = val[1:n-1], true
			case strings.HasPrefix(val, `@"`):
				val, quoted = "@"+val[2:n-1], true
			}
		}
		// A quoted from:/to: value can only name a person, so the `@` is
		// optional there. A quoted in: value may be a channel, so it keeps
		// the `@` requirement.
		if quoted && !strings.HasPrefix(val, "@") && !strings.EqualFold(key, "in") {
			val = "@" + val
		}
		if !strings.HasPrefix(val, "@") || len(val) == 1 {
			out = append(out, toks[i])
			continue
		}
		name := val[1:]

		if quoted {
			id, err := r.ResolveUser(ctx, name)
			if err != nil {
				return "", searchUserNotFound("@" + name)
			}
			out = append(out, fmt.Sprintf("%s%s:<@%s>", neg, key, id))
			continue
		}

		// Unquoted: the name may continue across following tokens.
		end := i + 1
		for end < len(toks) && !modifierShapedPattern.MatchString(toks[end]) && !strings.HasPrefix(toks[end], `"`) {
			end++
		}
		span := toks[i+1 : end]
		resolved := false
		for n := len(span); n >= 0; n-- {
			candidate := strings.Join(append([]string{name}, span[:n]...), " ")
			id, err := r.ResolveUser(ctx, candidate)
			if err != nil {
				continue
			}
			out = append(out, fmt.Sprintf("%s%s:<@%s>", neg, key, id))
			i += n
			resolved = true
			break
		}
		if !resolved {
			return "", searchUserNotFound("@" + strings.Join(append([]string{name}, span...), " "))
		}
	}
	return strings.Join(out, " "), nil
}

// searchUserNotFound is user_not_found with a search-specific hint: the
// standard hint tells the caller how to find the user, this one also says how
// to bypass resolution.
func searchUserNotFound(input string) *output.Error {
	e := output.UserNotFound(input)
	e.Detail = fmt.Sprintf("No user matching '%s' for a from:/to:/in: search modifier", input)
	e.Hint = "Quote multi-word names (from:\"@Alice Adams\") or pass the user ID as from:<@U01XYZ>. " + e.Hint
	return e
}

// splitQueryTokens splits a search query on whitespace, keeping a
// double-quoted span (including any `key:` prefix glued to its opening
// quote) as one token. An unterminated quote runs to the end of the query.
func splitQueryTokens(q string) []string {
	var toks []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, c := range q {
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteRune(c)
		case !inQuote && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return toks
}
