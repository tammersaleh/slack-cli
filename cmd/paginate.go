package cmd

import (
	"context"
	"errors"
	"strconv"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// streamOption adjusts the trailer streamPages emits. Options are applied to
// every trailer, including the ones written on failure, so a marker can't be
// reported on a clean run and forgotten on a truncated one.
type streamOption func(*output.Meta)

// withClientSideFilter records that a client-side filter (--query,
// --has-unread) is active, so the trailer states whether that filter saw every
// page. Filtering is exhaustive only when the stream finished without error and
// Slack had no further pages: anything else means an empty result could be
// hiding matches the command never looked at.
func withClientSideFilter() streamOption {
	return func(m *output.Meta) {
		exhaustive := m.Error == "" && !m.HasMore
		m.FilterExhaustive = &exhaustive
	}
}

// streamPages drives the pagination loop shared by every list command: fetch
// a page, hand it to emit, and repeat while --all is set and Slack keeps
// returning a cursor.
//
// It exists so stdout ends with a _meta trailer even when a page fetch fails
// partway through. Without that trailer a listing cut short by a rate limit
// looks identical to a short but complete one, and a consumer that reads
// stdout without checking the exit code acts on truncated data. On failure
// the trailer carries the error code, and has_more plus the resume cursor
// when retrying that page could help. The command still writes the error to
// stderr and exits nonzero.
//
// Rate-limited pages are retried with Retry-After backoff (api.FetchPage), so
// a single 429 no longer ends the command.
//
// The trailer is not an absolute guarantee: nothing can be written after
// stdout itself fails.
//
// cursor is the caller's starting cursor and all reports whether --all was
// given. emit is output-only by contract: it may filter and convert items, but
// it must not make network calls or fail for domain reasons, because an error
// from it is taken as a broken stdout and returned with no trailer. Domain
// checks belong before the first PrintItem of a page.
func streamPages[T any](
	ctx context.Context,
	cli *CLI,
	p *output.Printer,
	endpoint string,
	cursor string,
	all bool,
	fetch api.PageFunc[T],
	emit func(items []T) error,
	opts ...streamOption,
) error {
	// Seeded with the starting cursor so a cycle back to it is caught on the
	// first lap rather than the second. Today that only engages for the
	// page-number commands, which always start at "1" - every command
	// rejects --all with --cursor, so a user-supplied resume point never
	// reaches an --all walk. Cheap insurance either way.
	seen := map[string]bool{}
	if cursor != "" {
		seen[cursor] = true
	}

	// printMeta applies every option before writing, so options see the final
	// has_more and error and can key off them.
	printMeta := func(meta output.Meta) error {
		for _, opt := range opts {
			opt(&meta)
		}
		return p.PrintMeta(meta)
	}

	for {
		items, next, err := api.FetchPage(ctx, endpoint, cursor, fetch)
		if err != nil {
			oErr, resumable := streamError(cli, err)
			meta := output.Meta{Error: oErr.Err}
			if resumable {
				// The resume point is cursor, not next: the page that
				// failed is the first one missing from stdout.
				meta.HasMore = true
				meta.NextCursor = cursor
			}
			// A trailer write that fails is dropped - the fetch failure
			// is the more useful error to report.
			_ = printMeta(meta)
			return oErr
		}

		if err := emit(items); err != nil {
			return err
		}

		if !all || next == "" {
			return printMeta(output.Meta{HasMore: next != "", NextCursor: next})
		}

		// A cursor that repeats would loop forever, and --timeout defaults
		// to off. Report it instead of hanging. No resume cursor: the one
		// Slack handed back is the very cursor that loops.
		if seen[next] {
			oErr := &output.Error{
				Err:      "pagination_error",
				Detail:   "Slack returned a cursor that repeats a previous page",
				Endpoint: endpoint,
				Code:     output.ExitGeneral,
			}
			_ = printMeta(output.Meta{HasMore: true, Error: oErr.Err})
			return oErr
		}
		seen[next] = true

		cursor = next
	}
}

// slackMaxPage is the highest page Slack's page-number APIs serve. Measured
// live 2026-11-10 on search.messages, search.files, and files.list: page 100
// answers normally and still reports further pages, and any page above it
// answers with page 1's rows and a next page of 2. Slack's docs state the cap
// for search ("the max page value is 100") but not the wrap.
const slackMaxPage = 100

// pageWalk is the per-command state of a walk over a page-number API.
// pageLimit builds the error for a page above slackMaxPage, so each endpoint
// can name its own way around the cap.
type pageWalk struct {
	pageLimit func(page int) *output.Error
}

// searchPageWalk is the walk for search.messages and search.files.
func searchPageWalk() *pageWalk {
	return &pageWalk{pageLimit: func(page int) *output.Error {
		return &output.Error{
			Err:    "search_page_limit",
			Detail: pageLimitDetail("search", page),
			Hint:   "Split the query into date windows with before:/after: and run each window with --all --limit 100. A window that still ends in this error holds more than 10,000 results - narrow it further.",
			Code:   output.ExitGeneral,
		}
	}}
}

// filePageWalk is the walk for files.list.
func filePageWalk() *pageWalk {
	return &pageWalk{pageLimit: func(page int) *output.Error {
		return &output.Error{
			Err:    "file_page_limit",
			Detail: pageLimitDetail("file listing", page),
			Hint:   "Narrow the listing with --channel, --user, or --types and run each with --all --limit 100.",
			Code:   output.ExitGeneral,
		}
	}}
}

func pageLimitDetail(what string, page int) string {
	return "Slack serves at most " + strconv.Itoa(slackMaxPage) + " pages of a " + what +
		" (10,000 results at --limit 100). Page " + strconv.Itoa(page) +
		" was not requested, because Slack answers it with page 1 again."
}

// pageCursorFetch adapts a page-number Slack API (files.list, search.messages,
// search.files) to api.PageFunc. The cursor is the page number as a string,
// which is exactly what these commands accept back via --cursor. The next page
// comes from the response's paging block rather than from incrementing a
// captured counter, so a retried attempt can't skip ahead.
//
// A page above slackMaxPage is refused before the request. The error is an
// *output.Error, so streamError ends the stream with a terminal trailer and no
// resume cursor: an --all walk stops after page 100 instead of appending a
// second copy of page 1, and a hand-written --cursor 101 fails instead of
// returning page 1 with exit 0.
func pageCursorFetch[T any](w *pageWalk, fetch func(page int) ([]T, *slack.Paging, error)) api.PageFunc[T] {
	return func(cursor string) ([]T, string, error) {
		page, err := parsePageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		if page > slackMaxPage {
			return nil, "", w.pageLimit(page)
		}
		items, paging, err := fetch(page)
		if err != nil {
			return nil, "", err
		}
		if paging == nil || paging.Page >= paging.Pages {
			return items, "", nil
		}
		return items, strconv.Itoa(paging.Page + 1), nil
	}
}

// streamError classifies a page-fetch failure and reports whether resuming
// from the failed page could help.
//
// An error a fetch closure raised itself is already an *output.Error - a
// thread that doesn't exist, a response that wouldn't parse. Those describe
// the request rather than a page that failed to arrive, so they pass through
// with their detail and hint intact and are not resumable.
//
// Everything else goes through ClassifyError, picking up the auth hint for this
// session. Those are resumable by default, because the cursor still names a
// real page and retrying it - after a wait, or after the caller repairs
// credentials or permissions - can work. The exception is a failure that voids
// the continuation itself: a cursor Slack rejected, a malformed request, a
// method that no longer exists. api.IsNonResumablePageError names those, and it
// is asked about the original error rather than the classified one, because
// resumability is its own policy and must not be inferred from the exit code.
func streamError(cli *CLI, err error) (oErr *output.Error, resumable bool) {
	if errors.As(err, &oErr) {
		return oErr, false
	}
	return cli.ClassifyError(err), !api.IsNonResumablePageError(err)
}
