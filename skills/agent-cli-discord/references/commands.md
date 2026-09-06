# Command reference

Every command is `agent-cli-discord <command> <subcommand> [--option value
...]`. Options are parsed as pairs, so an odd number of option tokens fails
with `every option requires a value`, an option outside the command's set
fails with `unknown option "--x"`, a repeated option fails with `duplicate
option "--x"`, and a value beginning with `--` fails with `option "--x"
requires a value`. Those messages appear verbatim for `messages read`,
`messages post`, and `messages reply`; the other commands replace them with
their usage string. All argument failures use code `cli.invalid_arguments`
and exit 2 before any configuration, credential, or network access.

Snowflake means 17 to 20 ASCII digits. Idempotent requests are retried at
most twice after a 429 with a valid delay; non-idempotent requests are never
retried.

## version

```text
agent-cli-discord version
```

Offline. Reads no configuration and no credentials. Any extra argument fails
with `version accepts no arguments`.

`data`: `{"name":"agent-cli-discord","version":"v1.0.0","schema_version":"1"}`.
`version` is the Go module version stamped into the executable: a release
tag, a pseudo-version such as `v0.0.0-20260906121237-cb248015f190` for an
untagged source build, a `+dirty` suffix for a modified tree, or `dev` when
no build metadata exists.

## auth check

```text
agent-cli-discord auth check
```

Usage error: `usage: agent-cli-discord auth check`.

Request: `GET /users/@me`, idempotent. Fails with `discord.not_bot_identity`
when the credential belongs to a user account rather than a bot.

`data`: string `id`, string `username`, nullable string `global_name`,
string `discriminator`, nullable string `avatar`, boolean `bot` (always
`true` on success).

## channels list

```text
agent-cli-discord channels list
```

Usage error: `usage: agent-cli-discord channels list`.

Request: `GET /guilds/{guild_id}/channels`, idempotent. The response is
filtered to channels whose ID is in `allowed_channel_ids`; nothing else is
returned even though the bot may see more. HTTP 403 becomes
`discord.guild_access_denied`, which means the bot is not a member of the
configured guild or cannot view its channels.

`data`: array, possibly `[]`, of objects with string `id`, integer `type`,
string `guild_id`, integer `position`, nullable string `name`, and nullable
string `parent_id`. Type 0 is a text channel.

## messages read

```text
agent-cli-discord messages read --channel ID [--limit N]
                                [--before ID | --after ID | --around ID]
```

| Option | Required | Default | Rule |
|--------|----------|---------|------|
| `--channel` | yes | | allowed channel ID or an authorized thread ID |
| `--limit` | no | 50 | integer 1 to 100 |
| `--before` | no | | snowflake; page ending before this message |
| `--after` | no | | snowflake; page starting after this message |
| `--around` | no | | snowflake; page centered on this message |

Argument errors, verbatim: `--channel must be a Discord snowflake`,
`--limit must be an integer between 1 and 100`, `cursor values must be
Discord snowflakes`, `--before, --after, and --around are mutually
exclusive`.

Policy: the target is authorized as described under "Message targets"
below. Request: `GET /channels/{id}/messages?limit=N[&before|after|around]`,
idempotent.

`data`: object with `messages`, an array of message objects ordered oldest
to newest (`[]` when empty), and optional `cursor`. A plain or `--before`
page sets `cursor.before` to its oldest message ID; an `--after` page sets
`cursor.after` to its newest message ID; `--around` pages and empty pages
have no `cursor`. Cursors are plain snowflakes and can be passed straight
back as the matching option.

## messages get

```text
agent-cli-discord messages get --channel ID --message ID
```

Both options required, both snowflakes. Any failure yields
`usage: agent-cli-discord messages get --channel ID --message ID`.

Request: `GET /channels/{channel}/messages/{message}`, idempotent, after
target authorization.

`data`: one message object.

## messages post and messages reply

```text
agent-cli-discord messages post --channel ID [--file PATH] [--attach PATH ...]
agent-cli-discord messages reply --channel ID --message ID [--file PATH]
                                 [--attach PATH ...]
```

| Option | post | reply | Rule |
|--------|------|-------|------|
| `--channel` | required | required | allowed channel ID or authorized thread ID |
| `--message` | forbidden | required | snowflake of the message being replied to |
| `--file` | optional | optional | read content from this regular file instead of stdin |
| `--attach` | optional, repeatable | same | regular file to upload; at most 10 |

Argument errors, verbatim: `--channel must be a Discord snowflake`,
`--message must be a Discord snowflake for replies`, `--message is only
valid for replies`, `a message may contain at most 10 attachments`, `could
not open message content file`, `message content file must be a regular
file`, `could not read message content`, `message content exceeds 8000
bytes`, `message content must be valid UTF-8`, `message content exceeds 2000
characters`, `message content must not be empty without an attachment`.

Content is read before the configuration is loaded, so input problems never
touch Discord. Content is sent exactly as read, including any trailing
newline.

Attachment checks run after authorization and before the request: an
unreadable path is `attachment.unavailable`; a non-regular file, a file over
10 MiB, or a basename containing a carriage return or newline is
`attachment.invalid`; more than 24 MiB combined is `attachment.too_large`.
The uploaded filename is the path's basename.

Request: `POST /channels/{id}/messages`, not idempotent, never retried. The
payload always carries `allowed_mentions: {"parse": [], "replied_user":
false}`. A reply adds a `message_reference` with `fail_if_not_exists: true`,
so replying to a deleted message fails with `discord.http_error`.
Attachments switch the body to multipart form data.

`data`: the message object Discord returned for the created message.

## reactions add and reactions remove

```text
agent-cli-discord reactions add --channel ID --message ID --emoji EMOJI
agent-cli-discord reactions remove --channel ID --message ID --emoji EMOJI
```

All three options are required. Any failure yields
`usage: agent-cli-discord reactions add|remove --channel ID --message ID
--emoji EMOJI`.

`--emoji` accepts two forms. A value containing `:` is a custom emoji and
must be `name:id`, where `name` matches `[A-Za-z0-9_]{2,32}` and `id` is a
snowflake; the animated prefix form `a:name:id` is rejected. Any other value
is treated as Unicode and must be nonempty, contain no `/`, `\`, carriage
return, or newline, and contain at least one non-ASCII printable character.

Request: `PUT` or `DELETE
/channels/{channel}/messages/{message}/reactions/{emoji}/@me`, idempotent,
after target authorization. Only the bot's own reaction is affected. HTTP
403 becomes `discord.reaction_access_denied`; adding an emoji that is not
already on the message may need the Add Reactions permission.

`data`: `{"channel_id":"...","message_id":"...","emoji":"...","action":"add"}`
with `action` `"add"` or `"remove"`.

## threads list

```text
agent-cli-discord threads list
```

Usage error: `usage: agent-cli-discord threads list`.

Request: `GET /guilds/{guild_id}/threads/active`, idempotent. The result is
filtered to threads whose parent channel is allowed and, when
`allowed_thread_ids` is nonempty, whose own ID is listed. Archived threads
are not active and never appear.

`data`: array, possibly `[]`, of thread objects.

## threads create

```text
agent-cli-discord threads create --channel PARENT_ID --name NAME
                                 [--auto-archive MINUTES]
```

| Option | Required | Default | Rule |
|--------|----------|---------|------|
| `--channel` | yes | | parent channel; must be in `allowed_channel_ids` |
| `--name` | yes | | 1 to 100 UTF-8 characters |
| `--auto-archive` | no | 1440 | exactly 60, 1440, 4320, or 10080 |

Argument errors: the usage string
`usage: agent-cli-discord threads create --channel PARENT_ID --name NAME
[--auto-archive MINUTES]` for a bad channel or name, and `--auto-archive
must be 60, 1440, 4320, or 10080` for a bad duration.

Policy: the parent must be an allowed channel
(`policy.channel_not_authorized` with message `parent channel is not
authorized`), and `allowed_thread_ids` must be absent or empty
(`policy.thread_creation_restricted`). Both checks happen before any
network call.

Request: `POST /channels/{parent}/threads` with `type: 11` (public thread),
not idempotent, never retried. If Discord returns a thread outside the
requested guild or parent the command fails with `discord.invalid_response`.

`data`: the created thread object.

## threads join and threads leave

```text
agent-cli-discord threads join --thread ID
agent-cli-discord threads leave --thread ID
```

`--thread` is required and must be a snowflake. Any failure yields
`usage: agent-cli-discord threads join|leave --thread ID`.

Requests: `GET /channels/{thread}` to fetch metadata, then `PUT` or
`DELETE /channels/{thread}/thread-members/@me`. Both idempotent. The thread
must belong to the configured guild, be of type 10, 11, or 12, have an
allowed parent, and pass any explicit thread allowlist
(`policy.thread_not_authorized`). An archived thread fails with
`discord.thread_archived`.

`data`: `{"thread_id":"...","action":"join"}` with `action` `"join"` or
`"leave"`.

## Message targets

`messages read`, `messages get`, `messages post`, `messages reply`, and both
reaction commands resolve `--channel` the same way:

1. If the ID is in `allowed_channel_ids`, it is used directly with no extra
   request.
2. Otherwise the ID must be a snowflake, and the CLI makes one
   `GET /channels/{id}` request. The target is accepted only when Discord
   reports the same ID, the configured guild, a thread type (10, 11, or 12),
   thread metadata, an allowed parent channel, and, when
   `allowed_thread_ids` is nonempty, an ID in that list.
3. Anything else fails with `policy.channel_not_authorized` and message
   `channel or thread is not authorized`.

No message content is read from Discord and no mutation is sent until the
target passes.

## Subcommand usage strings

- `messages` alone: `usage: agent-cli-discord messages read|get [options]`
- `messages <other>`: `usage: agent-cli-discord messages
  read|get|post|reply [options]`
- `threads` alone or `threads <other>`: `usage: agent-cli-discord threads
  list|create|join|leave [options]`
- `reactions` alone or `reactions <other>`: the reactions usage string above
- No arguments at all: `a command is required`
- Unknown first word: `unknown command: <word>` with code
  `cli.unknown_command`
