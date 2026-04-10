package cmd

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type MessagePostCmd struct {
	Channel string `arg:"" required:"" help:"Channel ID or name."`
	Text    string `help:"Message text."`
	Stdin   bool   `help:"Read message text from stdin."`
	Thread  string `help:"Thread timestamp to reply to."`
}

func (c *MessagePostCmd) Run(cli *CLI) error {
	text := c.Text
	if c.Stdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return &output.Error{Err: "stdin_error", Detail: err.Error(), Code: output.ExitGeneral}
		}
		text = strings.TrimRight(string(data), "\n")
	}

	if text == "" {
		return &output.Error{Err: "missing_text", Detail: "Provide --text or --stdin", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx := context.Background()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return &output.Error{Err: "channel_not_found", Detail: "No channel matching '" + c.Channel + "'", Code: output.ExitGeneral}
	}

	opts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
	}
	if c.Thread != "" {
		opts = append(opts, slack.MsgOptionTS(c.Thread))
	}

	respChannel, respTS, err := client.Bot().PostMessageContext(ctx, channelID, opts...)
	if err != nil {
		return cli.ClassifyError(err)
	}

	// Parse timestamp for ISO format.
	tsISO := ""
	if parts := strings.SplitN(respTS, ".", 2); len(parts) > 0 {
		if epoch, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
			tsISO = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
		}
	}

	if err := p.PrintItem(map[string]any{
		"channel": respChannel,
		"ts":      respTS,
		"ts_iso":  tsISO,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
