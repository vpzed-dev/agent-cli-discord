# Output and errors

Output schema version `1`. The normative contract is
`docs/json-contract.md` in the repository at
https://github.com/vpzed-dev/agent-cli-discord; this file restates it for
use without the repository. Unknown fields may be added within a schema
version, so ignore fields you do not recognize.

## Envelopes and exit status

Success, exit 0, exactly one document on stdout and nothing on stderr:

```json
{"ok":true,"data":{},"warnings":["token file is readable by group or other users"]}
```

`warnings` is omitted when empty. The current warnings are `token file is
readable by group or other users` and `token file permissions could not be
verified on this platform`.

Failure, exit 2, exactly one document on stderr and nothing on stdout:

```json
{
  "ok": false,
  "error": {
    "code": "discord.rate_limited",
    "message": "Discord rate limit exceeded",
    "retryable": true,
    "http_status": 429,
    "discord_code": 20028,
    "rate_limit_scope": "route"
  }
}
```

Exit 1 means the CLI could not encode or write its result; a complete
document is not guaranteed and neither stream should be trusted.

## Error object

Always present: `code` (stable, branch on this), `message` (human-readable,
never branch on it), `retryable` (boolean, always present).

Present only when applicable: `http_status` (Discord HTTP status),
`discord_code` (Discord's integer API error code), `rate_limit_scope`
(`"route"` or `"global"`, only with `discord.rate_limited`), and
`outcome_unknown` (`true` when a non-idempotent request failed in transport
and may have reached Discord).

`retryable: true` is advice, not a promise. The CLI itself retries only
idempotent requests that received a 429 with a valid delay, at most twice.
Apply your own bounded retry policy and, whenever `outcome_unknown` is
`true`, inspect Discord (`messages read`, `threads list`) before repeating a
post, reply, or thread creation.

## Error codes

| Code | Cause | What to do |
|------|-------|------------|
| `cli.invalid_arguments` | malformed options or content; also emitted by the Discord layer as a second check | fix the invocation; the message often contains the usage string |
| `cli.unknown_command` | unknown first word, including `help` and `--help` | use a command from this skill |
| `config.unavailable` | the user configuration directory could not be resolved | set `HOME` or `XDG_CONFIG_HOME` |
| `config.invalid` | `config.json` missing, wrong permissions, or failed validation | read the message and fix the file; see configuration.md |
| `credential.unavailable` | no usable token from environment or file | see configuration.md |
| `log.unavailable` | audit log could not be opened or appended (fail-closed) | fix the log path; if the message says the command completed, do not repeat it |
| `policy.guild_not_authorized` | guild mismatch | fix `guild_id` |
| `policy.channel_not_authorized` | `--channel` is neither an allowed channel nor an authorized thread, or a thread parent is not allowed | add the channel to `allowed_channel_ids` or use an allowed one |
| `policy.thread_not_authorized` | `threads join` or `leave` target fails local policy | check the parent channel and `allowed_thread_ids` |
| `policy.thread_creation_restricted` | `allowed_thread_ids` is nonempty | post into an existing listed thread instead |
| `attachment.unavailable` | an `--attach` path could not be opened | fix the path |
| `attachment.invalid` | not a regular file, over 10 MiB, or unsafe filename | choose another file |
| `attachment.too_large` | combined attachments over 24 MiB | send fewer or smaller files |
| `discord.http_error` | any non-2xx status not mapped below | inspect `http_status` and `discord_code`; 5xx on idempotent requests is `retryable` |
| `discord.transport_error` | network failure, timeout, or cancellation | retry idempotent requests; for others check `outcome_unknown` |
| `discord.rate_limited` | 429 after retries were exhausted or not applicable | wait, then retry; `rate_limit_scope` says whether the whole bot is limited |
| `discord.invalid_request` | the CLI could not build the request | report as a bug |
| `discord.invalid_response` | Discord's response could not be decoded or was inconsistent | retry once; report if persistent |
| `discord.not_bot_identity` | `auth check` found a user account token | use a bot token |
| `discord.guild_access_denied` | 403 listing guild channels | invite the bot to the guild and grant View Channel |
| `discord.reaction_access_denied` | 403 changing a reaction | grant Add Reactions or Read Message History |
| `discord.thread_archived` | `threads join` or `leave` on an archived thread | nothing; archived threads are read-only for membership |
| `internal.error` | unexpected failure | report as a bug |

Timeouts surface as `discord.transport_error` with message `Discord request
timed out`; `command_timeout` in the configuration bounds the whole command
including rate-limit waits.

## Shared objects

Snowflakes are strings. Timestamps are RFC 3339 strings with timezone.
Optional fields are omitted unless described as nullable.

### Message

```json
{
  "id": "345678901234567890",
  "channel_id": "234567890123456789",
  "author": {
    "id": "456789012345678901",
    "username": "agent",
    "global_name": null,
    "bot": true
  },
  "content": "hello",
  "timestamp": "2026-09-03T12:00:00Z",
  "edited_timestamp": null,
  "attachments": [],
  "embeds": [],
  "type": 0
}
```

- `attachments` entries: string `id`, string `filename`, optional string
  `description`, optional string `content_type`, integer `size` in bytes,
  string `url`, optional string `proxy_url`.
- `embeds`: Discord embed objects passed through unchanged.
- Optional `message_reference`: any of string `message_id`, `channel_id`,
  `guild_id`. Present on replies.
- Optional `referenced_message`: the replied-to message, same shape.
- Optional boolean `content_may_be_unavailable`: `true` when a type 0
  message from a non-bot author has empty content, no attachments, and no
  embeds. Treat it as a missing Message Content intent rather than an empty
  message.
- `type` 0 is a normal message; 19 is a reply. Other values are Discord
  system messages.

### Thread

```json
{
  "id": "345678901234567890",
  "guild_id": "123456789012345678",
  "parent_id": "234567890123456789",
  "name": "agent work",
  "type": 11,
  "thread_metadata": {
    "archived": false,
    "auto_archive_duration": 1440,
    "archive_timestamp": "2026-09-03T12:00:00Z",
    "locked": false
  }
}
```

`type` 10 is a news thread, 11 a public thread, 12 a private thread.
`thread_metadata` is nullable.

## Per-command data

| Command | `data` |
|---------|--------|
| `version` | `{name, version, schema_version}` strings |
| `auth check` | `{id, username, global_name (nullable), discriminator, avatar (nullable), bot}` |
| `channels list` | array of `{id, type, guild_id, position, name (nullable), parent_id (nullable)}` |
| `messages read` | `{messages: [Message...], cursor?: {before} or {after}}` |
| `messages get` | Message |
| `messages post`, `messages reply` | the created Message |
| `reactions add`, `reactions remove` | `{channel_id, message_id, emoji, action}` |
| `threads list` | array of Thread |
| `threads create` | the created Thread |
| `threads join`, `threads leave` | `{thread_id, action}` |

Pagination for `messages read`: messages are oldest to newest. A nonempty
plain or `--before` page returns `cursor.before` equal to its oldest
message ID; a nonempty `--after` page returns `cursor.after` equal to its
newest message ID; `--around` pages and empty pages carry no `cursor`.
