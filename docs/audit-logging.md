# Audit logging

Audit logging is disabled when the configuration has no `log` object. Enable it
with an explicit destination and level:

```json
{
  "log": {
    "path": "/var/log/agent-cli-discord.jsonl",
    "level": "info"
  }
}
```

The destination is opened in append mode and created with mode `0600`. It must
be a regular file. Each completed Discord command appends one JSON object and a
newline. Version 1 events contain these string fields:

- `schema_version`, currently `"1"`
- `timestamp`, an RFC 3339 timestamp in UTC
- `level`, the configured log level
- `event`, currently `"command.completed"`
- `command`, a normalized command name such as `"messages post"`
- `outcome`, either `"success"` or `"failure"`
- `guild_id` and, when relevant, `channel_id`, `message_id`, or `thread_id`

Events never contain raw command arguments, tokens, authorization headers,
message content, attachment content or paths, thread names, or emoji.

Logging is fail-closed. If the destination cannot be opened, the command fails
with `log.unavailable` before credentials are loaded or a Discord request is
made. If appending fails after execution, the normal result is suppressed and
`log.unavailable` is returned. For a mutating command, that failure does not
mean the Discord operation was rolled back; the error message distinguishes
this case.
