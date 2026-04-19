package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

func main() {
	var cli cmd.CLI
	ctx := kong.Parse(&cli,
		kong.Name("slack"),
		kong.Description("CLI for Slack."),
		kong.UsageOnError(),
	)

	err := ctx.Run(&cli)
	if err == nil {
		return
	}

	var exitErr *output.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}

	var oErr *output.Error
	if errors.As(err, &oErr) {
		_ = json.NewEncoder(os.Stderr).Encode(oErr)
		os.Exit(oErr.Code)
	}

	// Unstructured errors: wrap in JSON for consistency.
	_ = json.NewEncoder(os.Stderr).Encode(map[string]string{
		"error":  "general_error",
		"detail": err.Error(),
	})
	os.Exit(output.ExitGeneral)
}
