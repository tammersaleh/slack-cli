package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

func main() {
	var cli cmd.CLI
	ctx := kong.Parse(&cli,
		kong.Name("slack"),
		kong.Description("Read-only CLI for Slack."),
		kong.UsageOnError(),
	)

	err := ctx.Run(&cli)
	if err == nil {
		return
	}

	var oErr *output.Error
	if errors.As(err, &oErr) {
		json.NewEncoder(os.Stderr).Encode(oErr)
		os.Exit(oErr.Code)
	}

	// Unstructured errors: wrap in JSON for consistency.
	json.NewEncoder(os.Stderr).Encode(map[string]string{
		"error":  "general_error",
		"detail": err.Error(),
	})
	fmt.Fprintln(os.Stderr)
	os.Exit(output.ExitGeneral)
}
