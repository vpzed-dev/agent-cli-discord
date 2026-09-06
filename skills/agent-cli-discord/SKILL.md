---
name: agent-cli-discord
description: >-
  Operate Discord guild channels and threads from a shell with the
  agent-cli-discord command-line tool: verify the bot identity, list allowed
  channels, read and page through messages, post or reply with attachments,
  add or remove reactions, and list, create, join, or leave threads. Use this
  skill whenever a task reads from or writes to Discord through that
  executable, even when the request only says "post this to the channel" or
  "check the thread", and whenever agent-cli-discord, its config.json, or its
  token.env is mentioned.
license: MIT
compatibility: >-
  Requires the agent-cli-discord executable (Linux amd64 release binary or a
  build from source) and network access to discord.com.
metadata:
  author: vpzed-dev/agent-cli-discord
  version: "1.0.0"
---

# agent-cli-discord

`agent-cli-discord` is a JSON-speaking CLI that lets an agent act through a
Discord bot identity in explicitly allowed guild channels and threads. Every
invocation makes one Discord REST call (plus at most one metadata lookup),
writes exactly one JSON document, and exits. There is no daemon, no Gateway
connection, and no interactive mode. Local allowlists in the configuration
file restrict which guild, channels, and threads are reachable, on top of
whatever Discord permissions the bot has.

This skill matches executable version `v1.0.0` and output schema version `1`.

## Prerequisites

Check these before the first real command. Read
[references/configuration.md](references/configuration.md) when the
configuration or token has to be created, or when a command fails with a
`config.*`, `credential.*`, or `log.*` error code.

- The executable is on `PATH`. `agent-cli-discord version` succeeds and
  prints `"version":"v1.0.0"` (or a pseudo-version for a source build).
- `config.json` exists in the tool's configuration directory. On Linux that
  is `$XDG_CONFIG_HOME/agent-cli-discord/`, falling back to
  `~/.config/agent-cli-discord/`. macOS uses
  `~/Library/Application Support/agent-cli-discord/` and Windows
  `%AppData%\agent-cli-discord\`. There is no `--config` flag.
- A bot token is available from `DISCORD_BOT_TOKEN`, from the `token_file`
  named in the configuration, or from `token.env` next to `config.json`, in
  that order of precedence.

## Invocation rules

The CLI has no help output. `agent-cli-discord`, `help`, `--help`, and `-h`
all exit 2 with a `cli.invalid_arguments` or `cli.unknown_command` failure.
Use this skill instead of probing the binary.

- Commands are two words (`messages read`) except `version`.
- Options are `--name value` pairs separated by a space. There is no
  `--name=value` form, no short flag, and no positional argument.
- A value may not begin with `--`. Content and paths that start with `--`
  must be supplied another way (for example through `--file`).
- An option may appear once. Only `--attach` may repeat.
- IDs are Discord snowflakes: 17 to 20 ASCII digits, always passed and
  returned as strings. Quote them in `jq` filters.
- Message content for `post` and `reply` comes from standard input unless
  `--file PATH` is given. Always pipe input or pass `--file`; an interactive
  terminal with nothing piped blocks forever.

## Reading results

Every command writes one JSON document followed by a newline.

| Exit | Stream | Meaning |
|------|--------|---------|
| `0` | stdout | Success. `{"ok":true,"data":...}` plus optional `warnings`. |
| `2` | stderr | Failure. `{"ok":false,"error":{...}}`; stdout is empty. |
| `1` | either | The CLI could not write its result. Do not trust output. |

```sh
agent-cli-discord auth check
# {"ok":true,"data":{"id":"456789012345678901","username":"agent",
#   "global_name":null,"discriminator":"0","avatar":null,"bot":true}}

agent-cli-discord messages get --channel 1 --message 2
# stderr: {"ok":false,"error":{"code":"cli.invalid_arguments",
#   "message":"usage: agent-cli-discord messages get --channel ID --message ID",
#   "retryable":false}}
```

Capture stderr when you need the error object, and branch on `error.code`,
never on `error.message`. `warnings` currently only reports token-file
permission conditions. Read
[references/output-and-errors.md](references/output-and-errors.md) when `ok`
is `false`, when you need a `data` field shape, or before retrying anything.

Useful `jq` idioms:

```sh
agent-cli-discord channels list | jq -r '.data[] | "\(.id)\t\(.name)"'
agent-cli-discord messages read --channel "$CH" --limit 20 \
  | jq -r '.data.messages[] | "\(.id) \(.author.username): \(.content)"'
agent-cli-discord messages post --channel "$CH" <<< "hello" | jq -r '.data.id'
```

## Command quick reference

```text
agent-cli-discord version
agent-cli-discord auth check
agent-cli-discord channels list
agent-cli-discord messages read --channel ID [--limit 1..100]
                                [--before ID | --after ID | --around ID]
agent-cli-discord messages get --channel ID --message ID
agent-cli-discord messages post --channel ID [--file PATH] [--attach PATH ...]
agent-cli-discord messages reply --channel ID --message ID [--file PATH]
                                 [--attach PATH ...]
agent-cli-discord reactions add --channel ID --message ID --emoji EMOJI
agent-cli-discord reactions remove --channel ID --message ID --emoji EMOJI
agent-cli-discord threads list
agent-cli-discord threads create --channel PARENT_ID --name NAME
                                 [--auto-archive 60|1440|4320|10080]
agent-cli-discord threads join --thread ID
agent-cli-discord threads leave --thread ID
```

`--limit` defaults to 50 and `--auto-archive` to 1440 minutes. `--channel`
accepts an allowed channel ID or the ID of a thread under an allowed channel
for every message and reaction command. Read
[references/commands.md](references/commands.md) for each command's Discord
request, policy checks, exact usage errors, and `data` shape.

## Workflows

### Verify access before doing work

```sh
agent-cli-discord version
agent-cli-discord auth check        # confirms the token belongs to a bot
agent-cli-discord channels list     # only channels in allowed_channel_ids
```

An empty `channels list` result means the allowlist and the guild disagree,
or the bot cannot see those channels. Fix the configuration or the bot's
channel permissions before continuing.

### Read a channel and page through history

Pages are returned oldest to newest. A plain or `--before` page returns
`cursor.before` set to its oldest message ID; pass it back to go further into
the past. An `--after` page returns `cursor.after` set to its newest ID; pass
it back to move toward the present. `--around` pages have no cursor, and an
empty page has no cursor.

```sh
page=$(agent-cli-discord messages read --channel "$CH" --limit 100)
printf '%s\n' "$page" | jq -r '.data.messages[] | .content'
older=$(printf '%s\n' "$page" | jq -r '.data.cursor.before // empty')
[ -n "$older" ] && agent-cli-discord messages read --channel "$CH" \
  --limit 100 --before "$older"
```

To poll for new messages since a known ID, use `--after "$LAST_ID"` and keep
the returned `cursor.after` as the next starting point.

### Post, reply, and attach files

```sh
# Content from stdin. A heredoc keeps its trailing newline; use printf to
# control it exactly.
printf 'Build finished: all 42 tests passed.' \
  | agent-cli-discord messages post --channel "$CH"

# Content from a file, plus attachments (at most 10, 10 MiB each, 24 MiB
# combined). The attachment's basename becomes the Discord filename.
agent-cli-discord messages post --channel "$CH" --file ./summary.md \
  --attach ./report.pdf --attach ./chart.png

# Reply to a specific message in the same channel or thread.
printf 'Fixed in commit abc123.' \
  | agent-cli-discord messages reply --channel "$CH" --message "$MSG"

# Attachment-only message: content may be empty when --attach is present.
agent-cli-discord messages post --channel "$CH" --attach ./log.txt < /dev/null
```

`data` is the created message; `.data.id` is the new message ID.

### React to a message

```sh
agent-cli-discord reactions add --channel "$CH" --message "$MSG" --emoji '✅'
agent-cli-discord reactions add --channel "$CH" --message "$MSG" \
  --emoji 'partyparrot:123456789012345678'
agent-cli-discord reactions remove --channel "$CH" --message "$MSG" --emoji '✅'
```

Unicode emoji are passed literally. Custom emoji use exactly `name:id`. Both
commands act only on the bot's own reaction and are idempotent.

### Work in threads

```sh
agent-cli-discord threads list                       # active, allowed threads
thread=$(agent-cli-discord threads create --channel "$CH" \
  --name "deploy 2026-09-06" | jq -r '.data.id')
agent-cli-discord threads join --thread "$thread"
printf 'Starting the rollout.' \
  | agent-cli-discord messages post --channel "$thread"
agent-cli-discord messages read --channel "$thread"
agent-cli-discord threads leave --thread "$thread"
```

Threads are addressed by passing the thread ID as `--channel` to message and
reaction commands. `threads create` always makes a public thread under an
allowed parent channel. Joining is not required to post into a public thread.
Archived threads cannot be joined or left.

## Gotchas

- **No help, no `=` syntax, no short flags.** Every malformed invocation is
  a `cli.invalid_arguments` failure on stderr, often with the full usage
  string in `error.message`.
- **Stdin blocks.** `post` and `reply` read stdin to EOF when `--file` is
  absent. Always pipe, redirect, or use `--file`.
- **Content limits.** At most 2000 characters and 8000 bytes, valid UTF-8,
  sent verbatim with no trimming. Empty content is allowed only with at
  least one `--attach`.
- **Mentions never notify.** Every post and reply is sent with an empty
  `allowed_mentions` list and `replied_user: false`, so `@user`, `@role`,
  and `@everyone` render as text and do not ping. Replies do not notify the
  original author.
- **Creation is never retried.** `messages post`, `messages reply`, and
  `threads create` make one attempt. A transport failure on them returns
  `discord.transport_error` with `outcome_unknown: true`; read the channel or
  thread list before sending again, or you may duplicate the message.
- **Rate limits.** Read-only and toggle commands retry a 429 at most twice
  using the delay Discord supplies. `command_timeout` (default 30 seconds)
  bounds the whole command including those waits; `request_timeout` (default
  15 seconds) bounds each attempt.
- **Thread authorization.** With `allowed_thread_ids` absent or empty, any
  thread under an allowed channel is usable. When it is nonempty the thread
  must be listed there too, and `threads create` fails with
  `policy.thread_creation_restricted` before any network call.
- **Thread targets cost one lookup.** Passing a thread ID as `--channel`
  triggers a `GET /channels/{id}` metadata request before the real request.
  Directly allowed channel IDs skip it.
- **Emoji validation.** A value containing `:` is treated as custom and must
  be `name:id` with a 2 to 32 character alphanumeric or underscore name and a
  snowflake ID. The animated form `a:name:id` is rejected. A value without
  `:` must contain at least one non-ASCII character, so `:white_check_mark:`
  style shortcodes and plain ASCII are rejected.
- **`content_may_be_unavailable: true`** on a message means Discord returned
  a human message with no content, attachments, or embeds. The usual cause
  is the bot lacking the Message Content privileged intent, not an empty
  message.
- **`DISCORD_BOT_TOKEN` set but empty** is a `credential.unavailable` error.
  The CLI does not fall through to the token file.
- **Audit logging is fail-closed.** When the configuration has a `log`
  object and the log cannot be opened, no command runs. If the log cannot be
  appended after a mutating command succeeded, the command still exits 2
  with `log.unavailable` even though Discord applied the change. Check
  Discord before repeating it.
- **Tokens are never arguments.** Do not pass, print, or log the token, and
  do not include it in message content. The CLI redacts it from Discord error
  messages but not from anything you write yourself.
- **`version` is the only offline command.** Every other command loads the
  configuration and credentials first, so configuration errors surface even
  for read-only operations.
