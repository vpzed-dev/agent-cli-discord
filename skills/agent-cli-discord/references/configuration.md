# Configuration, credentials, and logging

## Locations

The CLI reads `config.json` from an `agent-cli-discord` directory under the
platform user configuration directory. There is no flag or environment
variable that names the file directly; set `XDG_CONFIG_HOME` on Linux to
relocate the whole directory.

| Platform | Directory |
|----------|-----------|
| Linux | `$XDG_CONFIG_HOME/agent-cli-discord/`, else `~/.config/agent-cli-discord/` |
| macOS | `~/Library/Application Support/agent-cli-discord/` |
| Windows | `%AppData%\agent-cli-discord\` |

`token.env`, when used, lives in the same directory.

If the directory cannot be resolved the command fails with
`config.unavailable`. Every other configuration problem is `config.invalid`
with a descriptive message.

## config.json

File requirements:

- A regular file no larger than 1 MiB.
- On Unix it must not be writable by group or others (`chmod 600` or `644`).
- Strict JSON: no comments, no trailing commas, no duplicate keys at any
  depth, no unknown fields, exactly one top-level value.

Fields:

| Field | Type | Required | Default | Rule |
|-------|------|----------|---------|------|
| `schema_version` | string | yes | | must be `"1"` |
| `guild_id` | string | yes | | snowflake of the one allowed guild |
| `allowed_channel_ids` | array of strings | yes | | at least one snowflake, no duplicates |
| `allowed_thread_ids` | array of strings | no | absent | snowflakes, no duplicates; see below |
| `request_timeout` | string | no | `"15s"` | positive Go duration per HTTP attempt |
| `command_timeout` | string | no | `"30s"` | positive Go duration for the whole command; must be at least `request_timeout` |
| `token_file` | string | no | absent | path to a token file; overrides the default `token.env` |
| `log` | object | no | absent | enables audit logging; see below |
| `log.path` | string | yes when `log` is present | | JSONL destination; relative paths resolve against the working directory |
| `log.level` | string | no | `"info"` | one of `debug`, `info`, `warn`, `error`; recorded on each event, does not filter anything |

Go durations look like `"15s"`, `"1m30s"`, or `"500ms"`.

Thread semantics: when `allowed_thread_ids` is absent or empty, any thread
whose parent is in `allowed_channel_ids` is authorized, and `threads create`
works on allowed parents. When it is nonempty, a thread must also be listed
explicitly, and `threads create` is refused because a new thread's ID cannot
be listed in advance.

Minimal configuration (replace both IDs with the authorized targets):

```json
{
  "schema_version": "1",
  "guild_id": "123456789012345678",
  "allowed_channel_ids": ["234567890123456789"]
}
```

Add optional fields only when needed. A nonempty `allowed_thread_ids`
disables thread creation. If enabling logging, first ensure the parent
directory of `log.path` exists. Change guild or allowlists only within
user-authorized scope; an access failure alone does not authorize a change.

## Bot token

Resolution order; the first source that is present wins:

1. `DISCORD_BOT_TOKEN` in the environment. If the variable is set but
   empty the command fails; it does not fall through.
2. The file named by `token_file` in `config.json`.
3. `token.env` in the configuration directory.

Tokens are never accepted as command arguments. Any failure is
`credential.unavailable`.

Token file requirements:

- A regular file no larger than 1024 bytes, valid UTF-8.
- On Unix it must not be writable by group or others, or the command fails.
  If it is readable by group or others the command still runs and adds the
  warning `token file is readable by group or other users`. Use `chmod 600`.
- On Windows permissions are not checked and the warning `token file
  permissions could not be verified on this platform` is added.

Token file grammar, a deliberately small dotenv subset:

- Exactly one line of the form `DISCORD_BOT_TOKEN=value`. Whitespace around
  the key and value is trimmed.
- Blank lines and lines whose first non-space character is `#` are ignored.
  Inline comments after the value are not supported.
- Any other assignment, a line without `=`, an `export` prefix, or a second
  `DISCORD_BOT_TOKEN` line is an error.
- The value may be bare or wrapped in matching single or double quotes. A
  bare value may not contain spaces, tabs, `#`, `\`, `$`, `'`, or `"`. A
  quoted value may not contain its own quote character, `\`, or `$`.

```sh
umask 077
printf 'DISCORD_BOT_TOKEN=%s\n' "$TOKEN" > ~/.config/agent-cli-discord/token.env
chmod 600 ~/.config/agent-cli-discord/token.env
```

## Audit logging

Logging is off unless `config.json` has a `log` object. When enabled, the
destination is opened in append mode and created with mode `0600`; its parent
directory must already exist and it must be a regular file. Each completed
Discord command appends one JSON object and a newline with these string
fields: `schema_version` (`"1"`), `timestamp` (RFC 3339, UTC), `level`,
`event` (`"command.completed"`), `command` (for example `"messages post"`),
`outcome` (`"success"` or `"failure"`), `guild_id`, and when relevant
`channel_id`, `message_id`, or `thread_id`. Events never contain raw
arguments, tokens, message content, attachment content or paths, thread
names, or emoji. `version` is not logged.

Logging is fail-closed; all cases use code `log.unavailable` and exit 2:

| Message | When |
|---------|------|
| `could not open configured audit log` | before credentials are read or any request is made |
| `could not append to configured audit log` | the credential step failed and that failure could not be recorded |
| `command failed and its audit event could not be recorded` | the Discord command failed and the failure could not be recorded |
| `command completed but its audit event could not be recorded` | the Discord command succeeded; the success document is suppressed but the Discord side effect stands |

Configuration errors happen before the log is opened and are never logged.

## Bot permissions in Discord

Grant only what the allowed channels and threads need. Reading needs View
Channel and Read Message History. Posting needs Send Messages. Creating
public threads needs Create Public Threads; posting in threads needs Send
Messages in Threads. Adding a reaction whose emoji is not already on the
message may need Add Reactions. The Message Content privileged intent must be
enabled for the application for human message bodies to be returned;
without it `content_may_be_unavailable` will be `true` on those messages.
Discord permissions remain the primary boundary and the local allowlists
narrow it further.
