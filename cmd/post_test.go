package cmd_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
)

func TestMessagePost_Text(t *testing.T) {
	var gotText, gotChannel string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotText = r.FormValue("text")
		gotChannel = r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": gotChannel,
			"ts":      "1709251200.000100",
		})
	})

	out, err := runWithMock(t, mux, "message", "post", "C01ABC", "--text", "Deploy complete")
	if err != nil {
		t.Fatal(err)
	}

	if gotText != "Deploy complete" {
		t.Errorf("expected text='Deploy complete', got %q", gotText)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("expected channel='C01ABC', got %q", gotChannel)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (result + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["channel"] != "C01ABC" {
		t.Errorf("expected channel='C01ABC', got %q", item["channel"])
	}
	if item["ts"] != "1709251200.000100" {
		t.Errorf("expected ts, got %q", item["ts"])
	}
	if _, ok := item["ts_iso"]; !ok {
		t.Error("expected ts_iso field")
	}
}

func TestMessagePost_Thread(t *testing.T) {
	var gotThreadTS string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotThreadTS = r.FormValue("thread_ts")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": "C01ABC",
			"ts":      "1709251200.000200",
		})
	})

	out, err := runWithMock(t, mux, "message", "post", "C01ABC", "--text", "reply", "--thread", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	if gotThreadTS != "1709251200.000100" {
		t.Errorf("expected thread_ts='1709251200.000100', got %q", gotThreadTS)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
}

func TestMessagePost_ChannelResolution(t *testing.T) {
	var gotChannel string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01ABC", "name": "general", "is_member": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": gotChannel,
			"ts":      "1709251200.000100",
		})
	})

	_, err := runWithMock(t, mux, "message", "post", "#general", "--text", "hello")
	if err != nil {
		t.Fatal(err)
	}

	if gotChannel != "C01ABC" {
		t.Errorf("expected resolved channel C01ABC, got %q", gotChannel)
	}
}

func TestMessagePost_MissingText(t *testing.T) {
	mux := http.NewServeMux()
	r := runWithMockFull(t, mux, "message", "post", "C01ABC")
	if r.err == nil {
		t.Fatal("expected error when no --text or --stdin")
	}
	if !strings.Contains(r.err.Error(), "missing_text") {
		t.Errorf("expected missing_text error, got %v", r.err)
	}
}

func TestMessagePost_TextAndStdinExclusive(t *testing.T) {
	mux := http.NewServeMux()
	r := runWithMockFull(t, mux, "message", "post", "C01ABC", "--text", "foo", "--stdin")
	if r.err == nil {
		t.Fatal("expected error for --text and --stdin together")
	}
	if !strings.Contains(r.err.Error(), "invalid_input") {
		t.Errorf("expected invalid_input error, got %v", r.err)
	}
}

func TestMessagePost_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "not_authed",
		})
	})

	_, err := runWithMock(t, mux, "message", "post", "C01ABC", "--text", "hello")
	if err == nil {
		t.Fatal("expected error for not_authed")
	}
}

func TestMessagePost_Stdin(t *testing.T) {
	var gotText string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotText = r.FormValue("text")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": "C01ABC",
			"ts":      "1709251200.000100",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("SLACK_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse([]string{"message", "post", "C01ABC", "--stdin"})
	if err != nil {
		t.Fatal(err)
	}

	cli.SetOutput(&outBuf, &errBuf)
	cli.SetInput(strings.NewReader("Hello from stdin\n"))

	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}

	if gotText != "Hello from stdin" {
		t.Errorf("expected text='Hello from stdin', got %q", gotText)
	}
}

func TestMessagePost_StdinTrimsCarriageReturn(t *testing.T) {
	var gotText string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotText = r.FormValue("text")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": "C01ABC",
			"ts":      "1709251200.000100",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("SLACK_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse([]string{"message", "post", "C01ABC", "--stdin"})
	if err != nil {
		t.Fatal(err)
	}

	cli.SetOutput(&outBuf, &errBuf)
	cli.SetInput(strings.NewReader("Windows text\r\n"))

	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}

	if gotText != "Windows text" {
		t.Errorf("expected text='Windows text', got %q", gotText)
	}
}
