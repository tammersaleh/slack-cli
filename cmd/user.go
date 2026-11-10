package cmd

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
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
	Query    string `help:"Filter by name or email substring (client-side; searches one page - pair with --all to search every page, and check filter_exhaustive in the trailer)."`
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

	// slack-go's users.list carries its pagination state in the paginator
	// value. Build a fresh one per attempt with every option reapplied, so a
	// retried page repeats the same request instead of restarting at page 1.
	fetch := func(cursor string) ([]slack.User, string, error) {
		pag := client.Bot().GetUsersPaginated(
			slack.GetUsersOptionLimit(limit),
			slack.GetUsersOptionPresence(c.Presence),
			slack.GetUsersOptionCursor(cursor),
		)
		pag, err := pag.Next(ctx)
		if err != nil {
			if pag.Done(err) {
				return nil, "", nil
			}
			return nil, "", err
		}
		return pag.Users, pag.Cursor, nil
	}

	// --query filters client-side, so the trailer has to say whether it saw
	// every page. Unlike `channel list`, this command does not widen to a full
	// walk on its own: users.list has no member-scoped equivalent, and a whole
	// directory is 72 Tier-2 requests on a large org - too slow to trigger
	// behind the caller's back. --all remains theirs to pass.
	var opts []streamOption
	if c.Query != "" {
		opts = append(opts, withClientSideFilter())
	}

	return streamPages(ctx, cli, p, "users.list", c.Cursor, c.All, fetch, func(users []slack.User) error {
		for _, user := range users {
			if c.Query != "" && !matchesUserQuery(user, c.Query) {
				continue
			}
			if err := p.PrintItem(userToMap(user)); err != nil {
				return err
			}
		}
		return nil
	}, opts...)
}

type UserInfoCmd struct {
	Users []string `arg:"" required:"" help:"User ID, email, or @name."`
	Full  bool     `help:"Fetch custom profile fields (Manager, Division, Department, etc.) via users.profile.get."`
}

func (UserInfoCmd) Help() string {
	return `Look up one or more users by ID, email, or @display-name.

Accepted forms:
  U01ABC123               Slack user ID
  alice@example.com       email, read from the local user cache; an email
                          the cache does not hold falls back to
                          users.lookupByEmail (which fails with session
                          tokens on Enterprise Grid)
  @alice                  display name, username, or real name via local cache

Email and @name need the whole user directory. When the user cache is
missing or older than its TTL (24h, SLACK_CACHE_TTL), the lookup first
walks users.list to rebuild it, which prints nothing until it finishes and
can take minutes on a large org. --trace shows each page and rate-limit
wait. A user ID never triggers the walk.

Examples:

  slack user info U01ABC123 U01DEF456
  slack user info alice@example.com
  slack user info @alice

Per-user failures emit {input, error, detail, hint} rows on stdout and
set _meta.error_count in the trailer. error is "user_not_found" when the
user does not exist, otherwise Slack's own code.

A failure that is not about one user (auth, missing_scope, a rate limit
that outlasts its retries, network, --timeout) stops the run. Stdout then
ends with {"_meta":{"has_more":true,"error":"<code>"}}: the rows printed
are good, the inputs with no row were not looked up. Rerun those.

--full fetches users.profile.get per user, which Slack rate-limits after a
few calls. The fetch is retried, but a long list can still stop early.`
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
			if fatal := systemicResolveError(cli, err); fatal != nil {
				return abortWithTrailer(p, fatal, errorCount)
			}
			errorCount++
			if err := p.PrintItem(userResolveError(input, err).AsItem()); err != nil {
				return err
			}
			continue
		}

		user, err := r.FetchUser(ctx, userID)
		if err != nil {
			if abort := userLookupFailed(cli, p, input, err, &errorCount); abort != nil {
				return abort
			}
			continue
		}

		m := userToMap(user)
		m["input"] = input

		if c.Full {
			profile, err := fetchUserProfile(ctx, client, userID)
			if err != nil {
				if abort := userLookupFailed(cli, p, input, err, &errorCount); abort != nil {
					return abort
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

// userLookupFailed reports one failed user lookup. A systemic failure ends
// the run behind a trailer and comes back as the error to return; anything
// else prints this input's error row, counts it, and returns nil.
func userLookupFailed(cli *CLI, p *output.Printer, input string, err error, errorCount *int) error {
	cErr := cli.ClassifyError(err)
	if isSystemicErr(cErr) {
		return abortWithTrailer(p, cErr, *errorCount)
	}
	*errorCount++
	return p.PrintItem(userLookupError(input, cErr).AsItem())
}

// fetchUserProfile reads one user's profile with custom-field labels through
// the shared rate-limit retry. users.profile.get limits after a handful of
// calls and reports it in the body with no Retry-After, so each wait is the
// retry loop's floor.
func fetchUserProfile(ctx context.Context, client *api.Client, userID string) (*slack.UserProfile, error) {
	var profile *slack.UserProfile
	err := api.Call(ctx, "users.profile.get", func() error {
		var err error
		profile, err = client.Bot().GetUserProfileContext(ctx, &slack.GetUserProfileParameters{
			UserID:        userID,
			IncludeLabels: true,
		})
		return err
	})
	return profile, err
}

// isSystemicErr reports whether a classified error reflects a whole-command
// problem rather than one bad input, so the caller can fail fast instead of
// emitting the same error per input: the credential (auth, missing scope), a
// rate limit that outlasted its retries, the network, or the --timeout budget.
func isSystemicErr(e *output.Error) bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case output.ExitAuth, output.ExitRateLimit, output.ExitNetwork:
		return true
	}
	return e.Err == "timeout"
}

// systemicResolveError returns the classified error when resolving an input
// failed for a whole-command reason, or nil when it is this input's problem.
// Only the email path surfaces one: users.lookupByEmail's error comes back
// raw, while the @name path reports every failure as not found (see
// todo/resolver-errors-are-reported-as-channel-not-found.md).
func systemicResolveError(cli *CLI, err error) *output.Error {
	if isBadURLErr(err) {
		return nil
	}
	if cErr := cli.ClassifyError(err); isSystemicErr(cErr) {
		return cErr
	}
	return nil
}

// userNotFoundCodes are Slack's ways of saying the user does not exist or is
// not visible to this token.
var userNotFoundCodes = map[string]bool{
	"user_not_found":   true,
	"users_not_found":  true,
	"user_not_visible": true,
}

// userLookupError is the per-input row for a failed user lookup. Only a
// not-found answer becomes user_not_found, with its discovery hint; any other
// failure keeps its own code, so a user whose fetch hit a transient Slack
// error is not reported as nonexistent.
func userLookupError(input string, cErr *output.Error) *output.Error {
	if userNotFoundCodes[cErr.Err] {
		return output.UserNotFound(input)
	}
	row := *cErr
	row.Input = input
	return &row
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
		pr, err := fetchUserProfile(ctx, client, id)
		if err != nil {
			return nil, err
		}
		cache[id] = pr
		return pr, nil
	}

	for _, input := range c.Users {
		rootID, err := r.ResolveUser(ctx, input)
		if err != nil {
			if fatal := systemicResolveError(cli, err); fatal != nil {
				return abortWithTrailer(p, fatal, errorCount)
			}
			errorCount++
			if err := p.PrintItem(userResolveError(input, err).AsItem()); err != nil {
				return err
			}
			continue
		}

		rows, fatal, hadError := c.walk(getProfile, cli, input, rootID)
		if fatal != nil {
			return abortWithTrailer(p, fatal, errorCount)
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
			cErr := cli.ClassifyError(err)
			if isSystemicErr(cErr) {
				return nil, cErr, false
			}
			if len(chain) == 0 {
				// Root lookup failed and it's not systemic: per-item error.
				return []map[string]any{userLookupError(input, cErr).AsItem()}, nil, true
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
