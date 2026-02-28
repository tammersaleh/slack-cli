package cmd

import "fmt"

type CLI struct {
	Workspace string `short:"w" help:"Select workspace (name or ID)." env:"SLACK_WORKSPACE"`
	Format    string `short:"f" enum:"json,text" default:"json" help:"Output format." env:"SLACK_FORMAT"`
	Quiet     bool   `short:"q" help:"Suppress non-essential output."`
	Verbose   bool   `short:"v" help:"Include extra diagnostic info on stderr."`
	NoPager   bool   `help:"Disable automatic paging of long output."`
	Limit     int    `help:"Max items to return for list commands (0 = all)." default:"0"`
	Raw       bool   `help:"Output raw API responses without transformation."`

	Channel  ChannelCmd  `cmd:"" help:"Read channel information."`
	Message  MessageCmd  `cmd:"" help:"Read messages."`
	Thread   ThreadCmd   `cmd:"" help:"Read thread replies."`
	User     UserCmd     `cmd:"" help:"Read user information."`
	Reaction ReactionCmd `cmd:"" help:"Read reactions."`
}

// Stub subcommands. Each will be fleshed out in later issues.

type ChannelCmd struct {
	List    ChannelListCmd    `cmd:"" help:"List channels."`
	Info    ChannelInfoCmd    `cmd:"" help:"Show channel details."`
	Members ChannelMembersCmd `cmd:"" help:"List channel members."`
}

type ChannelListCmd struct{}

func (c *ChannelListCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type ChannelInfoCmd struct {
	Channel string `arg:"" help:"Channel ID or name."`
}

func (c *ChannelInfoCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type ChannelMembersCmd struct {
	Channel string `arg:"" help:"Channel ID or name."`
}

func (c *ChannelMembersCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type MessageCmd struct {
	List MessageListCmd `cmd:"" aliases:"read" help:"List messages in a channel."`
	Get  MessageGetCmd  `cmd:"" help:"Get a single message by timestamp."`
}

type MessageListCmd struct {
	Channel string `arg:"" help:"Channel ID or name."`
}

func (c *MessageListCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type MessageGetCmd struct {
	Channel   string `arg:"" help:"Channel ID or name."`
	Timestamp string `arg:"" help:"Message timestamp."`
}

func (c *MessageGetCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type ThreadCmd struct {
	List ThreadListCmd `cmd:"" aliases:"read" help:"List thread replies."`
}

type ThreadListCmd struct {
	Channel   string `arg:"" help:"Channel ID or name."`
	Timestamp string `arg:"" help:"Parent message timestamp."`
}

func (c *ThreadListCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type UserCmd struct {
	List UserListCmd `cmd:"" help:"List users."`
	Info UserInfoCmd `cmd:"" help:"Show user details."`
}

type UserListCmd struct{}

func (c *UserListCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type UserInfoCmd struct {
	User string `arg:"" help:"User ID, @name, or email."`
}

func (c *UserInfoCmd) Run() error {
	return fmt.Errorf("not implemented")
}

type ReactionCmd struct {
	List ReactionListCmd `cmd:"" help:"List reactions on a message."`
}

type ReactionListCmd struct {
	Channel   string `arg:"" help:"Channel ID or name."`
	Timestamp string `arg:"" help:"Message timestamp."`
}

func (c *ReactionListCmd) Run() error {
	return fmt.Errorf("not implemented")
}
