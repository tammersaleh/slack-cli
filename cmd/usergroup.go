package cmd

import (
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type UsergroupCmd struct {
	List    UsergroupListCmd    `cmd:"" help:"List usergroups."`
	Members UsergroupMembersCmd `cmd:"" help:"List members of a usergroup."`
}

type UsergroupListCmd struct {
	IncludeDisabled bool   `help:"Include disabled usergroups."`
	Query           string `help:"Filter by name substring (client-side)."`
}

func (c *UsergroupListCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	var opts []slack.GetUserGroupsOption
	if c.IncludeDisabled {
		opts = append(opts, slack.GetUserGroupsOptionIncludeDisabled(true))
	}

	groups, err := client.Bot().GetUserGroupsContext(ctx, opts...)
	if err != nil {
		return cli.ClassifyError(err)
	}

	for _, g := range groups {
		if c.Query != "" && !strings.Contains(strings.ToLower(g.Name), strings.ToLower(c.Query)) {
			continue
		}
		if err := p.PrintItem(usergroupToMap(g)); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

type UsergroupMembersCmd struct {
	Usergroup string `arg:"" required:"" help:"Usergroup ID."`
}

func (c *UsergroupMembersCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	members, err := client.Bot().GetUserGroupMembersContext(ctx, c.Usergroup)
	if err != nil {
		return cli.ClassifyError(err)
	}

	for _, id := range members {
		if err := p.PrintItem(map[string]any{"user_id": id}); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

func usergroupToMap(g slack.UserGroup) map[string]any { return toMap(g) }
