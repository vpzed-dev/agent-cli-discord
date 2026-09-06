# agent-cli-discord

`agent-cli-discord` is a standalone JSON-speaking CLI for coding agents that
participate in explicitly authorized Discord guild channels through a Discord
Application bot identity. It uses Discord REST API v10 and does not run a
Gateway connection or daemon.

## Status

Releases are published for Linux amd64 as a statically linked executable on
the [GitHub Releases](https://github.com/vpzed-dev/agent-cli-discord/releases)
page, with a `SHA256SUMS` file to verify the download. Each release is
live-tested against a dedicated Discord guild before publication. Other
platforms build from source.

## Build

Go 1.27.1 or later is required.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o agent-cli-discord ./cmd/agent-cli-discord
```

The reported version comes from the git checkout: a release tag on a clean
checkout yields `v1.0.0`, an untagged commit yields a pseudo-version, and
uncommitted changes add `+dirty`. No build flag is needed.

## How to use

1. Install the executable. Download `agent-cli-discord-linux-amd64` and
   `SHA256SUMS` from the [Releases](https://github.com/vpzed-dev/agent-cli-discord/releases)
   page, verify the download, and place the binary on `PATH`:

   ```sh
   sha256sum -c SHA256SUMS
   install -m 0755 agent-cli-discord-linux-amd64 ~/.local/bin/agent-cli-discord
   agent-cli-discord version
   ```

   Alternatively, `go install github.com/vpzed-dev/agent-cli-discord/cmd/agent-cli-discord@v1.0.0`
   builds from source into `$(go env GOBIN)` or `$(go env GOPATH)/bin`.

2. Create the configuration file. The CLI reads `config.json` from an
   `agent-cli-discord` directory under the platform user configuration
   directory; there is no `--config` flag.

   | Platform | Directory |
   |----------|-----------|
   | Linux | `$XDG_CONFIG_HOME/agent-cli-discord/`, else `~/.config/agent-cli-discord/` |
   | macOS | `~/Library/Application Support/agent-cli-discord/` |
   | Windows | `%AppData%\agent-cli-discord\` |

   Copy [examples/config.example.json](examples/config.example.json) there as
   `config.json` and edit it. The file must not be writable by group or other
   users.

3. Provide the bot token. Set `DISCORD_BOT_TOKEN` in the environment, or copy
   [examples/token.env.example](examples/token.env.example) to `token.env` in
   the same directory and `chmod 600` it, or point `token_file` in the
   configuration at a token file. Sources are checked in that order.

4. Optionally install the agent skill.
   [skills/agent-cli-discord](skills/agent-cli-discord/SKILL.md) is an
   [Agent Skill](https://agentskills.io/) that teaches a coding agent how to
   use this tool. Copy or symlink that directory into the harness's skills
   location, for example `.claude/skills/` or `.agents/skills/`.

## Configuration

Configuration is strict JSON; comments, trailing commas, duplicate keys, and
unknown fields are rejected.

The public templates are [examples/config.example.json](examples/config.example.json)
and [examples/token.env.example](examples/token.env.example).

```json
{
  "schema_version": "1",
  "guild_id": "123456789012345678",
  "allowed_channel_ids": ["234567890123456789"],
  "allowed_thread_ids": ["345678901234567890"],
  "request_timeout": "15s",
  "command_timeout": "30s"
}
```

Omit `allowed_thread_ids` to let threads inherit authorization from their
verified parent channel. If it is nonempty, a thread must also be explicitly
listed. New thread creation is disabled under an explicit thread allowlist
because its future ID cannot be pre-authorized.

Message and reaction commands accept either an allowed channel ID or a thread
ID in `--channel`. A directly allowed channel is used without an extra lookup.
For any other syntactically valid ID, the CLI fetches channel metadata and
continues only when Discord identifies it as a thread in the configured guild,
its parent channel is allowed, and any explicit thread allowlist permits it.
No message-content or mutation request is made before those checks pass.

Set the bot token in `DISCORD_BOT_TOKEN`. Alternatively, use `token_file` in the
configuration or a permission-checked `token.env` in the same configuration
directory. Tokens are never accepted as command arguments.

Optional JSONL logging is described in [docs/audit-logging.md](docs/audit-logging.md).

## Commands

```text
agent-cli-discord version
agent-cli-discord auth check
agent-cli-discord channels list
agent-cli-discord messages read --channel ID [--limit N] [--before ID|--after ID|--around ID]
agent-cli-discord messages get --channel ID --message ID
agent-cli-discord messages post --channel ID [--file PATH] [--attach PATH ...]
agent-cli-discord messages reply --channel ID --message ID [--file PATH] [--attach PATH ...]
agent-cli-discord reactions add --channel ID --message ID --emoji EMOJI
agent-cli-discord reactions remove --channel ID --message ID --emoji EMOJI
agent-cli-discord threads list
agent-cli-discord threads create --channel PARENT_ID --name NAME [--auto-archive MINUTES]
agent-cli-discord threads join --thread ID
agent-cli-discord threads leave --thread ID
```

Message content is read from standard input unless `--file` is supplied. A
message can have up to 10 `--attach` options. Results are one JSON document on
standard output; failures are one JSON document on standard error and no
success document. Field shapes, exit statuses, warnings, pagination, and error
semantics are defined in [docs/json-contract.md](docs/json-contract.md).

When Discord returns a valid rate-limit delay, idempotent requests are retried
at most twice after the initial attempt. The command timeout includes these
waits. Non-idempotent message and thread creation requests are never retried
automatically because their outcome may be unknown.

## Bot permissions

Grant only the permissions needed in the allowed channels and their permitted
threads. Typical operations need View Channel and Read Message History.
Posting needs Send Messages. Starting public threads needs Create Public
Threads; joining and writing in threads needs Send Messages in Threads. Adding
a reaction may also need Add Reactions when that emoji is not already present.
Discord permissions remain the primary boundary and the local allowlists
further restrict access.

The Message Content privileged intent may be needed for message bodies and
related fields to be available. The CLI reports a conservative diagnostic when
Discord appears to have withheld them.

## Development checks

```sh
GOCACHE=/tmp/agent-cli-discord-go-cache go test ./...
GOCACHE=/tmp/agent-cli-discord-go-cache go test -race ./...
GOCACHE=/tmp/agent-cli-discord-go-cache go vet ./...
gofmt -d cmd internal
```

Tests use local HTTP servers and do not call Discord.
Contributor setup for deliberate testing against a dedicated Discord guild is
documented in [docs/live-testing.md](docs/live-testing.md). Real live-test
configuration and credentials stay under the ignored `.local/` directory.

## License

MIT. See [LICENSE](LICENSE).
