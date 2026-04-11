package cmd

import (
	"fmt"
	"io"
	"os"
)

type SkillCmd struct {
	Binary string `help:"Path to the slack binary for allowed-tools." default:""`
}

func (c *SkillCmd) Run(cli *CLI) error {
	binary := c.Binary
	if binary == "" {
		if exe, err := os.Executable(); err == nil {
			binary = exe
		} else {
			binary = "slack"
		}
	}

	out := io.Writer(os.Stdout)
	if cli.out != nil {
		out = cli.out
	}

	_, err := fmt.Fprintf(out, skillTemplate, binary)
	return err
}

const skillTemplate = `---
name: slack-cli
description: "Read and manage Slack workspaces: messages, channels, users, files, saved items, sidebar sections"
argument-hint: ""
allowed-tools:
  - Bash(%s *)
---

# Slack CLI

CLI for Slack. JSONL output (one JSON object per line). All commands end with
a ` + "`_meta`" + ` trailer: ` + "`" + `{"_meta":{"has_more":false}}` + "`" + `.

Auth is pre-configured. If you get ` + "`not_authed`" + `, tell the user to run
` + "`slack auth login`" + `.

## Output

Every command writes JSONL to stdout. Filter fields with ` + "`--fields id,name`" + `.
Suppress stdout with ` + "`--quiet`" + `. Errors go to stderr as JSON.

## Commands

### Messages

` + "```" + `
slack message list <channel> [--limit N] [--after TS] [--before TS] [--has-replies] [--has-reactions]
` + "```" + `

` + "```" + `
slack message get <channel> <ts>...
slack message permalink <channel> <ts>...
slack message post <channel> --text "..." [--thread TS]
slack message post <channel> --stdin [--thread TS]
` + "```" + `

### Threads

` + "```" + `
slack thread list <channel> <ts> [--limit N]
` + "```" + `

### Search (requires user token)

` + "```" + `
slack search messages <query> [--limit N] [--sort timestamp|score] [--sort-dir asc|desc]
slack search files <query> [--limit N]
` + "```" + `

### Channels

` + "```" + `
slack channel list [--limit N] [--type public|private|mpim|im] [--query STR] [--include-non-member]
slack channel info <channel>...
slack channel members <channel> [--limit N]
` + "```" + `

### Users

` + "```" + `
slack user list [--limit N] [--query STR] [--presence]
slack user info <user>...
` + "```" + `

User arguments accept IDs (U...), emails, or @names.

### Files

` + "```" + `
slack file list [--limit N] [--channel CH] [--user UID] [--types images,pdfs]
slack file info <file-id>...
slack file download <file-id> [-o path]
` + "```" + `

### Reactions

` + "```" + `
slack reaction list <channel> <ts>...
` + "```" + `

### Pins and Bookmarks

` + "```" + `
slack pin list <channel>
slack bookmark list <channel>
` + "```" + `

### Saved Items (requires session token)

` + "```" + `
slack saved list [--limit N] [--enrich] [--include-completed]
slack saved counts
` + "```" + `

### Sidebar Sections (requires session token)

` + "```" + `
slack section list
slack section channels <section-id>
slack section find <pattern>
slack section move --channels ID,ID --section ID
slack section move --channels ID,ID --new-section "Name"
slack section create <name>
` + "```" + `

### User State

` + "```" + `
slack status get [user...]
slack presence get <user>...
slack dnd info <user>...
` + "```" + `

### Other

` + "```" + `
slack emoji list [--query STR]
slack usergroup list [--include-disabled] [--query STR]
slack usergroup members <group-id>
slack workspace-info info
` + "```" + `

## Pagination

Most list commands return one page by default. Use ` + "`--all`" + ` to fetch
everything, or use the ` + "`next_cursor`" + ` from ` + "`_meta`" + ` with ` + "`--cursor`" + `.

## Channel and User Resolution

Channels accept IDs (C.../G.../D...) or #names. Users accept IDs (U...), emails,
or @display-names.
`
