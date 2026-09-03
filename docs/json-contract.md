# JSON contract

This document defines output schema version `1`. Run `agent-cli-discord version`
to discover the schema version and executable version. Consumers must ignore
unknown object fields so compatible fields can be added without changing the
schema version. Existing field meanings and types will not change within a
schema version.

Discord snowflakes are always JSON strings. Timestamps are RFC 3339 strings
with timezone information. Optional fields are omitted unless described as
nullable.

## Streams and exit status

Each successful command writes exactly one JSON document and a trailing newline
to standard output and writes nothing to standard error:

```json
{"ok":true,"data":{},"warnings":["warning text"]}
```

`ok` is always `true`. `data` is always present and its shape depends on the
command. `warnings` is omitted when empty. Warnings describe a condition that
did not prevent the command from completing; currently they report token-file
permissions that could not be fully verified or that allow read access to other
users.

Each command failure writes exactly one JSON document and a trailing newline to
standard error and writes no success document to standard output:

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

Exit status `0` means success. Exit status `2` means a structured command
failure as shown above. Exit status `1` means the CLI could not encode or write
its result; in that case a complete JSON document is not guaranteed. Process
termination by the operating system is outside this contract.

## Error object

The `error` object always contains:

- `code`: stable machine-readable string.
- `message`: safe human-readable diagnostic; do not branch on this text.
- `retryable`: whether retrying may be appropriate. Callers must still apply a
  bounded retry policy.

The following fields are omitted when they do not apply:

- `http_status`: Discord HTTP status.
- `discord_code`: Discord's integer API error code.
- `rate_limit_scope`: `"route"` or `"global"`, only for a rate-limit error.
- `outcome_unknown`: `true` when a non-idempotent request failed in transport
  and may have reached Discord. Before retrying, inspect Discord or otherwise
  reconcile the operation to avoid duplicate messages or threads.

Current error codes are grouped below. New codes may be added within schema
version `1`; consumers must handle unrecognized codes safely.

- Command usage: `cli.invalid_arguments`, `cli.unknown_command`.
- Configuration: `config.unavailable`, `config.invalid`.
- Credentials: `credential.unavailable`.
- Fail-closed audit logging: `log.unavailable`.
- Local policy: `policy.guild_not_authorized`,
  `policy.channel_not_authorized`, `policy.thread_not_authorized`, and
  `policy.thread_creation_restricted`.
- Attachments: `attachment.unavailable`, `attachment.invalid`, and
  `attachment.too_large`.
- Discord transport and protocol: `discord.http_error`,
  `discord.transport_error`, `discord.rate_limited`,
  `discord.invalid_request`, and `discord.invalid_response`.
- Operation-specific Discord failures: `discord.not_bot_identity`,
  `discord.guild_access_denied`, `discord.reaction_access_denied`, and
  `discord.thread_archived`.
- Unexpected internal failure: `internal.error`.

## Shared data objects

### Message

Message-returning commands use this shape:

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

Message fields are:

- `id`, `channel_id`, `content`, `timestamp`, and integer `type`.
- `author`, containing string `id`, string `username`, nullable string
  `global_name`, and boolean `bot`.
- `edited_timestamp`, a nullable timestamp.
- `attachments`, an array whose entries contain string `id`, string `filename`,
  optional string `description`, optional string `content_type`, integer byte
  `size`, string `url`, and optional string `proxy_url`.
- `embeds`, an array of Discord embed JSON objects. Embed fields are passed
  through and are not a stable sub-schema of this CLI.
- Optional `message_reference`, containing any available string `message_id`,
  `channel_id`, and `guild_id`.
- Optional `referenced_message`, recursively using the message shape.
- Optional boolean `content_may_be_unavailable`; when present and `true`, the
  empty content may be a symptom of unavailable Message Content intent data.

### Thread

Thread-returning commands use this shape:

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

`thread_metadata` is nullable and otherwise contains boolean `archived`, integer
`auto_archive_duration` in minutes, timestamp string `archive_timestamp`, and
boolean `locked`.

## Command results

### `version`

`data` contains string `name`, string `version`, and string `schema_version`.

### `auth check`

`data` contains the bot user's string `id`, string `username`, nullable string
`global_name`, string `discriminator`, nullable string `avatar`, and boolean
`bot`. A successful result always has `bot: true`.

### `channels list`

`data` is an array of allowed guild channels. Each item contains string `id`,
integer `type`, string `guild_id`, integer `position`, nullable string `name`,
and nullable string `parent_id`. An empty result is `[]`.

### `messages read`

`data` contains:

- `messages`: an array of message objects ordered chronologically from oldest
  to newest. An empty page is `[]`.
- Optional `cursor`, containing either string `before` or string `after`.

The default page limit is 50 and the accepted range is 1 through 100. `before`,
`after`, and `around` are mutually exclusive. A nonempty ordinary or `before`
page returns `cursor.before` set to its oldest message ID. A nonempty `after`
page returns `cursor.after` set to its newest message ID. `cursor` is omitted
for empty and `around` pages. Cursors are stateless Discord snowflakes and may
be passed directly to the corresponding option on the next call.

### `messages get`, `messages post`, and `messages reply`

`data` is one message object. For `post` and `reply`, it is the object returned
by Discord after creation.

### `reactions add` and `reactions remove`

`data` contains string `channel_id`, string `message_id`, string `emoji`, and
string `action`. `action` is respectively `"add"` or `"remove"`.

### `threads list`

`data` is an array of active, locally authorized thread objects. An empty result
is `[]`.

### `threads create`

`data` is the created thread object returned by Discord after its guild and
parent identifiers have been checked.

### `threads join` and `threads leave`

`data` contains string `thread_id` and string `action`. `action` is respectively
`"join"` or `"leave"`.
