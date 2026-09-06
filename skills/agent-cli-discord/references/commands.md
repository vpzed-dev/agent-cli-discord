# Command constraints

Syntax is in [SKILL.md](../SKILL.md); output shapes and recovery are in
[output-and-errors.md](output-and-errors.md).

## Identity and discovery

Only `version` is offline and reads no configuration or credentials.
It reports a release tag, source pseudo-version, dirty build suffix, or `dev`.
`auth check` rejects non-bot identities.
`channels list` includes only configured allowed channels.
`threads list` includes only active threads passing parent and thread
allowlists; archived threads are excluded.

## Message targets

Message and reaction commands accept allowed channels or authorized threads:

1. An ID in `allowed_channel_ids` is used directly.
2. Otherwise the CLI fetches channel metadata and requires the same ID,
   configured guild, thread type (10, 11, or 12), thread metadata, an allowed
   parent, and membership in any nonempty `allowed_thread_ids`.
3. Failure is `policy.channel_not_authorized`; no message read or mutation
   occurs before authorization succeeds.

## Messages and attachments

For history, `--before`, `--after`, and `--around` are mutually exclusive.
`messages get` requires both channel and message IDs.
Replies require a message ID in the target channel; replying to a deleted
message fails with `discord.http_error`.

Content is read before configuration. It is sent verbatim, including
newlines, and must satisfy the limits in the main skill.
`--file` must name a readable regular file.

Attachments are checked after target authorization:

- At most 10 regular files, 10 MiB each and 24 MiB combined.
- Unreadable paths: `attachment.unavailable`.
- Non-regular files, files over 10 MiB, or basenames containing CR/LF:
  `attachment.invalid`.
- Combined size over 24 MiB: `attachment.too_large`.

Each uploaded filename is its path's basename.

## Reactions

A value containing `:` must be custom emoji `name:id`, with name matching
`[A-Za-z0-9_]{2,32}` and a snowflake ID. Animated `a:name:id` and
`:shortcode:` forms are rejected.
Otherwise pass Unicode containing at least one non-ASCII printable character,
with no slash, backslash, CR, or LF.
Both commands affect only the bot's reaction. Adding a new emoji to a message
may require Add Reactions permission.

## Threads

Creation requires an allowed parent and absent or empty `allowed_thread_ids`.
A nonempty list causes `policy.thread_creation_restricted` before network
access. Names must contain 1–100 UTF-8 characters.
Creation always makes a public thread (type 11). A returned guild or parent
mismatch causes `discord.invalid_response` after the creation request.

Join/leave fetch metadata and require the configured guild, thread type
10/11/12, an allowed parent, and any explicit thread allowlist.
Policy failure is `policy.thread_not_authorized`; archived threads fail with
`discord.thread_archived`.
