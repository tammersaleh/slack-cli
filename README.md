# slack-cli

Read-only CLI for Slack. Designed for agents and automation.

## Install

macOS (Homebrew tap):

```bash
brew install tammersaleh/tap/slack-cli
```

Or from source (any platform):

```bash
go install github.com/tammersaleh/slack-cli/cmd/slack@latest
```

## Authentication

### Create a Slack app

Before using `slack auth login`, you need a Slack app with the right scopes.

Go to [api.slack.com/apps](https://api.slack.com/apps) and click **Create New App** > **From scratch**. Pick a name and the workspace you want to connect to.

### Add scopes

Under **OAuth & Permissions**, add these **Bot Token Scopes**:

- `channels:read`
- `groups:read`
- `im:read`
- `mpim:read`
- `users:read`
- `reactions:read`

Add this **User Token Scope** (needed for `search`):

- `search:read`

### Set the redirect URL

Under **OAuth & Permissions**, add a redirect URL:

```
http://127.0.0.1
```

The CLI starts a temporary local server on a random port during login. Slack allows any port on a registered redirect host.

### Export credentials

Copy the **Client ID** and **Client Secret** from the app's **Basic Information** page:

```bash
export SLACK_CLIENT_ID="your-client-id"
export SLACK_CLIENT_SECRET="your-client-secret"
```

### Log in

```bash
slack auth login
```

This opens your browser for the OAuth flow. Once approved, tokens are stored in `~/.config/slack-cli/credentials.json`.

### Verify

```bash
slack auth status
```

### Token overrides

For CI/automation, skip the OAuth flow entirely:

```bash
export SLACK_TOKEN="xoxb-your-bot-token"
export SLACK_USER_TOKEN="xoxp-your-user-token"  # optional, for search
```

These env vars take precedence over stored credentials.
