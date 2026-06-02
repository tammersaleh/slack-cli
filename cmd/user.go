package cmd

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
)

type UserCmd struct {
	List         UserListCmd         `cmd:"" help:"List users."`
	Info         UserInfoCmd         `cmd:"" help:"Show user details."`
	ManagerChain UserManagerChainCmd `cmd:"" name:"manager-chain" help:"Walk the management chain upward from a user."`
}

type UserListCmd struct {
	Limit    int    `help:"Page size." default:"100"`
	Cursor   string `help:"Continue from previous page."`
	All      bool   `help:"Fetch all pages."`
	Query    string `help:"Filter by name or email substring (client-side)."`
	Presence bool   `help:"Include presence information."`
}

func (c *UserListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	limit := c.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	// slack-go's users.list uses its own pagination. We use GetUsersPaginated
	// for single-page control.
	pag := client.Bot().GetUsersPaginated(
		slack.GetUsersOptionLimit(limit),
		slack.GetUsersOptionPresence(c.Presence),
	)

	if c.Cursor != "" {
		pag.Cursor = c.Cursor
	}

	for {
		var fetchErr error
		pag, fetchErr = pag.Next(ctx)
		if pag.Done(fetchErr) {
			return p.PrintMeta(output.Meta{})
		}
		if fetchErr != nil {
			return cli.ClassifyError(fetchErr)
		}

		for _, user := range pag.Users {
			if c.Query != "" && !matchesUserQuery(user, c.Query) {
				continue
			}
			if err := p.PrintItem(userToMap(user)); err != nil {
				return err
			}
		}

		nextCursor := pag.Cursor
		if !c.All || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    nextCursor != "",
				NextCursor: nextCursor,
			})
		}
	}
}

type UserInfoCmd struct {
	Users []string `arg:"" required:"" help:"User ID or email."`
	Full  bool     `help:"Fetch custom profile fields (Manager, Division, Department, etc.) via users.profile.get."`
}

func (UserInfoCmd) Help() string {
	return `Look up one or more users by ID, email, or @display-name.

Accepted forms:
  U01ABC123               Slack user ID
  alice@example.com       email (uses users.lookupByEmail - fails with
                          session tokens on Enterprise Grid; prefer @name there)
  @alice                  display name, username, or real name via local cache

Examples:

  slack user info U01ABC123 U01DEF456
  slack user info alice@example.com
  slack user info @alice

Per-user failures emit {input, error:"user_not_found", hint:...} rows
on stdout and set _meta.error_count in the trailer.`
}

func (c *UserInfoCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	for _, input := range c.Users {
		userID, err := r.ResolveUser(ctx, input)
		if err != nil {
			errorCount++
			if err := p.PrintItem(userResolveError(input, err).AsItem()); err != nil {
				return err
			}
			continue
		}

		// Try cached profile first, fall back to direct API call.
		user, found, lookupErr := r.LookupUser(ctx, userID)
		if lookupErr != nil || !found {
			apiUser, err := client.Bot().GetUserInfoContext(ctx, userID)
			if err != nil {
				errorCount++
				if err := p.PrintItem(output.UserNotFound(input).AsItem()); err != nil {
					return err
				}
				continue
			}
			user = *apiUser
		}

		m := userToMap(user)
		m["input"] = input

		if c.Full {
			profile, err := client.Bot().GetUserProfileContext(ctx, &slack.GetUserProfileParameters{
				UserID:        userID,
				IncludeLabels: true,
			})
			if err != nil {
				// Systemic failures (auth, missing scope, rate limit) abort the
				// whole command rather than repeating per input.
				if cErr := cli.ClassifyError(err); isSystemicErr(cErr) {
					return cErr
				}
				errorCount++
				if err := p.PrintItem(output.UserNotFound(input).AsItem()); err != nil {
					return err
				}
				continue
			}
			m["custom_fields"] = buildCustomFields(profile, func(id string) (string, bool) {
				u, found, lookupErr := r.LookupUser(ctx, id)
				if lookupErr != nil || !found {
					return "", false
				}
				return userDisplayName(u), true
			})
		}

		if err := p.PrintItem(m); err != nil {
			return err
		}
	}

	meta := output.Meta{ErrorCount: errorCount}
	if err := p.PrintMeta(meta); err != nil {
		return err
	}
	if errorCount > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

func userToMap(u slack.User) map[string]any { return toMap(u) }

func matchesUserQuery(u slack.User, query string) bool {
	q := strings.ToLower(strings.TrimPrefix(query, "@"))
	return strings.Contains(strings.ToLower(u.Name), q) ||
		strings.Contains(strings.ToLower(u.RealName), q) ||
		strings.Contains(strings.ToLower(u.Profile.Email), q) ||
		strings.Contains(strings.ToLower(u.Profile.DisplayName), q)
}

// userDisplayName prefers a user's display name, falling back to real name
// then the @handle.
func userDisplayName(u slack.User) string {
	if u.Profile.DisplayName != "" {
		return u.Profile.DisplayName
	}
	if u.RealName != "" {
		return u.RealName
	}
	return u.Name
}

// isSystemicErr reports whether a classified error reflects a whole-command
// problem (auth, missing scope, rate limit) rather than one bad input, so the
// caller can fail fast instead of emitting the same error per input.
func isSystemicErr(e *output.Error) bool {
	if e == nil {
		return false
	}
	if e.Code == output.ExitAuth {
		return true
	}
	switch e.Err {
	case "missing_scope", "ratelimited", "rate_limited", "account_inactive", "token_revoked":
		return true
	}
	return false
}

// customField is a single labeled custom profile field in --full output.
type customField struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Value     string `json:"value"`
	Alt       string `json:"alt,omitempty"`
	ValueName string `json:"value_name,omitempty"`
}

var fieldKeyNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeFieldKey turns a human field label into a snake_case JSON key.
func normalizeFieldKey(label string) string {
	return strings.Trim(fieldKeyNonAlnum.ReplaceAllString(strings.ToLower(label), "_"), "_")
}

// buildCustomFields converts a profile's custom fields into a label-keyed
// object. Empty values are dropped. User-ID-shaped values are resolved to a
// display name via resolveName (best-effort). Keys are assigned deterministically
// (sorted by field ID) so collision suffixes (_2, _3) don't flap across runs.
func buildCustomFields(profile *slack.UserProfile, resolveName func(id string) (string, bool)) map[string]customField {
	raw := profile.FieldsMap()
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make(map[string]customField, len(ids))
	for _, id := range ids {
		f := raw[id]
		if f.Value == "" {
			continue
		}
		cf := customField{ID: id, Label: f.Label, Value: f.Value, Alt: f.Alt}
		if resolve.IsUserID(f.Value) {
			if name, ok := resolveName(f.Value); ok {
				cf.ValueName = name
			}
		}

		key := normalizeFieldKey(f.Label)
		if key == "" {
			key = "field_" + id
		}
		if _, taken := out[key]; taken {
			for n := 2; ; n++ {
				alt := key + "_" + strconv.Itoa(n)
				if _, taken := out[alt]; !taken {
					key = alt
					break
				}
			}
		}
		out[key] = cf
	}
	return out
}

// UserManagerChainCmd walks the management chain upward from each user,
// emitting one row per level. Slack stores no downward (direct-reports) data
// reliably, so traversal is upward-only.
type UserManagerChainCmd struct {
	Users        []string `arg:"" required:"" help:"User ID, email, or @display-name."`
	ManagerField string   `name:"manager-field" default:"Manager" help:"Custom profile field label that holds the manager reference."`
}

func (UserManagerChainCmd) Help() string {
	return `Walk the management chain upward from one or more users.

Each level is one JSONL row: {input, root_user_id, level, id, display_name,
real_name, title, manager_id, manager_name}. The terminal row carries a
stop_reason:

  no_manager                          no manager field set (not an error)
  invalid_manager_value               manager field isn't a user ID (error)
  ambiguous_manager_field             >1 field matches --manager-field (error)
  cycle_detected                      a manager loop was hit (error)
  max_depth                           chain exceeded the depth cap (error)
  profile_lookup_failed               a hop's profile fetch failed (error)

Error stop_reasons increment _meta.error_count and set a nonzero exit code.
The manager reference is read from the "Manager" custom profile field by
default; override the label with --manager-field.`
}

const managerChainMaxDepth = 20

// chainRow is one level of a management chain.
type chainRow struct {
	input      string
	rootUserID string
	level      int
	id         string
	profile    *slack.UserProfile
	managerID  string
	stopReason string
}

func (c *UserManagerChainCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	// Command-local profile cache so a manager shared across inputs (or hit
	// twice) is fetched once.
	cache := map[string]*slack.UserProfile{}
	getProfile := func(id string) (*slack.UserProfile, error) {
		if pr, ok := cache[id]; ok {
			return pr, nil
		}
		pr, err := client.Bot().GetUserProfileContext(ctx, &slack.GetUserProfileParameters{
			UserID:        id,
			IncludeLabels: true,
		})
		if err != nil {
			return nil, err
		}
		cache[id] = pr
		return pr, nil
	}

	for _, input := range c.Users {
		rootID, err := r.ResolveUser(ctx, input)
		if err != nil {
			errorCount++
			if err := p.PrintItem(userResolveError(input, err).AsItem()); err != nil {
				return err
			}
			continue
		}

		rows, fatal, hadError := c.walk(getProfile, cli, input, rootID)
		if fatal != nil {
			return fatal
		}
		if hadError {
			errorCount++
		}
		for _, row := range rows {
			if err := p.PrintItem(row); err != nil {
				return err
			}
		}
	}

	if err := p.PrintMeta(output.Meta{ErrorCount: errorCount}); err != nil {
		return err
	}
	if errorCount > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

// walk traverses the manager chain from rootID. It returns the emitted rows,
// a fatal error (systemic failure that should abort the whole command), and
// whether the chain ended on an error stop_reason (counts toward error_count).
func (c *UserManagerChainCmd) walk(
	getProfile func(string) (*slack.UserProfile, error),
	cli *CLI,
	input, rootID string,
) (rows []map[string]any, fatal *output.Error, hadError bool) {
	var chain []chainRow
	seen := map[string]bool{}
	cur := rootID

	for {
		profile, err := getProfile(cur)
		if err != nil {
			if cErr := cli.ClassifyError(err); isSystemicErr(cErr) {
				return nil, cErr, false
			}
			if len(chain) == 0 {
				// Root lookup failed and it's not systemic: per-item error.
				return []map[string]any{output.UserNotFound(input).AsItem()}, nil, true
			}
			chain[len(chain)-1].stopReason = "profile_lookup_failed"
			break
		}

		row := chainRow{input: input, rootUserID: rootID, level: len(chain), id: cur, profile: profile}
		seen[cur] = true

		mgrID, status := managerReference(profile, c.ManagerField)
		switch status {
		case mgrAbsent:
			row.stopReason = "no_manager"
			chain = append(chain, row)
		case mgrAmbiguous:
			row.stopReason = "ambiguous_manager_field"
			chain = append(chain, row)
		case mgrInvalid:
			row.stopReason = "invalid_manager_value"
			chain = append(chain, row)
		case mgrOK:
			row.managerID = mgrID
			chain = append(chain, row)
		}
		if status != mgrOK {
			break
		}

		if len(chain) >= managerChainMaxDepth {
			chain[len(chain)-1].stopReason = "max_depth"
			break
		}
		if seen[mgrID] {
			chain[len(chain)-1].stopReason = "cycle_detected"
			break
		}
		cur = mgrID
	}

	return formatChain(chain)
}

// formatChain converts internal chain rows to output maps, filling each row's
// manager_name from the next hop's profile, and reports whether any row ended
// on an error stop_reason.
func formatChain(chain []chainRow) (rows []map[string]any, fatal *output.Error, hadError bool) {
	for i, row := range chain {
		m := map[string]any{
			"input":        row.input,
			"root_user_id": row.rootUserID,
			"level":        row.level,
			"id":           row.id,
		}
		if row.profile != nil {
			m["display_name"] = row.profile.DisplayName
			m["real_name"] = row.profile.RealName
			m["title"] = row.profile.Title
		}
		if row.managerID != "" {
			m["manager_id"] = row.managerID
			// The next chain entry is this row's manager; use its name.
			if i+1 < len(chain) && chain[i+1].profile != nil {
				m["manager_name"] = userProfileName(chain[i+1].profile)
			}
		}
		if row.stopReason != "" {
			m["stop_reason"] = row.stopReason
			if isErrorStopReason(row.stopReason) {
				hadError = true
			}
		}
		rows = append(rows, m)
	}
	return rows, nil, hadError
}

func isErrorStopReason(reason string) bool {
	switch reason {
	case "invalid_manager_value", "ambiguous_manager_field", "cycle_detected", "max_depth", "profile_lookup_failed":
		return true
	}
	return false
}

// userProfileName prefers display name, then real name.
func userProfileName(p *slack.UserProfile) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.RealName
}

type managerStatus int

const (
	mgrOK managerStatus = iota
	mgrAbsent
	mgrInvalid
	mgrAmbiguous
)

// managerReference finds the manager user ID from a profile's custom fields by
// matching the given label (case-insensitive). Returns the value and a status
// distinguishing absent / ambiguous (>1 match) / invalid (not a user ID).
func managerReference(profile *slack.UserProfile, label string) (string, managerStatus) {
	want := strings.ToLower(strings.TrimSpace(label))
	var values []string
	for _, f := range profile.FieldsMap() {
		if strings.ToLower(strings.TrimSpace(f.Label)) == want && f.Value != "" {
			values = append(values, f.Value)
		}
	}
	switch len(values) {
	case 0:
		return "", mgrAbsent
	case 1:
		if !resolve.IsUserID(values[0]) {
			return values[0], mgrInvalid
		}
		return values[0], mgrOK
	default:
		return "", mgrAmbiguous
	}
}
