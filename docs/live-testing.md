# Live Discord test plan

The ordinary `go test ./...` suite uses local HTTP fixtures and does not call
Discord. This plan is the release gate for exercising the CLI against Discord's
production REST API. It deliberately uses a disposable guild and bot: a live
API test must never use a production community, production channel, or
production bot credential.

The operator owns the guild, bot application, test data, and cleanup. Run the
plan manually, serially, and once per candidate build. Do not turn it into a
scheduled or high-volume test.

Steps are labeled by where the tester performs them:

- **[Discord UI]** means the Discord website, desktop/mobile application, or
  Developer Portal—not this CLI.
- **[Shell]** means local setup, evidence inspection, or configuration editing.
- **[CLI]** means an invocation of the candidate `agent-cli-discord` binary and
  therefore, except for `version`, a possible Discord REST API request.

## Safety envelope

**[Discord UI]** Create one private test guild with these resources:

- a dedicated bot application used by no other environment;
- `cli-live-test`, a text channel visible only to the operator and bot;
- `cli-not-allowed`, a second private text channel used only to prove the local
  allowlist fails closed; and
- three harmless seed messages in `cli-live-test`, posted by the operator. Put
  the unique run ID in each message body, for example
  `live-20260903T120000Z seed-message-1` through `seed-message-3`.

Install the bot with the `bot` scope only. Do not grant Administrator, Manage
Guild, Manage Channels, Manage Messages, Manage Threads, mention permissions,
or webhook permissions. Grant only:

- View Channel and Read Message History;
- Send Messages;
- Create Public Threads and Send Messages in Threads; and
- Add Reactions, if the reaction test requires it.

Enable Message Content for this dedicated application if the read tests are
expected to return human-authored content. If it is intentionally disabled,
record that choice and expect `content_may_be_unavailable: true` for an
otherwise empty human-authored message rather than treating absent content as a
successful content test.

The relevant Discord contracts are the official documentation for
[rate limits](https://discord.com/developers/docs/topics/rate-limits),
[permissions](https://discord.com/developers/docs/topics/permissions),
[messages](https://discord.com/developers/docs/resources/message), and
[channels and threads](https://discord.com/developers/docs/resources/channel).

During the run:

- issue one command at a time and wait at least two seconds between commands;
- do not deliberately provoke a 429 or run loops, concurrency, fuzzing, or
  load tests against Discord;
- use only small UTF-8 text files and a single attachment under 1 KiB with no
  private data;
- never place the token in command arguments, shell history, captured output,
  an issue, or a commit; and
- stop the run on any 429, unexpected 5xx response, permission spill into a
  non-test channel, notification, duplicate mutation, or unexplained audit-log
  failure.

The client may retry an idempotent read, reaction, or membership request up to
two times when Discord supplies a valid rate-limit delay. It does not retry
message or thread creation. If a non-idempotent command returns
`outcome_unknown: true`, inspect the channel before doing anything else. Record
the observed message or thread ID if it exists; do not blindly repeat the
command.

## Release gate and setup

Start only from a clean checkout of the exact commit being evaluated. All four
local checks must pass before a token is installed:

```sh
GOCACHE=/tmp/agent-cli-discord-go-cache go test ./...
GOCACHE=/tmp/agent-cli-discord-go-cache go test -race ./...
GOCACHE=/tmp/agent-cli-discord-go-cache go vet ./...
gofmt -d cmd internal
```

`gofmt -d` passes only when it prints nothing. Then use **[Shell]** to create the
ignored runtime tree and build the candidate:

```sh
mkdir -p .local/live-test/agent-cli-discord .local/live-test/fixtures
cp examples/live-test/config.example.json .local/live-test/agent-cli-discord/config.json
cp examples/live-test/token.env.example .local/live-test/agent-cli-discord/token.env
chmod 600 .local/live-test/agent-cli-discord/config.json
chmod 600 .local/live-test/agent-cli-discord/token.env
CGO_ENABLED=0 go build -trimpath -o .local/live-test/agent-cli-discord-bin ./cmd/agent-cli-discord
```

**[Shell]** Replace the examples with the disposable guild ID, only the
`cli-live-test` channel ID, and the dedicated bot token. Keep
`allowed_thread_ids` omitted at first so this run can create a thread. Keep the
audit path below `.local/`. Confirm `git status --short` does not show any
credential, configuration, fixture, log, or binary.

For the commands below, set these convenience variables to non-secret IDs and
the unique run label. Do not set the token as a convenience variable.

```sh
CLI=.local/live-test/agent-cli-discord-bin
LIVE_CONFIG_ROOT="$PWD/.local/live-test"
TEST_CHANNEL_ID=replace-with-cli-live-test-channel-id
DENIED_CHANNEL_ID=replace-with-cli-not-allowed-channel-id
OPERATOR_USER_ID=replace-with-operator-test-account-id
RUN_ID=live-YYYYMMDDTHHMMSSZ
```

### Evidence capture

**[Shell]** Create harmless inputs and a private evidence directory:

```sh
printf '%s\n' "$RUN_ID plain-message" >.local/live-test/fixtures/plain.txt
printf '%s\n' "$RUN_ID attachment-message" >.local/live-test/fixtures/attachment-message.txt
printf '%s\n' "$RUN_ID harmless attachment" >.local/live-test/fixtures/attachment.txt
printf '%s\n' "$RUN_ID literal-mention <@$OPERATOR_USER_ID>" >.local/live-test/fixtures/mention.txt
printf '%s\n' "$RUN_ID reply" >.local/live-test/fixtures/reply.txt
EVIDENCE_DIR=".local/live-test/evidence/$RUN_ID"
mkdir -p "$EVIDENCE_DIR"
chmod 700 "$EVIDENCE_DIR"
```

Define this function in the same shell. It runs one command, keeps the streams
separate, records the numeric exit status, and prints only a one-line summary.
It intentionally does not print JSON that may contain message data.

```sh
run_case() {
  case_name=$1
  shift
  stdout_path="$EVIDENCE_DIR/$case_name.stdout.json"
  stderr_path="$EVIDENCE_DIR/$case_name.stderr.json"
  status_path="$EVIDENCE_DIR/$case_name.status"

  if XDG_CONFIG_HOME="$LIVE_CONFIG_ROOT" "$@" >"$stdout_path" 2>"$stderr_path"; then
    command_status=0
  else
    command_status=$?
  fi
  printf '%s\n' "$command_status" >"$status_path"
  printf '%s status=%s\n' "$case_name" "$command_status"
}
```

For a successful case, these checks must all succeed:

```sh
test "$(cat "$EVIDENCE_DIR/01-version.status")" -eq 0
test ! -s "$EVIDENCE_DIR/01-version.stderr.json"
jq -e '.ok == true' "$EVIDENCE_DIR/01-version.stdout.json"
```

For an expected application failure, use the inverse stream checks and inspect
the stable error code:

```sh
test "$(cat "$EVIDENCE_DIR/20-denied-post.status")" -eq 2
test ! -s "$EVIDENCE_DIR/20-denied-post.stdout.json"
jq -e '.ok == false and .error.code == "policy.channel_not_authorized"' \
  "$EVIDENCE_DIR/20-denied-post.stderr.json"
```

Apply the corresponding three checks to every case. Review captured JSON
locally with `jq`; sanitize message data and IDs before sharing it. Never copy
the token into the evidence directory. `run_case` supplies the isolated config
root automatically. Command blocks below show sequence, not a batch script: run
one `run_case`, validate its status and streams, wait two seconds, and only then
run the following line or extract its ID.

## Test sequence

### 1. Identity and visibility (no mutations)

**[CLI]** Run:

```sh
run_case 01-version "$CLI" version
run_case 02-auth-check "$CLI" auth check
run_case 03-channels-list "$CLI" channels list
run_case 04-threads-list-initial "$CLI" threads list
```

**[Shell]** Confirm `01` reports the expected executable and schema versions;
`02` is the dedicated bot with `bot: true`; `03` contains exactly
`TEST_CHANNEL_ID`; and every result in `04`, if any, has the configured guild
and allowed parent. Prefer starting with no active test threads.

### 2. Message reads and pagination (no mutations)

**[Discord UI]** Post these three messages, in order, from the operator account:

```text
RUN_ID seed-message-1
RUN_ID seed-message-2
RUN_ID seed-message-3
```

Replace `RUN_ID` with its value. Enable Discord Developer Mode, use Copy Message
ID, and set `SEED_1_ID`, `SEED_2_ID`, and `SEED_3_ID` in **[Shell]**. IDs are
non-secret.

**[CLI]** Run:

```sh
run_case 10-read-limit "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 2
READ_BEFORE=$(jq -r '.data.cursor.before' "$EVIDENCE_DIR/10-read-limit.stdout.json")
run_case 11-read-before "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 2 --before "$READ_BEFORE"
run_case 12-read-after "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 2 --after "$SEED_1_ID"
run_case 13-read-around "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 3 --around "$SEED_2_ID"
run_case 14-read-empty "$CLI" messages read --channel "$TEST_CHANNEL_ID" --after "$SEED_3_ID"
run_case 15-get-seed "$CLI" messages get --channel "$TEST_CHANNEL_ID" --message "$SEED_2_ID"
```

**[Shell]** Confirm:

- results are oldest-to-newest within each page;
- every message has the configured channel ID and expected author, timestamp,
  attachment, embed, and nullable fields;
- cursors select the expected neighboring seed messages; and
- case `14` is `messages: []` with no cursor; and
- case `15` matches the same message in a read response.

If Message Content is enabled, confirm all three exact seed strings are present.

### 3. Local authorization boundary (no mutation expected)

**[CLI]** Attempt a post to the denied channel:

```sh
run_case 20-denied-post "$CLI" messages post --channel "$DENIED_CHANNEL_ID" --file .local/live-test/fixtures/plain.txt
```

**[Shell]** Expect exit `2` and `policy.channel_not_authorized` as shown in the
evidence example above. **[Discord UI]** Confirm no message appeared in
`cli-not-allowed`. The implementation may make one metadata GET to distinguish
a thread from a channel, but it must not make a message-content or mutation
request.

Do not use a real channel from another guild for negative testing.

### 4. Messages and reactions (controlled mutations)

**[CLI]** Run each mutation once, extracting IDs only after its success checks
pass:

```sh
run_case 30-post "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file .local/live-test/fixtures/plain.txt
POST_ID=$(jq -r '.data.id' "$EVIDENCE_DIR/30-post.stdout.json")
run_case 31-post-attachment "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file .local/live-test/fixtures/attachment-message.txt --attach .local/live-test/fixtures/attachment.txt
ATTACHMENT_POST_ID=$(jq -r '.data.id' "$EVIDENCE_DIR/31-post-attachment.stdout.json")
run_case 32-post-mention "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file .local/live-test/fixtures/mention.txt
run_case 33-reply "$CLI" messages reply --channel "$TEST_CHANNEL_ID" --message "$SEED_2_ID" --file .local/live-test/fixtures/reply.txt
REPLY_ID=$(jq -r '.data.id' "$EVIDENCE_DIR/33-reply.stdout.json")
run_case 34-reaction-add "$CLI" reactions add --channel "$TEST_CHANNEL_ID" --message "$POST_ID" --emoji '✅'
run_case 35-reaction-remove "$CLI" reactions remove --channel "$TEST_CHANNEL_ID" --message "$POST_ID" --emoji '✅'
run_case 36-get-post "$CLI" messages get --channel "$TEST_CHANNEL_ID" --message "$POST_ID"
run_case 37-read-created "$CLI" messages read --channel "$TEST_CHANNEL_ID" --around "$POST_ID"
```

**[Discord UI]** After each mutation, confirm exactly one expected change. In
particular, verify the attachment's filename and bytes, that the literal mention
did not notify the operator, that the reply references seed message 2 without
notifying the operator, and that only the bot's reaction appears and disappears.
**[Shell]** Compare cases `36` and `37` with case `30` and the UI.

### 5. Thread lifecycle (controlled mutations)

**[CLI]** Create and exercise one thread:

```sh
run_case 40-thread-create "$CLI" threads create --channel "$TEST_CHANNEL_ID" --name "$RUN_ID cli-thread" --auto-archive 60
THREAD_ID=$(jq -r '.data.id' "$EVIDENCE_DIR/40-thread-create.stdout.json")
run_case 41-threads-list "$CLI" threads list
run_case 42-thread-post "$CLI" messages post --channel "$THREAD_ID" --file .local/live-test/fixtures/plain.txt
THREAD_MESSAGE_ID=$(jq -r '.data.id' "$EVIDENCE_DIR/42-thread-post.stdout.json")
run_case 43-thread-read "$CLI" messages read --channel "$THREAD_ID" --around "$THREAD_MESSAGE_ID"
run_case 44-thread-get "$CLI" messages get --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID"
run_case 45-thread-reply "$CLI" messages reply --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID" --file .local/live-test/fixtures/reply.txt
run_case 46-thread-reaction-add "$CLI" reactions add --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID" --emoji '✅'
run_case 47-thread-reaction-remove "$CLI" reactions remove --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID" --emoji '✅'
run_case 48-thread-leave "$CLI" threads leave --thread "$THREAD_ID"
run_case 49-thread-join "$CLI" threads join --thread "$THREAD_ID"
```

**[Shell]** Confirm the create response has the configured guild and parent,
type `11`, the exact name, and archive duration `60`; the list contains it; and
the message responses agree. **[Discord UI]** Confirm each message, reaction,
and membership change after its command.

**[Shell]** Edit `config.json` to add `"allowed_thread_ids": ["THREAD_ID"]`,
using the actual ID, then run:

```sh
run_case 50-allowed-thread-read "$CLI" messages read --channel "$THREAD_ID" --limit 1
run_case 51-restricted-create "$CLI" threads create --channel "$TEST_CHANNEL_ID" --name "$RUN_ID must-not-exist"
```

Case `50` must succeed. Case `51` must exit `2` with
`policy.thread_creation_restricted`. **[Discord UI]** Confirm it created no
second thread.

**[Discord UI]** Manually create a second disposable public thread under
`cli-live-test`, put the run ID in its name, copy its ID, and set
`DENIED_THREAD_ID` in **[Shell]**. Then use **[CLI]**:

```sh
run_case 52-denied-thread-read "$CLI" messages read --channel "$DENIED_THREAD_ID" --limit 1
```

Expect exit `2` with `policy.channel_not_authorized` and no message content in
the error. **[Discord UI]** Delete this manually created thread during cleanup.

### 6. Audit evidence

**[Shell]** Parse every line of `.local/live-test/audit.jsonl` as a separate JSON
object:

```sh
jq -c . .local/live-test/audit.jsonl >/dev/null
stat -c '%a %n' .local/live-test/audit.jsonl
jq -c 'keys' .local/live-test/audit.jsonl
```

There must be one completion event per Discord command, including expected
failures. Confirm timestamps, normalized command names, outcomes, guild ID, and
applicable input IDs. Confirm the file mode is `0600` and that the log contains
no token, Authorization header, message content, attachment path or bytes,
thread name, or emoji.

The audit record for a successful create may contain only IDs known before the
request; use captured command output as the authoritative record of newly
created message and thread IDs.

## Optional least-privilege checks

Run these only in the disposable guild, after the main sequence, and one at a
time. **[Discord UI]** Remove Send Messages in `cli-live-test` and wait for the
permission state to be visible. **[CLI]** Run:

```sh
run_case 60-no-send-permission "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file .local/live-test/fixtures/plain.txt
```

**[Shell]** Confirm a structured 403 failure. **[Discord UI]** Confirm no message
was created and restore Send Messages. Repeat the pattern by removing Read
Message History, then use **[CLI]**:

```sh
run_case 61-no-history-permission "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 1
```

These checks validate the guild configuration, not stable CLI error codes, so
record Discord's actual 403 response and do not require a particular application
error beyond a structured failure. **[Discord UI]** Restore the permission.

Never test revoked permissions by widening channel visibility or granting
Administrator.

## Cleanup and acceptance

**[Discord UI]** Delete the created messages and both threads; deletion is
intentionally outside the CLI command surface. Remove the bot from the guild
and reset its token if the application will not be retained exclusively for
future isolated tests. **[Shell]** Remove `.local/live-test` only after sanitized
evidence has been recorded.

The live gate passes only when:

- all local checks and every non-optional test above passed;
- only the expected resources were created, no one was notified, and all
  created state was removed;
- no 429, unexplained 5xx, duplicate mutation, or `outcome_unknown` result
  occurred;
- the allowlists blocked both the denied channel and denied thread tests;
- the audit log was complete and disclosed none of the prohibited data; and
- the evidence records commit, build command, UTC run time, bot application ID,
  guild ID, test channel IDs, created resource IDs, result per step, cleanup
  confirmation, deviations, and operator name, with the credential redacted.

Any stop condition makes the run inconclusive, not passed. Preserve sanitized
evidence, reconcile and clean up Discord state, investigate locally, and begin
a new run rather than continuing from a partially understood state.

## Release-user example

The files directly under `examples/` are templates for normal installations.
Copy `config.example.json` to the platform configuration directory as
`agent-cli-discord/config.json`. Either export `DISCORD_BOT_TOKEN` in the calling
environment or copy `token.env.example` beside the configuration as
`token.env`, replace its placeholder, and restrict it to the current user.
