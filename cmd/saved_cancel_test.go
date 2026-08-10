package cmd_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// cancelRunSavedList drives the CLI against an already-running server instead of
// standing one up itself.
//
// runWithMockSession cannot be used for a test that reasons about what the
// handlers were doing when Run returned: it closes its httptest.Server on the
// way out, and Close blocks until every handler has returned. By the time it
// hands back, a handler held open by the command is finished no matter what the
// command did. Owning the server in the test body keeps it alive across the
// assertions.
func cancelRunSavedList(t *testing.T, apiURL string, args ...string) (string, error) {
	t.Helper()

	isolateTestEnv(t)
	t.Setenv("SLACK_TOKEN", "xoxc-test")
	t.Setenv("SLACK_API_URL", apiURL)

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	cli.SetOutput(&outBuf, &errBuf)
	runErr := kctx.Run(&cli)
	return outBuf.String(), runErr
}

// TestSavedListEnrich_FirstCauseSurvivesSiblingCancel forces the race that
// enrichItems' sync.Once and page-scoped cancel exist for: one lookup is still
// in flight on the server when a sibling fails systemically, so the sibling that
// gets woken reports context.Canceled while the real cause is missing_scope.
//
// No other test sets this up. TestSavedListEnrich_SystemicErrorsAreFatal claims
// the first-cause property in its comment but cannot observe it - its healthy
// lookup answers immediately, so nothing is ever woken by the cancel and there
// is no second error that could win. `go test -race` cannot prove it either: it
// proves the absence of data races, not that the correct error survives one.
//
// The wrong-but-plausible implementations this rules out:
//
//   - failPage assigning fatalErr on every call instead of under sync.Once. The
//     woken sibling's context.Canceled classifies as unknown_error, and the page
//     would report that in place of missing_scope - the real cause, and the only
//     one that tells the user what to fix. The overwrite is not a coin flip: the
//     sibling can only fail because of the cancel, so its call to failPage always
//     lands second and always wins.
//   - failPage recording the error without cancelling the page context, leaving
//     siblings to spend their requests on a page nobody will emit. Then nothing
//     releases the held lookup and the command blocks for the full hold below.
//   - classifying the fatal error before handing it to streamPages, which would
//     make the trailer terminal and drop the resume cursor.
//
// Deliberately not asserted: that the held handler had already exited when Run
// returned, the observable side of enrichItems' wg.Wait. It cannot be tested
// this way. Once the page fails, every sibling is torn down by the cancel within
// microseconds, so the join window is the gap between "the client closed the
// connection" and "the server noticed", and that gap is shorter than the command
// takes to write its trailer. Measured over 200 runs on the correct
// implementation, a non-blocking check for the handler's exit at that instant
// failed 77 times; with the wg.Wait removed it failed 178 times. It fails both
// ways, so it discriminates nothing - do not add it back.
func TestSavedListEnrich_FirstCauseSurvivesSiblingCancel(t *testing.T) {
	// How long the held handler waits for its cancellation before giving up. It
	// doubles as the deadline for the whole command: nothing may block that long.
	const hold = 2 * time.Second

	// rootTS is the lookup held open; replyTS is the one that fails.
	heldStarted := make(chan struct{})
	heldCancelled := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list",
		savedListHandler(t, savedItemsFor(rootTS, replyTS), map[string]any{"total": 2}, ""))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call to %s", r.URL.Path)
		slackErr(w, "unexpected_call")
	})
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		// ParseForm does more here than read the ts. The server starts the
		// background read that notices a client hanging up only once the request
		// body has hit EOF, so a handler that never drains the body would sit in
		// the select below until the hold expires even though the client is gone.
		_ = r.ParseForm()

		switch r.FormValue("latest") {
		case rootTS:
			close(heldStarted)
			// Every wait in this test is bounded. httptest.Server.Close waits for
			// handlers and the server does not reliably notice a client
			// abandoning an unanswered request, so an unbounded receive here
			// would turn a regression into a hung suite instead of a failure.
			select {
			case <-r.Context().Done():
				close(heldCancelled)
			case <-time.After(hold):
				t.Error("the in-flight lookup was never cancelled after its sibling failed systemically")
			}
			// No response body: the client has already given up on this request.
		case replyTS:
			// Fail only once the sibling lookup is genuinely in flight. Without
			// this the sibling could finish first and there would be no
			// cancellation to lose the first cause to.
			select {
			case <-heldStarted:
			case <-time.After(hold):
				t.Error("timed out waiting for the sibling lookup to reach the server")
			}
			slackErr(w, "missing_scope")
		default:
			t.Errorf("unexpected conversations.history for ts %q", r.FormValue("latest"))
			slackErr(w, "unexpected_call")
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	start := time.Now()
	out, err := cancelRunSavedList(t, srv.URL+"/api/", "saved", "list", "--enrich")
	elapsed := time.Since(start)

	// The first substantive cause, not the cancellation it caused.
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "missing_scope" {
		t.Fatalf("error = %q, want missing_scope (the woken sibling's error overwrote the first cause?)",
			oErr.Err)
	}

	// The cancel reached a request that was already in flight, rather than the
	// command abandoning it silently or the server timing it out.
	select {
	case <-heldCancelled:
	case <-time.After(hold):
		t.Error("the page-scoped cancel never reached the in-flight request")
	}
	// And the cancel is what ended the run: with the fatal error recorded but no
	// cancel, nothing releases the held lookup until the hold expires, so the
	// command would take at least that long to return.
	if elapsed >= hold/2 {
		t.Errorf("Run took %s, want well under the %s hold (the in-flight lookup was waited out, not cancelled)",
			elapsed, hold)
	}

	// Page-atomic: no rows, exactly one trailer, and it carries the real cause.
	meta := assertFatalPage(t, out, "missing_scope", 0)
	// The error reaches streamPages unclassified, so the page stays resumable.
	if meta["has_more"] != true {
		t.Errorf("_meta.has_more = %v, want true (enrich errors must stay resumable)", meta["has_more"])
	}
}
