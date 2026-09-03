# Live Discord testing

The ordinary `go test ./...` suite uses local HTTP fixtures and needs neither a
Discord token nor a guild. Live testing is a separate, deliberate pre-release
activity because it creates or changes real Discord resources.

Use a dedicated test guild and a dedicated bot application. Give the bot only
the permissions required by the command surface, and limit the configuration to
channels created for testing. Do not reuse production credentials or channels.

## Local setup

From the repository root, create an ignored configuration tree:

```sh
mkdir -p .local/live-test/agent-cli-discord
cp examples/live-test/config.example.json .local/live-test/agent-cli-discord/config.json
cp examples/live-test/token.env.example .local/live-test/agent-cli-discord/token.env
chmod 600 .local/live-test/agent-cli-discord/config.json
chmod 600 .local/live-test/agent-cli-discord/token.env
```

Replace the fake guild and channel snowflakes in `config.json`. Replace the
placeholder in `token.env` with the dedicated test bot token. Both resulting
files are below `.local/`, which is ignored by Git. Never put a real token in an
example, shell argument, issue, test output, or commit.

The live-test example intentionally omits `allowed_thread_ids`, allowing a new
thread ID to be created during the smoke test. Its audit log also remains below
the ignored `.local/` tree.

Build and run commands with the isolated configuration root:

```sh
CGO_ENABLED=0 go build -trimpath -o .local/live-test/agent-cli-discord-bin ./cmd/agent-cli-discord
XDG_CONFIG_HOME="$PWD/.local/live-test" .local/live-test/agent-cli-discord-bin auth check
XDG_CONFIG_HOME="$PWD/.local/live-test" .local/live-test/agent-cli-discord-bin channels list
```

Continue through each command in the README. Use unique test messages and
attachments containing no sensitive data. Record created message and thread
IDs, then clean up test state through Discord after the smoke pass; destructive
message/thread cleanup is intentionally outside this CLI's current surface.

## Release-user example

The files directly under `examples/` are templates for normal installations.
Copy `config.example.json` to the platform configuration directory as
`agent-cli-discord/config.json`. Either export `DISCORD_BOT_TOKEN` in the calling
environment or copy `token.env.example` beside the configuration as
`token.env`, replace its placeholder, and restrict it to the current user.
