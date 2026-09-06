#!/usr/bin/env bash
# Guided, evidence-capturing run of docs/live-testing.md against a disposable
# Discord guild and bot. It runs every [CLI] step itself, validates each
# captured result, asks the operator to confirm each [Discord UI] observation,
# checks the audit log, and writes a verdict report into the evidence directory.
#
#   scripts/live-test.sh [--skip-gates]
#
# --skip-gates skips the four local Go checks. That is recorded as a deviation,
# so such a run can never PASS; use it only while working on this script.
#
# The script never reads the bot token value. It only checks that token.env has
# a non-placeholder DISCORD_BOT_TOKEN line and restrictive permissions.

set -u -o pipefail

# ---------------------------------------------------------------- terminal ---
if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ $(tput colors 2>/dev/null || echo 0) -ge 8 ]]; then
  BOLD=$(tput bold) DIM=$(tput dim 2>/dev/null || true) RED=$(tput setaf 1) GREEN=$(tput setaf 2)
  YELLOW=$(tput setaf 3) BLUE=$(tput setaf 4) CYAN=$(tput setaf 6) RESET=$(tput sgr0)
else
  BOLD='' DIM='' RED='' GREEN='' YELLOW='' BLUE='' CYAN='' RESET=''
fi

say()    { printf '%s\n' "$*"; }
note()   { printf '%s  %s%s\n' "$DIM" "$*" "$RESET"; }
ok()     { printf '%s  ✔ %s%s\n' "$GREEN" "$*" "$RESET"; }
warn()   { printf '%s  ! %s%s\n' "$YELLOW" "$*" "$RESET"; }
bad()    { printf '%s  ✘ %s%s\n' "$RED" "$*" "$RESET"; }
header() { printf '\n%s%s══ %s ══%s\n' "$BOLD" "$BLUE" "$*" "$RESET"; }
phase()  { printf '\n%s%s▶ Phase %s of %s — %s%s\n' "$BOLD" "$CYAN" "$1" "$PHASE_COUNT" "$2" "$RESET"; }
ui()     { printf '\n%s  [Discord UI]%s %s\n' "$BOLD" "$RESET" "$1"; }

read_line() { # read_line VAR PROMPT — aborts when input is closed
  local __var=$1
  printf '%s  ? %s%s' "$CYAN" "$2" "$RESET"
  if ! IFS= read -r "${__var?}"; then
    printf '\n'
    abort_run "input closed while waiting for the operator"
  fi
}

pause() { local _x; read_line _x "${1:-Press Enter to continue} ↵ "; }

ask_yes_no() { # ask_yes_no PROMPT -> 0 yes, 1 no
  local a
  while :; do
    read_line a "$1 [y/n] "
    case ${a,,} in y|yes) return 0 ;; n|no) return 1 ;; esac
  done
}

ask_typed() { # ask_typed WORD PROMPT -> 0 when the operator types WORD exactly
  local a
  read_line a "$2 (type $1 to confirm, anything else to go back): "
  [[ $a == "$1" ]]
}

ask_value() { # ask_value VAR PROMPT [DEFAULT]
  local __var=$1 prompt=$2 def=${3:-} a
  while :; do
    if [[ -n $def ]]; then read_line a "$prompt [$def]: "; else read_line a "$prompt: "; fi
    a=${a:-$def}
    [[ -n $a ]] && { printf -v "$__var" '%s' "$a"; return 0; }
  done
}

is_snowflake() { [[ $1 =~ ^[0-9]{17,20}$ ]]; }

ask_snowflake() { # ask_snowflake VAR PROMPT
  local __var=$1 a
  while :; do
    read_line a "$2: "
    a=${a//[[:space:]]/}
    if is_snowflake "$a"; then printf -v "$__var" '%s' "$a"; return 0; fi
    warn "'$a' is not a Discord snowflake (17-20 digits)"
  done
}

ask_choice() { # ask_choice PROMPT KEY LABEL [KEY LABEL ...] -> CHOICE
  local prompt=$1 a; shift
  local keys=()
  while (($# >= 2)); do printf '      [%s] %s\n' "$1" "$2"; keys+=("$1"); shift 2; done
  while :; do
    read_line a "$prompt (${keys[*]}): "
    for k in "${keys[@]}"; do [[ ${a,,} == "$k" ]] && { CHOICE=$k; return 0; }; done
  done
}

# ------------------------------------------------------------------- state ---
PHASE_COUNT=10
SKIP_GATES=0
for arg in "$@"; do
  case $arg in
    --skip-gates) SKIP_GATES=1 ;;
    -h|--help) sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) printf 'unknown option: %s\n' "$arg" >&2; exit 64 ;;
  esac
done

declare -A ATTEMPTS=() RESULTS=() FAILURE_NOTES=() UI_RESULTS=() UI_TEXT=()
CASE_ORDER=() UI_ORDER=() DEVIATIONS=() EXEC_LOG=() CREATED=() CASE_FAILURES=()
CASE_CHECKS=0 LAST_CLI='' CHOICE=''
ABORT_REASON='' REPORT_WRITTEN=0 CONFIG_MODIFIED=0 AUDIT_RESULT=pending AUDIT_NOTES=''
SCAN_RESULT=pending CLEANUP_MESSAGES=unconfirmed CLEANUP_THREADS=unconfirmed CLEANUP_BOT=''
OPTIONAL_RAN=no GATES_RESULT=''
RUN_ID='' EVIDENCE_DIR='' GATES_DIR='' COMMIT='' TREE_STATE='' OPERATOR='' START_UTC='' END_UTC=''
BUILD_CMD='' BIN_SHA256='' BIN_VERSION='' MESSAGE_CONTENT=''
GUILD_ID='' TEST_CHANNEL_ID='' DENIED_CHANNEL_ID='' OPERATOR_USER_ID='' BOT_ID=''
SEED_1_ID='' SEED_2_ID='' SEED_3_ID='' READ_BEFORE='' POST_ID='' ATTACHMENT_POST_ID='' MENTION_POST_ID=''
REPLY_ID='' THREAD_ID='' THREAD_MESSAGE_ID='' THREAD_REPLY_ID='' DENIED_THREAD_ID='' ATTACH_SIZE=0
AUDIT_PATH='' AUDIT_MOVED=''

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || { printf 'run this script from inside the repository\n' >&2; exit 64; }
cd "$REPO_ROOT" || exit 64
umask 077
export GOCACHE=${GOCACHE:-/tmp/agent-cli-discord-go-cache}

LIVE_ROOT=.local/live-test
LIVE_CONFIG_ROOT="$REPO_ROOT/$LIVE_ROOT"
CONFIG_DIR=$LIVE_ROOT/agent-cli-discord
CONFIG=$CONFIG_DIR/config.json
TOKEN_ENV=$CONFIG_DIR/token.env
FIXTURES=$LIVE_ROOT/fixtures
CLI=$LIVE_ROOT/agent-cli-discord-bin
BUILD_CMD="CGO_ENABLED=0 go build -trimpath -o $CLI ./cmd/agent-cli-discord"
TOKEN_SHAPE='[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{20,}'

# --------------------------------------------------------- abort / cleanup ---
restore_config() {
  if (( CONFIG_MODIFIED )) && [[ -f $EVIDENCE_DIR/config.snapshot.json ]]; then
    cp "$EVIDENCE_DIR/config.snapshot.json" "$CONFIG" && chmod 600 "$CONFIG" && CONFIG_MODIFIED=0
    note "config.json restored from snapshot (allowed_thread_ids removed)"
  fi
}

abort_run() {
  ABORT_REASON=$1
  printf '\n'
  bad "RUN ABORTED: $1"
  restore_config
  END_UTC=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  finish_audit_evidence quiet
  write_report
  say ""
  say "Verdict: ${RED}${BOLD}INCONCLUSIVE${RESET}. Reconcile Discord state, preserve sanitized evidence, and begin a new run."
  [[ -n $EVIDENCE_DIR ]] && say "Evidence: $EVIDENCE_DIR"
  exit 1
}

on_exit() { restore_config; }
trap on_exit EXIT
trap 'abort_run "interrupted by operator (SIGINT)"' INT

# ------------------------------------------------------------- evidence io ---
add_deviation() { DEVIATIONS+=("$1"); warn "deviation recorded: $1"; }
add_created() { CREATED+=("$1"); }
add_failure() { CASE_FAILURES+=("$1"); }

cli_pace() {
  local now; now=$(date +%s)
  if [[ -n $LAST_CLI ]] && (( now - LAST_CLI < 2 )); then sleep $((2 - (now - LAST_CLI))); fi
  LAST_CLI=$(date +%s)
}

run_case() { # run_case NAME CLI ARGS...  (same contract as docs/live-testing.md run_case)
  local name=$1; shift
  local attempt=$(( ${ATTEMPTS[$name]:-0} + 1 ))
  ATTEMPTS[$name]=$attempt
  [[ -n ${RESULTS[$name]:-} ]] || CASE_ORDER+=("$name")
  local out=$EVIDENCE_DIR/$name.stdout.json err=$EVIDENCE_DIR/$name.stderr.json st=$EVIDENCE_DIR/$name.status
  if (( attempt > 1 )); then
    local s; for s in stdout.json stderr.json status; do
      [[ -f $EVIDENCE_DIR/$name.$s ]] && mv -f "$EVIDENCE_DIR/$name.$s" "$EVIDENCE_DIR/$name.attempt$((attempt - 1)).$s"
    done
  fi
  local normalized="$2${3:+ $3}"
  printf '  %s$ agent-cli-discord %s%s\n' "$DIM" "${*:2}" "$RESET"
  cli_pace
  local status
  if XDG_CONFIG_HOME="$LIVE_CONFIG_ROOT" "$@" >"$out" 2>"$err"; then status=0; else status=$?; fi
  printf '%s\n' "$status" >"$st"
  EXEC_LOG+=("$name"$'\t'"$attempt"$'\t'"$normalized"$'\t'"$status")
  note "$name status=$status"
  stop_condition_scan "$name"
}

stop_condition_scan() { # aborts on 429, 5xx, or outcome_unknown in the captured error
  local err=$EVIDENCE_DIR/$1.stderr.json
  [[ -s $err ]] || return 0
  local why
  why=$(jq -r '.error // {} | if .outcome_unknown == true then "outcome_unknown"
      elif (.http_status // 0) == 429 then "HTTP 429 rate limit"
      elif (.http_status // 0) >= 500 then "HTTP \(.http_status)" else empty end' "$err" 2>/dev/null) || return 0
  [[ -n $why ]] || return 0
  bad "STOP CONDITION in $1: $why (error code $(jq -r '.error.code // "?"' "$err"))"
  if [[ $why == outcome_unknown ]]; then
    ui "Inspect the channel NOW. If a message or thread was created, note its ID in the evidence directory. Do not repeat the command."
    pause
  fi
  abort_run "stop condition: $why during $1"
}

jqa() { # jqa FILTER FILE — jq -e with every run variable bound
  jq -e --arg run "$RUN_ID" --arg guild "$GUILD_ID" --arg test "$TEST_CHANNEL_ID" \
    --arg denied "$DENIED_CHANNEL_ID" --arg op "$OPERATOR_USER_ID" --arg bot "$BOT_ID" \
    --arg s1 "$SEED_1_ID" --arg s2 "$SEED_2_ID" --arg s3 "$SEED_3_ID" \
    --arg post "$POST_ID" --arg apost "$ATTACHMENT_POST_ID" --arg mpost "$MENTION_POST_ID" \
    --arg reply "$REPLY_ID" --arg thread "$THREAD_ID" --arg tmsg "$THREAD_MESSAGE_ID" \
    --arg dthread "$DENIED_THREAD_ID" --argjson attach_size "${ATTACH_SIZE:-0}" \
    "$1" "${@:2}" >/dev/null 2>&1
}

status_of() { cat "$EVIDENCE_DIR/$1.status" 2>/dev/null || echo "?"; }

expect_ok() { # status 0, empty stderr, ok:true
  local n=$1 s; s=$(status_of "$n")
  CASE_CHECKS=$((CASE_CHECKS + 3))
  [[ $s == 0 ]] || add_failure "exit status $s, expected 0"
  [[ ! -s $EVIDENCE_DIR/$n.stderr.json ]] || add_failure "stderr is not empty: $(jq -r '.error.code // "unparseable"' "$EVIDENCE_DIR/$n.stderr.json" 2>/dev/null)"
  jqa '.ok == true and has("data")' "$EVIDENCE_DIR/$n.stdout.json" || add_failure "stdout is not a single ok:true document"
}

expect_error() { # expect_error NAME CODE — status 2, empty stdout, ok:false with CODE
  local n=$1 code=$2 s; s=$(status_of "$n")
  CASE_CHECKS=$((CASE_CHECKS + 3))
  [[ $s == 2 ]] || add_failure "exit status $s, expected 2"
  [[ ! -s $EVIDENCE_DIR/$n.stdout.json ]] || add_failure "stdout is not empty (a success document was written)"
  jqa --arg code "$code" '.ok == false and .error.code == $code' "$EVIDENCE_DIR/$n.stderr.json" \
    || add_failure "stderr error code is $(jq -r '.error.code // "unparseable"' "$EVIDENCE_DIR/$n.stderr.json" 2>/dev/null), expected $code"
}

check() { # check NAME FILTER DESCRIPTION — assert FILTER on NAME.stdout.json
  CASE_CHECKS=$((CASE_CHECKS + 1))
  jqa "$2" "$EVIDENCE_DIR/$1.stdout.json" || add_failure "$3"
}

check_err() { # check_err NAME FILTER DESCRIPTION — assert FILTER on NAME.stderr.json
  CASE_CHECKS=$((CASE_CHECKS + 1))
  jqa "$2" "$EVIDENCE_DIR/$1.stderr.json" || add_failure "$3"
}

check_pair() { # check_pair NAME_A NAME_B FILTER DESCRIPTION — FILTER over [A.stdout, B.stdout]
  CASE_CHECKS=$((CASE_CHECKS + 1))
  jqa -s "$3" "$EVIDENCE_DIR/$1.stdout.json" "$EVIDENCE_DIR/$2.stdout.json" || add_failure "$4"
}

check_no_leak() { # check_no_leak NAME — stderr must not carry message data or the run ID
  CASE_CHECKS=$((CASE_CHECKS + 1))
  if grep -q -F "$RUN_ID" "$EVIDENCE_DIR/$1.stderr.json"; then add_failure "stderr contains the run ID (message data leaked into an error)"; fi
}

# jq fragments shared by message-read checks
ORDERED='([.data.messages[].id] as $i | $i == ($i | sort_by([length, .])))'
IN_TEST='all(.data.messages[]; .channel_id == $test)'
IN_THREAD='all(.data.messages[]; .channel_id == $thread)'
SHAPE='all(.data.messages[]; (.id|type=="string") and (.channel_id|type=="string") and (.timestamp|type=="string")
  and (.author.id|type=="string") and (.author.username|type=="string") and (.author.bot|type=="boolean")
  and has("edited_timestamp") and (.attachments|type=="array") and (.embeds|type=="array") and (.type|type=="number"))'
SEED_AUTHOR='all(.data.messages[] | select(.id==$s1 or .id==$s2 or .id==$s3); .author.id == $op and .author.bot == false)'
seed_content_filter() {
  if [[ $MESSAGE_CONTENT == yes ]]; then
    printf '%s' 'all(.data.messages[] | select(.id==$s1 or .id==$s2 or .id==$s3);
      .content == ($run + " seed-message-" + (if .id==$s1 then "1" elif .id==$s2 then "2" else "3" end)))'
  else
    printf '%s' 'all(.data.messages[] | select(.id==$s1 or .id==$s2 or .id==$s3);
      (.content | startswith($run)) or (.content == "" and .content_may_be_unavailable == true))'
  fi
}

is_mutation() { case $1 in 30-*|31-*|32-*|33-*|40-*|42-*|45-*|51-*) return 0 ;; *) return 1 ;; esac; }

show_capture() {
  warn "captured JSON may contain message data; sanitize before sharing"
  for s in stdout.json stderr.json; do
    say "  --- $1.$s"; [[ -s $EVIDENCE_DIR/$1.$s ]] && jq . "$EVIDENCE_DIR/$1.$s" 2>/dev/null || say "  (empty)"
  done
  say "  --- status: $(status_of "$1")"
}

step() { # step CASE_NAME CASE_FUNCTION — runs the case, validates, offers retry/continue/abort
  local name=$1 fn=$2
  while :; do
    CASE_FAILURES=(); CASE_CHECKS=0
    "$fn"
    if (( ${#CASE_FAILURES[@]} == 0 )); then
      RESULTS[$name]=PASS; ok "$name passed ($CASE_CHECKS checks)"; return 0
    fi
    RESULTS[$name]=FAIL
    FAILURE_NOTES[$name]=$(IFS='; '; printf '%s' "${CASE_FAILURES[*]}")
    bad "$name FAILED:"; local f; for f in "${CASE_FAILURES[@]}"; do printf '      - %s\n' "$f"; done
    while :; do
      ask_choice "Next" i "show captured output" r "retry this case (recorded as a deviation)" \
        c "continue with $name marked FAILED" a "abort the run (INCONCLUSIVE)"
      case $CHOICE in
        i) show_capture "$name" ;;
        r) if is_mutation "$name"; then
             warn "$name may have mutated Discord. Inspect the channel first; a repeat must not create a duplicate."
             ask_typed INSPECTED "I inspected Discord and a retry will not duplicate a mutation" || continue
           fi
           add_deviation "$name retried after failure (attempt ${ATTEMPTS[$name]}: ${FAILURE_NOTES[$name]})"; break ;;
        c) add_deviation "$name left FAILED: ${FAILURE_NOTES[$name]}"; return 1 ;;
        a) abort_run "operator aborted after $name failed" ;;
      esac
    done
  done
}

extract_id() { # extract_id VAR CASE_NAME JQ_PATH DESCRIPTION — aborts when the ID is unusable
  local v; v=$(jq -r "$3 // empty" "$EVIDENCE_DIR/$2.stdout.json" 2>/dev/null)
  if ! is_snowflake "$v"; then abort_run "cannot continue: no usable $4 ID in $2"; fi
  printf -v "$1" '%s' "$v"
  note "$4 ID: $v"
}

ui_confirm() { # ui_confirm KEY TEXT — records the operator's Discord UI observation
  local key=$1 text=$2
  UI_ORDER+=("$key"); UI_TEXT[$key]=$text
  ui "$text"
  if ask_yes_no "Confirmed?"; then UI_RESULTS[$key]=yes; return 0; fi
  UI_RESULTS[$key]=no
  bad "UI confirmation failed: $key"
  ask_choice "A wrong observation here is usually a stop condition" a "abort the run (INCONCLUSIVE)" c "continue and record as FAILED"
  [[ $CHOICE == a ]] && abort_run "operator could not confirm: $text"
  add_deviation "UI confirmation '$key' answered no"
  return 1
}

# ---------------------------------------------------------------- reports ---
finish_audit_evidence() { # copy the audit log into the evidence directory (idempotent)
  [[ -n $AUDIT_PATH && -f $AUDIT_PATH && -n $EVIDENCE_DIR && -d $EVIDENCE_DIR ]] || return 0
  cp -f "$AUDIT_PATH" "$EVIDENCE_DIR/audit.jsonl" && chmod 600 "$EVIDENCE_DIR/audit.jsonl"
  [[ ${1:-} == quiet ]] || note "audit log copied to $EVIDENCE_DIR/audit.jsonl"
}

compute_verdict() {
  local name
  if [[ -n $ABORT_REASON ]]; then VERDICT=INCONCLUSIVE; VERDICT_WHY=$ABORT_REASON; return; fi
  local fails=()
  for name in "${CASE_ORDER[@]}"; do [[ ${RESULTS[$name]} == FAIL ]] && fails+=("$name"); done
  for name in "${UI_ORDER[@]}"; do [[ ${UI_RESULTS[$name]} == no ]] && fails+=("ui:$name"); done
  [[ $AUDIT_RESULT == PASS ]] || fails+=("audit:$AUDIT_RESULT")
  [[ $SCAN_RESULT == PASS ]] || fails+=("evidence-scan:$SCAN_RESULT")
  [[ $CLEANUP_MESSAGES == yes && $CLEANUP_THREADS == yes ]] || fails+=("cleanup")
  if (( ${#fails[@]} )); then VERDICT=FAIL; VERDICT_WHY="investigate: ${fails[*]}"; return; fi
  if (( ${#DEVIATIONS[@]} )); then VERDICT=INCONCLUSIVE; VERDICT_WHY="deviations recorded; the plan requires a fresh run"; return; fi
  VERDICT=PASS; VERDICT_WHY="all required cases, UI confirmations, audit checks, and cleanup confirmed"
}

write_report() {
  [[ -n $EVIDENCE_DIR && -d $EVIDENCE_DIR ]] || return 0
  (( REPORT_WRITTEN )) && return 0
  REPORT_WRITTEN=1
  compute_verdict
  local r=$EVIDENCE_DIR/report.md name
  {
    say "# Live test run $RUN_ID"
    say ""
    say "**Verdict: $VERDICT** — $VERDICT_WHY"
    say ""
    say "| Field | Value |"
    say "|---|---|"
    say "| Operator | $OPERATOR |"
    say "| Commit | $COMMIT |"
    say "| Working tree | $TREE_STATE |"
    say "| Start (UTC) | $START_UTC |"
    say "| End (UTC) | ${END_UTC:-unfinished} |"
    say "| Local gates | ${GATES_RESULT:-not run} |"
    say "| Build command | \`$BUILD_CMD\` |"
    say "| Binary SHA-256 | ${BIN_SHA256:-not built} |"
    say "| Binary version | ${BIN_VERSION:-unknown} |"
    say "| Bot application/user ID | ${BOT_ID:-unknown} |"
    say "| Guild ID | $GUILD_ID |"
    say "| Test channel ID | $TEST_CHANNEL_ID |"
    say "| Denied channel ID | ${DENIED_CHANNEL_ID:-n/a} |"
    say "| Operator user ID | ${OPERATOR_USER_ID:-n/a} |"
    say "| Message Content intent | ${MESSAGE_CONTENT:-unknown} |"
    say "| Seed message IDs | ${SEED_1_ID:-n/a}, ${SEED_2_ID:-n/a}, ${SEED_3_ID:-n/a} |"
    say "| Denied thread ID | ${DENIED_THREAD_ID:-n/a} |"
    say "| Audit log | $AUDIT_RESULT${AUDIT_NOTES:+ ($AUDIT_NOTES)} |"
    say "| Evidence secret scan | $SCAN_RESULT |"
    say "| Optional least-privilege checks | $OPTIONAL_RAN |"
    say "| Cleanup: messages deleted | $CLEANUP_MESSAGES |"
    say "| Cleanup: threads deleted | $CLEANUP_THREADS |"
    say "| Bot retained/removed | ${CLEANUP_BOT:-not recorded} |"
    say "| Credential | redacted (never captured) |"
    say ""
    say "## Result per step"
    say ""
    say "| Case | Result | Exit | Attempts | Notes |"
    say "|---|---|---|---|---|"
    for name in "${CASE_ORDER[@]}"; do
      say "| $name | ${RESULTS[$name]} | $(status_of "$name") | ${ATTEMPTS[$name]} | ${FAILURE_NOTES[$name]:-} |"
    done
    say ""
    say "## Discord UI confirmations"
    say ""
    if (( ${#UI_ORDER[@]} )); then
      for name in "${UI_ORDER[@]}"; do say "- ${UI_RESULTS[$name]}: ${UI_TEXT[$name]}"; done
    else say "- none recorded"; fi
    say ""
    say "## Created resources"
    say ""
    if (( ${#CREATED[@]} )); then for name in "${CREATED[@]}"; do say "- $name"; done; else say "- none"; fi
    say ""
    say "## Deviations"
    say ""
    if (( ${#DEVIATIONS[@]} )); then for name in "${DEVIATIONS[@]}"; do say "- $name"; done; else say "- none"; fi
    say ""
    say "## Command executions (case, attempt, command, exit)"
    say ""
    say '```'
    for name in "${EXEC_LOG[@]}"; do say "$name"; done
    say '```'
    say ""
    say "Captured JSON in this directory may contain message data and usernames; sanitize before sharing."
  } >"$r"
  chmod 600 "$r"
  {
    say "# non-secret IDs from run $RUN_ID, for manual follow-up with docs/live-testing.md run_case"
    say "CLI=$CLI"
    say "LIVE_CONFIG_ROOT=\"$LIVE_CONFIG_ROOT\""
    say "RUN_ID=$RUN_ID"
    say "EVIDENCE_DIR=$EVIDENCE_DIR"
    say "TEST_CHANNEL_ID=$TEST_CHANNEL_ID"
    say "DENIED_CHANNEL_ID=$DENIED_CHANNEL_ID"
    say "OPERATOR_USER_ID=$OPERATOR_USER_ID"
    say "BOT_ID=$BOT_ID"
    say "SEED_1_ID=$SEED_1_ID"
    say "SEED_2_ID=$SEED_2_ID"
    say "SEED_3_ID=$SEED_3_ID"
    say "POST_ID=$POST_ID"
    say "ATTACHMENT_POST_ID=$ATTACHMENT_POST_ID"
    say "MENTION_POST_ID=$MENTION_POST_ID"
    say "REPLY_ID=$REPLY_ID"
    say "THREAD_ID=$THREAD_ID"
    say "THREAD_MESSAGE_ID=$THREAD_MESSAGE_ID"
    say "THREAD_REPLY_ID=$THREAD_REPLY_ID"
    say "DENIED_THREAD_ID=$DENIED_THREAD_ID"
  } >"$EVIDENCE_DIR/run.env"
  chmod 600 "$EVIDENCE_DIR/run.env"
  note "report written to $r"
}

# ================================================================ PHASE 0 ====
preflight() {
  phase 0 "Preflight"
  local missing=() t
  for t in jq go gofmt git sha256sum stat; do command -v "$t" >/dev/null 2>&1 || missing+=("$t"); done
  (( ${#missing[@]} )) && { bad "missing tools: ${missing[*]}"; exit 69; }
  ok "tools present: jq $(jq --version), $(go version | cut -d' ' -f3)"

  COMMIT=$(git rev-parse HEAD)
  local dirty; dirty=$(git status --short)
  if [[ -z $dirty ]]; then TREE_STATE="clean"; ok "working tree clean at $COMMIT"
  else
    TREE_STATE="dirty ($(printf '%s\n' "$dirty" | wc -l) entries)"
    warn "working tree is not clean; the plan requires a clean checkout of the exact commit:"
    printf '%s\n' "$dirty" | sed 's/^/      /'
    if printf '%s\n' "$dirty" | grep -q -E 'token|config\.json|\.jsonl|\.local'; then
      bad "a credential, configuration, or log file is visible to git; fix .gitignore or remove it before running"; exit 70
    fi
    ask_yes_no "Continue anyway? (recorded as a deviation; the run cannot PASS)" || exit 1
    add_deviation "working tree not clean: $(printf '%s' "$dirty" | tr '\n' ' ')"
  fi

  ask_value OPERATOR "Operator name for the evidence record" "$(git config user.name 2>/dev/null || true)"

  header "Safety envelope (docs/live-testing.md)"
  say "  Every answer must be yes. This run must never touch a production guild, channel, or bot."
  ask_yes_no "The guild is a private, disposable test guild owned by you?" || exit 1
  ask_yes_no "The bot application is dedicated to this test and installed with the bot scope and least privilege only?" || exit 1
  ask_yes_no "Private channels cli-live-test and cli-not-allowed exist, visible only to you and the bot?" || exit 1
  ask_yes_no "You will run one command at a time and stop on any 429, 5xx, spill, notification, or duplicate?" || exit 1
  if ask_yes_no "Is the Message Content privileged intent ENABLED for this application?"; then MESSAGE_CONTENT=yes; else MESSAGE_CONTENT=no; fi
  ok "safety envelope acknowledged; Message Content intent: $MESSAGE_CONTENT"

  RUN_ID=$(date -u +live-%Y%m%dT%H%M%SZ)
  START_UTC=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  EVIDENCE_DIR=$LIVE_ROOT/evidence/$RUN_ID
  GATES_DIR=$EVIDENCE_DIR/gates
  mkdir -p "$GATES_DIR" && chmod 700 "$EVIDENCE_DIR"
  ok "run ID $RUN_ID; evidence in $EVIDENCE_DIR"
}

# ================================================================ PHASE 1 ====
run_gate() { # run_gate NAME CMD... — output to gates/NAME.log
  local name=$1; shift
  printf '  running %-16s ' "$name"
  if "$@" >"$GATES_DIR/$name.log" 2>&1; then printf '%sok%s\n' "$GREEN" "$RESET"; return 0; fi
  printf '%sFAILED%s (see %s)\n' "$RED" "$RESET" "$GATES_DIR/$name.log"; return 1
}

local_gates() {
  phase 1 "Local release gates"
  if (( SKIP_GATES )); then
    GATES_RESULT="skipped"; add_deviation "local gates skipped with --skip-gates"; return 0
  fi
  local failed=0
  run_gate go-test  go test ./...          || failed=1
  run_gate go-race  go test -race ./...    || failed=1
  run_gate go-vet   go vet ./...           || failed=1
  printf '  running %-16s ' gofmt
  gofmt -d cmd internal >"$GATES_DIR/gofmt.log" 2>&1
  if [[ -s $GATES_DIR/gofmt.log ]]; then printf '%sFAILED%s (gofmt -d printed output)\n' "$RED" "$RESET"; failed=1; else printf '%sok%s\n' "$GREEN" "$RESET"; fi
  if (( failed )); then GATES_RESULT="FAILED"; abort_run "local gates failed; fix the code before a live run"; fi
  GATES_RESULT="passed"
  ok "all four local gates passed"
}

# ================================================================ PHASE 2 ====
config_problems() { # prints one problem per line; empty output means the config is usable
  jq -e . "$CONFIG" >/dev/null 2>&1 || { say "config.json is not valid JSON"; return; }
  local g c
  g=$(jq -r '.guild_id // empty' "$CONFIG")
  is_snowflake "$g" || say "guild_id is missing or not a snowflake"
  [[ $g == 123456789012345678 ]] && say "guild_id still has the example placeholder"
  c=$(jq -r '.allowed_channel_ids | if type=="array" and length==1 then .[0] else empty end' "$CONFIG")
  is_snowflake "$c" || say "allowed_channel_ids must contain exactly the cli-live-test channel ID"
  [[ $c == 234567890123456789 ]] && say "allowed_channel_ids still has the example placeholder"
  jq -e 'has("allowed_thread_ids")' "$CONFIG" >/dev/null 2>&1 && say "allowed_thread_ids must be omitted at the start of a run"
  local p; p=$(jq -r '.log.path // empty' "$CONFIG")
  [[ $p == .local/* || $p == "$REPO_ROOT/.local/"* ]] || say "log.path must be set and stay below .local/"
  grep -q -E '^DISCORD_BOT_TOKEN=.+' "$TOKEN_ENV" || say "token.env has no DISCORD_BOT_TOKEN=... line"
  grep -q 'replace-with' "$TOKEN_ENV" && say "token.env still has the example placeholder"
  return 0
}

runtime_tree() {
  phase 2 "Runtime tree, configuration, and candidate build"
  mkdir -p "$CONFIG_DIR" "$FIXTURES"
  local copied=0
  if [[ -f $CONFIG && -f $TOKEN_ENV ]]; then
    ok "existing config.json and token.env found; examples not copied"
  else
    if [[ ! -f $CONFIG ]]; then cp examples/live-test/config.example.json "$CONFIG" && copied=1 && note "copied examples/live-test/config.example.json -> $CONFIG"; fi
    if [[ ! -f $TOKEN_ENV ]]; then cp examples/live-test/token.env.example "$TOKEN_ENV" && copied=1 && note "copied examples/live-test/token.env.example -> $TOKEN_ENV"; fi
  fi
  chmod 600 "$CONFIG" "$TOKEN_ENV"
  if (( copied )); then
    warn "edit the copied file(s) now, in another terminal:"
    say "      $CONFIG   -> disposable guild ID, only the cli-live-test channel ID, keep log.path below .local/"
    say "      $TOKEN_ENV -> the dedicated test bot token (never paste it here)"
  fi
  while :; do
    local problems; problems=$(config_problems)
    [[ -z $problems ]] && break
    bad "configuration is not ready:"; printf '%s\n' "$problems" | sed 's/^/      - /'
    pause "Fix the file(s) and press Enter to re-check"
  done
  chmod 600 "$CONFIG" "$TOKEN_ENV"
  GUILD_ID=$(jq -r '.guild_id' "$CONFIG")
  TEST_CHANNEL_ID=$(jq -r '.allowed_channel_ids[0]' "$CONFIG")
  AUDIT_PATH=$(jq -r '.log.path' "$CONFIG")
  cp "$CONFIG" "$EVIDENCE_DIR/config.snapshot.json" && chmod 600 "$EVIDENCE_DIR/config.snapshot.json"
  ok "config: guild $GUILD_ID, test channel $TEST_CHANNEL_ID, audit log $AUDIT_PATH (snapshot saved)"

  if git status --short | grep -q -E 'token|config\.json|\.jsonl|\.local'; then
    abort_run "git status shows a credential, configuration, or log file"
  fi

  say "  building: $BUILD_CMD"
  if ! CGO_ENABLED=0 go build -trimpath -o "$CLI" ./cmd/agent-cli-discord >"$GATES_DIR/build.log" 2>&1; then
    abort_run "build failed (see $GATES_DIR/build.log)"
  fi
  BIN_SHA256=$(sha256sum "$CLI" | cut -d' ' -f1)
  ok "built $CLI sha256 $BIN_SHA256"

  if [[ -s $AUDIT_PATH ]]; then
    local n; n=$(wc -l <"$AUDIT_PATH")
    warn "$AUDIT_PATH already has $n line(s) from earlier runs; the audit check needs a log holding only this run"
    ask_yes_no "Move it aside to $AUDIT_PATH.pre-$RUN_ID?" || abort_run "audit log from an earlier run is in the way"
    mv "$AUDIT_PATH" "$AUDIT_PATH.pre-$RUN_ID"; AUDIT_MOVED="$AUDIT_PATH.pre-$RUN_ID"
  fi

  header "Run identifiers (non-secret)"
  while :; do
    ask_snowflake DENIED_CHANNEL_ID "cli-not-allowed channel ID"
    [[ $DENIED_CHANNEL_ID != "$TEST_CHANNEL_ID" ]] && break
    warn "that is the allowed test channel; enter the cli-not-allowed channel"
  done
  ask_snowflake OPERATOR_USER_ID "Your operator (test account) user ID"

  printf '%s\n' "$RUN_ID plain-message" >"$FIXTURES/plain.txt"
  printf '%s\n' "$RUN_ID attachment-message" >"$FIXTURES/attachment-message.txt"
  printf '%s\n' "$RUN_ID harmless attachment" >"$FIXTURES/attachment.txt"
  printf '%s\n' "$RUN_ID literal-mention <@$OPERATOR_USER_ID>" >"$FIXTURES/mention.txt"
  printf '%s\n' "$RUN_ID reply" >"$FIXTURES/reply.txt"
  ATTACH_SIZE=$(stat -c %s "$FIXTURES/attachment.txt")
  ok "fixtures written under $FIXTURES (attachment $ATTACH_SIZE bytes)"
}

# ================================================================ PHASE 3 ====
case_01() { run_case 01-version "$CLI" version; expect_ok 01-version
  check 01-version '.data.name == "agent-cli-discord" and .data.schema_version == "1" and (.data.version|type) == "string"' "name/schema_version/version are not as expected"
  BIN_VERSION=$(jq -r '.data.version // "?"' "$EVIDENCE_DIR/01-version.stdout.json"); note "executable version: $BIN_VERSION, schema 1"; }
case_02() { run_case 02-auth-check "$CLI" auth check; expect_ok 02-auth-check
  check 02-auth-check '.data.bot == true and (.data.id|test("^[0-9]{17,20}$")) and (.data.username|type) == "string"' "identity is not a bot user with a snowflake ID"; }
case_03() { run_case 03-channels-list "$CLI" channels list; expect_ok 03-channels-list
  check 03-channels-list '(.data|length) == 1 and .data[0].id == $test and .data[0].guild_id == $guild and .data[0].type == 0' "channel list is not exactly the test channel in the configured guild"; }
case_04() { run_case 04-threads-list-initial "$CLI" threads list; expect_ok 04-threads-list-initial
  check 04-threads-list-initial 'all(.data[]; .guild_id == $guild and .parent_id == $test)' "a listed thread is outside the configured guild/parent"
  local n; n=$(jq -r '.data|length' "$EVIDENCE_DIR/04-threads-list-initial.stdout.json" 2>/dev/null)
  [[ $n == 0 ]] || warn "$n active thread(s) already exist; the plan prefers starting with none"; }

identity_phase() {
  phase 3 "Identity and visibility (no mutations)"
  step 01-version case_01
  step 02-auth-check case_02
  extract_id BOT_ID 02-auth-check '.data.id' "bot user"
  step 03-channels-list case_03
  step 04-threads-list-initial case_04
}

# ================================================================ PHASE 4 ====
case_10() { run_case 10-read-limit "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 2; expect_ok 10-read-limit
  check 10-read-limit "$ORDERED" "messages are not oldest-to-newest"
  check 10-read-limit "$IN_TEST" "a message has the wrong channel_id"
  check 10-read-limit "$SHAPE" "a message is missing a required field"
  check 10-read-limit '[.data.messages[].id] == [$s2, $s3]' "page is not exactly [seed-2, seed-3]"
  check 10-read-limit '.data.cursor.before == $s2' "cursor.before is not seed-2"
  check 10-read-limit "$SEED_AUTHOR" "a seed message is not authored by the operator"
  check 10-read-limit "$(seed_content_filter)" "seed content is not as expected for Message Content=$MESSAGE_CONTENT"; }
case_11() { run_case 11-read-before "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 2 --before "$READ_BEFORE"; expect_ok 11-read-before
  check 11-read-before "$ORDERED" "messages are not oldest-to-newest"
  check 11-read-before "$IN_TEST" "a message has the wrong channel_id"
  check 11-read-before '(.data.messages|length) >= 1 and (.data.messages|length) <= 2 and .data.messages[-1].id == $s1' "newest message on the page is not seed-1"
  check 11-read-before 'all(.data.messages[].id; [length, .] < [($s2|length), $s2])' "a message is not older than seed-2"
  check 11-read-before '.data.cursor.before == .data.messages[0].id' "cursor.before is not the oldest message on the page"; }
case_12() { run_case 12-read-after "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 2 --after "$SEED_1_ID"; expect_ok 12-read-after
  check 12-read-after "$ORDERED" "messages are not oldest-to-newest"
  check 12-read-after "$IN_TEST" "a message has the wrong channel_id"
  check 12-read-after '[.data.messages[].id] == [$s2, $s3]' "page after seed-1 is not exactly [seed-2, seed-3]"
  check 12-read-after '.data.cursor.after == $s3' "cursor.after is not seed-3"; }
case_13() { run_case 13-read-around "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 3 --around "$SEED_2_ID"; expect_ok 13-read-around
  check 13-read-around "$ORDERED" "messages are not oldest-to-newest"
  check 13-read-around "$IN_TEST" "a message has the wrong channel_id"
  check 13-read-around '[.data.messages[].id] == [$s1, $s2, $s3]' "page around seed-2 is not exactly [seed-1, seed-2, seed-3]"
  check 13-read-around '(.data|has("cursor")|not)' "an around page must not return a cursor"
  check 13-read-around "$(seed_content_filter)" "seed content is not as expected for Message Content=$MESSAGE_CONTENT"; }
case_14() { run_case 14-read-empty "$CLI" messages read --channel "$TEST_CHANNEL_ID" --after "$SEED_3_ID"; expect_ok 14-read-empty
  check 14-read-empty '.data.messages == [] and (.data|has("cursor")|not)' "page after seed-3 is not an empty page without a cursor"; }
case_15() { run_case 15-get-seed "$CLI" messages get --channel "$TEST_CHANNEL_ID" --message "$SEED_2_ID"; expect_ok 15-get-seed
  check 15-get-seed '.data.id == $s2 and .data.channel_id == $test and .data.author.id == $op' "get did not return seed-2 by the operator"
  check_pair 15-get-seed 13-read-around '.[0].data as $m | any(.[1].data.messages[]; . == $m)' "get result differs from the same message in the around page"; }

reads_phase() {
  phase 4 "Message reads and pagination (no mutations)"
  ui "Post these three messages, in order, from the operator account in cli-live-test:"
  say "      $RUN_ID seed-message-1"
  say "      $RUN_ID seed-message-2"
  say "      $RUN_ID seed-message-3"
  say "  Then, with Developer Mode on, use Copy Message ID for each."
  while :; do
    ask_snowflake SEED_1_ID "seed-message-1 ID"
    ask_snowflake SEED_2_ID "seed-message-2 ID"
    ask_snowflake SEED_3_ID "seed-message-3 ID"
    if jq -n -e --arg a "$SEED_1_ID" --arg b "$SEED_2_ID" --arg c "$SEED_3_ID" '[$a,$b,$c] as $x | ($x|unique|length) == 3 and $x == ($x|sort_by([length, .]))' >/dev/null; then break; fi
    warn "the three IDs must be distinct and ascending (seed-1 oldest); re-enter them"
  done
  step 10-read-limit case_10
  READ_BEFORE=$(jq -r '.data.cursor.before // empty' "$EVIDENCE_DIR/10-read-limit.stdout.json")
  is_snowflake "$READ_BEFORE" || abort_run "cannot continue: 10-read-limit produced no cursor.before"
  step 11-read-before case_11
  step 12-read-after case_12
  step 13-read-around case_13
  step 14-read-empty case_14
  step 15-get-seed case_15
}

# ================================================================ PHASE 5 ====
case_20() { run_case 20-denied-post "$CLI" messages post --channel "$DENIED_CHANNEL_ID" --file "$FIXTURES/plain.txt"
  expect_error 20-denied-post policy.channel_not_authorized; check_no_leak 20-denied-post; }

denied_phase() {
  phase 5 "Local authorization boundary (no mutation expected)"
  step 20-denied-post case_20
  ui_confirm 20-no-spill "No message appeared in cli-not-allowed." || true
}

# ================================================================ PHASE 6 ====
MSG_BOT='.data.author.id == $bot and .data.author.bot == true and (.data.attachments|type) == "array"'
case_30() { run_case 30-post "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file "$FIXTURES/plain.txt"; expect_ok 30-post
  check 30-post ".data.channel_id == \$test and $MSG_BOT and .data.content == (\$run + \" plain-message\") and .data.attachments == [] and .data.type == 0" "posted message is not the bot's plain message in the test channel"; }
case_31() { run_case 31-post-attachment "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file "$FIXTURES/attachment-message.txt" --attach "$FIXTURES/attachment.txt"; expect_ok 31-post-attachment
  check 31-post-attachment ".data.channel_id == \$test and $MSG_BOT and .data.content == (\$run + \" attachment-message\")" "attachment message content/author is wrong"
  check 31-post-attachment '(.data.attachments|length) == 1 and .data.attachments[0].filename == "attachment.txt" and .data.attachments[0].size == $attach_size and (.data.attachments[0].url|startswith("https://"))' "attachment metadata (one file, attachment.txt, $ATTACH_SIZE bytes, https url) is wrong"; }
case_32() { run_case 32-post-mention "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file "$FIXTURES/mention.txt"; expect_ok 32-post-mention
  check 32-post-mention ".data.channel_id == \$test and $MSG_BOT and .data.content == (\$run + \" literal-mention <@\" + \$op + \">\")" "mention message content is not the literal mention text"; }
case_33() { run_case 33-reply "$CLI" messages reply --channel "$TEST_CHANNEL_ID" --message "$SEED_2_ID" --file "$FIXTURES/reply.txt"; expect_ok 33-reply
  check 33-reply ".data.channel_id == \$test and $MSG_BOT and .data.content == (\$run + \" reply\") and .data.type == 19" "reply is not a bot reply (type 19) with the reply text"
  check 33-reply '.data.message_reference.message_id == $s2 and .data.referenced_message.id == $s2 and .data.referenced_message.author.id == $op' "reply does not reference seed-2"; }
case_34() { run_case 34-reaction-add "$CLI" reactions add --channel "$TEST_CHANNEL_ID" --message "$POST_ID" --emoji '✅'; expect_ok 34-reaction-add
  check 34-reaction-add '.data == {channel_id: $test, message_id: $post, emoji: "✅", action: "add"}' "reaction add result is wrong"; }
case_35() { run_case 35-reaction-remove "$CLI" reactions remove --channel "$TEST_CHANNEL_ID" --message "$POST_ID" --emoji '✅'; expect_ok 35-reaction-remove
  check 35-reaction-remove '.data == {channel_id: $test, message_id: $post, emoji: "✅", action: "remove"}' "reaction remove result is wrong"; }
case_36() { run_case 36-get-post "$CLI" messages get --channel "$TEST_CHANNEL_ID" --message "$POST_ID"; expect_ok 36-get-post
  check 36-get-post '.data.id == $post' "get did not return the posted message"
  check_pair 36-get-post 30-post '.[0].data == .[1].data' "get result differs from the post response"; }
case_37() { run_case 37-read-created "$CLI" messages read --channel "$TEST_CHANNEL_ID" --around "$POST_ID"; expect_ok 37-read-created
  check 37-read-created "$ORDERED" "messages are not oldest-to-newest"
  check 37-read-created "$IN_TEST" "a message has the wrong channel_id"
  check 37-read-created '[.data.messages[].id] as $i | all([$post, $apost, $mpost, $reply][]; IN($i[]))' "page around the post does not contain all four created messages"
  check_pair 37-read-created 30-post '.[1].data as $m | any(.[0].data.messages[]; . == $m)' "the posted message in the page differs from the post response"; }

messages_phase() {
  phase 6 "Messages and reactions (controlled mutations)"
  warn "each mutation runs exactly once; confirm each change in Discord before the next"
  step 30-post case_30
  extract_id POST_ID 30-post '.data.id' "plain post"; add_created "message $POST_ID (plain post, test channel)"
  ui_confirm 30-one-message "Exactly one new bot message '$RUN_ID plain-message' appeared in cli-live-test." || true
  step 31-post-attachment case_31
  extract_id ATTACHMENT_POST_ID 31-post-attachment '.data.id' "attachment post"; add_created "message $ATTACHMENT_POST_ID (attachment post, test channel)"
  ui_confirm 31-attachment "Exactly one new message with attachment.txt ($ATTACH_SIZE bytes, content '$RUN_ID harmless attachment') appeared." || true
  step 32-post-mention case_32
  extract_id MENTION_POST_ID 32-post-mention '.data.id' "mention post"; add_created "message $MENTION_POST_ID (literal mention post, test channel)"
  ui_confirm 32-no-notify "The literal mention message appeared and did NOT notify/ping the operator account." || true
  step 33-reply case_33
  extract_id REPLY_ID 33-reply '.data.id' "reply"; add_created "message $REPLY_ID (reply to seed-2, test channel)"
  ui_confirm 33-reply "The reply references seed-message-2 and did NOT notify the operator." || true
  step 34-reaction-add case_34
  ui_confirm 34-reaction-add "Only the bot's ✅ reaction appears on the plain post." || true
  step 35-reaction-remove case_35
  ui_confirm 35-reaction-remove "The ✅ reaction disappeared from the plain post." || true
  step 36-get-post case_36
  step 37-read-created case_37
}

# ================================================================ PHASE 7 ====
THREAD_SHAPE='.guild_id == $guild and .parent_id == $test and .type == 11 and .name == ($run + " cli-thread") and .thread_metadata.auto_archive_duration == 60 and .thread_metadata.archived == false and .thread_metadata.locked == false'
case_40() { run_case 40-thread-create "$CLI" threads create --channel "$TEST_CHANNEL_ID" --name "$RUN_ID cli-thread" --auto-archive 60; expect_ok 40-thread-create
  check 40-thread-create ".data | $THREAD_SHAPE" "created thread is not type 11 '$RUN_ID cli-thread' under the test channel with 60-minute archive"; }
case_41() { run_case 41-threads-list "$CLI" threads list; expect_ok 41-threads-list
  check 41-threads-list "any(.data[]; .id == \$thread and $THREAD_SHAPE)" "thread list does not contain the created thread with matching fields"
  check 41-threads-list 'all(.data[]; .guild_id == $guild and .parent_id == $test)' "a listed thread is outside the configured guild/parent"; }
case_42() { run_case 42-thread-post "$CLI" messages post --channel "$THREAD_ID" --file "$FIXTURES/plain.txt"; expect_ok 42-thread-post
  check 42-thread-post ".data.channel_id == \$thread and $MSG_BOT and .data.content == (\$run + \" plain-message\")" "thread post is not the bot's plain message in the thread"; }
case_43() { run_case 43-thread-read "$CLI" messages read --channel "$THREAD_ID" --around "$THREAD_MESSAGE_ID"; expect_ok 43-thread-read
  check 43-thread-read "$ORDERED" "messages are not oldest-to-newest"
  check 43-thread-read "$IN_THREAD" "a message has a channel_id other than the thread"
  check_pair 43-thread-read 42-thread-post '.[1].data as $m | any(.[0].data.messages[]; . == $m)' "thread page does not contain the thread post as returned by post"; }
case_44() { run_case 44-thread-get "$CLI" messages get --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID"; expect_ok 44-thread-get
  check_pair 44-thread-get 42-thread-post '.[0].data == .[1].data' "get result differs from the thread post response"; }
case_45() { run_case 45-thread-reply "$CLI" messages reply --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID" --file "$FIXTURES/reply.txt"; expect_ok 45-thread-reply
  check 45-thread-reply ".data.channel_id == \$thread and $MSG_BOT and .data.content == (\$run + \" reply\") and .data.type == 19 and .data.message_reference.message_id == \$tmsg and .data.referenced_message.id == \$tmsg" "thread reply does not reference the thread post"; }
case_46() { run_case 46-thread-reaction-add "$CLI" reactions add --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID" --emoji '✅'; expect_ok 46-thread-reaction-add
  check 46-thread-reaction-add '.data == {channel_id: $thread, message_id: $tmsg, emoji: "✅", action: "add"}' "thread reaction add result is wrong"; }
case_47() { run_case 47-thread-reaction-remove "$CLI" reactions remove --channel "$THREAD_ID" --message "$THREAD_MESSAGE_ID" --emoji '✅'; expect_ok 47-thread-reaction-remove
  check 47-thread-reaction-remove '.data == {channel_id: $thread, message_id: $tmsg, emoji: "✅", action: "remove"}' "thread reaction remove result is wrong"; }
case_48() { run_case 48-thread-leave "$CLI" threads leave --thread "$THREAD_ID"; expect_ok 48-thread-leave
  check 48-thread-leave '.data == {thread_id: $thread, action: "leave"}' "leave result is wrong"; }
case_49() { run_case 49-thread-join "$CLI" threads join --thread "$THREAD_ID"; expect_ok 49-thread-join
  check 49-thread-join '.data == {thread_id: $thread, action: "join"}' "join result is wrong"; }
case_50() { run_case 50-allowed-thread-read "$CLI" messages read --channel "$THREAD_ID" --limit 1; expect_ok 50-allowed-thread-read
  check 50-allowed-thread-read '(.data.messages|length) == 1 and .data.messages[0].channel_id == $thread and .data.cursor.before == .data.messages[0].id' "allowed-thread read did not return one thread message with a cursor"; }
case_51() { run_case 51-restricted-create "$CLI" threads create --channel "$TEST_CHANNEL_ID" --name "$RUN_ID must-not-exist"
  expect_error 51-restricted-create policy.thread_creation_restricted
  if [[ $(status_of 51-restricted-create) == 0 ]]; then
    local id; id=$(jq -r '.data.id // "unknown"' "$EVIDENCE_DIR/51-restricted-create.stdout.json" 2>/dev/null)
    add_created "thread $id (UNEXPECTED: '$RUN_ID must-not-exist' was created)"
  fi; }
case_52() { run_case 52-denied-thread-read "$CLI" messages read --channel "$DENIED_THREAD_ID" --limit 1
  expect_error 52-denied-thread-read policy.channel_not_authorized; check_no_leak 52-denied-thread-read
  check_err 52-denied-thread-read '(.error|keys) - ["code","message","retryable"] == []' "error object carries unexpected fields"; }

set_allowed_thread() {
  jq --arg t "$THREAD_ID" '. + {allowed_thread_ids: [$t]}' "$CONFIG" >"$CONFIG.tmp" && chmod 600 "$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG" \
    || abort_run "could not add allowed_thread_ids to config.json"
  CONFIG_MODIFIED=1
  ok "config.json now has allowed_thread_ids: [$THREAD_ID]"
}

threads_phase() {
  phase 7 "Thread lifecycle (controlled mutations)"
  step 40-thread-create case_40
  extract_id THREAD_ID 40-thread-create '.data.id' "thread"; add_created "thread $THREAD_ID ('$RUN_ID cli-thread' under test channel)"
  ui_confirm 40-thread "Exactly one new public thread '$RUN_ID cli-thread' exists under cli-live-test." || true
  step 41-threads-list case_41
  step 42-thread-post case_42
  extract_id THREAD_MESSAGE_ID 42-thread-post '.data.id' "thread message"; add_created "message $THREAD_MESSAGE_ID (plain post in thread)"
  ui_confirm 42-thread-post "Exactly one bot message '$RUN_ID plain-message' appeared in the thread." || true
  step 43-thread-read case_43
  step 44-thread-get case_44
  step 45-thread-reply case_45
  extract_id THREAD_REPLY_ID 45-thread-reply '.data.id' "thread reply"; add_created "message $THREAD_REPLY_ID (reply in thread)"
  ui_confirm 45-thread-reply "The thread reply references the bot's thread message and notified nobody." || true
  step 46-thread-reaction-add case_46
  ui_confirm 46-thread-reaction-add "Only the bot's ✅ reaction appears on the thread message." || true
  step 47-thread-reaction-remove case_47
  ui_confirm 47-thread-reaction-remove "The ✅ reaction disappeared from the thread message." || true
  step 48-thread-leave case_48
  ui_confirm 48-thread-leave "The bot is no longer a member of the thread (check the thread member list)." || true
  step 49-thread-join case_49
  ui_confirm 49-thread-join "The bot is a member of the thread again." || true

  header "Explicit thread allowlist"
  set_allowed_thread
  step 50-allowed-thread-read case_50
  step 51-restricted-create case_51
  ui_confirm 51-no-thread "No thread named '$RUN_ID must-not-exist' was created." || true

  ui "Manually create a second public thread under cli-live-test named '$RUN_ID denied-thread', then copy its ID."
  while :; do
    ask_snowflake DENIED_THREAD_ID "manually created (denied) thread ID"
    [[ $DENIED_THREAD_ID != "$THREAD_ID" && $DENIED_THREAD_ID != "$TEST_CHANNEL_ID" ]] && break
    warn "that ID is the CLI-created thread or the channel; enter the manually created thread's ID"
  done
  add_created "thread $DENIED_THREAD_ID (manually created denied thread)"
  step 52-denied-thread-read case_52
  restore_config
}

# ================================================================ PHASE 8 ====
audit_phase() {
  phase 8 "Audit evidence"
  local problems=() lines expected actual cmd st
  if [[ ! -f $AUDIT_PATH ]]; then
    AUDIT_RESULT=FAIL; AUDIT_NOTES="audit log $AUDIT_PATH does not exist"; bad "$AUDIT_NOTES"; return 1
  fi
  finish_audit_evidence
  local mode; mode=$(stat -c %a "$AUDIT_PATH")
  [[ $mode == 600 ]] || problems+=("file mode is $mode, expected 600")
  jq -c . "$AUDIT_PATH" >/dev/null 2>&1 || problems+=("a line is not valid JSON")
  jqa -s 'all(.[]; ((keys - ["schema_version","timestamp","level","event","command","outcome","guild_id","channel_id","message_id","thread_id"]) == [])
      and .schema_version == "1" and .event == "command.completed" and .level == "info" and .guild_id == $guild
      and (.outcome == "success" or .outcome == "failure") and (.timestamp|test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T.*Z$")))' "$AUDIT_PATH" \
    || problems+=("an event has unexpected keys or values (schema, event, level, guild, outcome, or UTC timestamp)")
  if grep -q -E -e "$RUN_ID" -e 'Authorization' -e '✅' -e '\.txt' -e 'cli-thread' -e 'must-not-exist' -e 'DISCORD_BOT_TOKEN' -e "$TOKEN_SHAPE" "$AUDIT_PATH"; then
    problems+=("log contains prohibited data (run ID/message text, thread name, emoji, attachment path, or token-shaped value)")
  fi
  expected=$EVIDENCE_DIR/audit-expected.tsv; actual=$EVIDENCE_DIR/audit-actual.tsv
  { local e; for e in "${EXEC_LOG[@]}"; do
      IFS=$'\t' read -r _ _ cmd st <<<"$e"
      [[ $cmd == version ]] && continue
      if [[ $st == 0 ]]; then printf '%s\tsuccess\n' "$cmd"; else printf '%s\tfailure\n' "$cmd"; fi
    done; } >"$expected"
  jq -r '[.command, .outcome] | @tsv' "$AUDIT_PATH" >"$actual" 2>/dev/null
  if ! diff -u "$expected" "$actual" >"$EVIDENCE_DIR/audit-sequence.diff" 2>&1; then
    problems+=("event sequence differs from the executed Discord commands (see audit-sequence.diff)")
  else rm -f "$EVIDENCE_DIR/audit-sequence.diff"; fi
  lines=$(wc -l <"$AUDIT_PATH")
  if (( ${#problems[@]} )); then
    AUDIT_RESULT=FAIL; AUDIT_NOTES=$(IFS='; '; printf '%s' "${problems[*]}")
    bad "audit log FAILED ($lines events):"; local p; for p in "${problems[@]}"; do printf '      - %s\n' "$p"; done
    return 1
  fi
  AUDIT_RESULT=PASS; AUDIT_NOTES="$lines events, one per Discord command, mode 600, no prohibited data"
  ok "audit log passed: $AUDIT_NOTES"
}

# ================================================================ PHASE 9 ====
case_60() { run_case 60-no-send-permission "$CLI" messages post --channel "$TEST_CHANNEL_ID" --file "$FIXTURES/plain.txt"
  CASE_CHECKS=$((CASE_CHECKS + 3)); local s; s=$(status_of 60-no-send-permission)
  [[ $s == 2 ]] || add_failure "exit status $s, expected 2"
  [[ ! -s $EVIDENCE_DIR/60-no-send-permission.stdout.json ]] || add_failure "a message was created (stdout not empty)"
  jqa '.ok == false and .error.http_status == 403' "$EVIDENCE_DIR/60-no-send-permission.stderr.json" || add_failure "not a structured 403 failure"; }
case_61() { run_case 61-no-history-permission "$CLI" messages read --channel "$TEST_CHANNEL_ID" --limit 1
  CASE_CHECKS=$((CASE_CHECKS + 3)); local s; s=$(status_of 61-no-history-permission)
  [[ $s == 2 ]] || add_failure "exit status $s, expected 2"
  [[ ! -s $EVIDENCE_DIR/61-no-history-permission.stdout.json ]] || add_failure "stdout is not empty"
  jqa '.ok == false and .error.http_status == 403' "$EVIDENCE_DIR/61-no-history-permission.stderr.json" || add_failure "not a structured 403 failure"; }

optional_phase() {
  phase 9 "Optional least-privilege checks"
  say "  These validate the guild configuration, not CLI error codes. They run only in the disposable guild."
  if ! ask_yes_no "Run the optional permission-revocation checks (60, 61)?"; then OPTIONAL_RAN=no; return 0; fi
  OPTIONAL_RAN=yes
  ui "Remove Send Messages for the bot in cli-live-test (channel permission override) and wait until it is visible."
  pause
  step 60-no-send-permission case_60
  ui_confirm 60-no-message "No message was created in cli-live-test." || true
  ui "Restore Send Messages, then remove Read Message History for the bot in cli-live-test and wait until visible."
  pause
  step 61-no-history-permission case_61
  ui "Restore Read Message History for the bot."
  pause
  ui_confirm 61-restored "Both permissions are restored to the pre-test state." || true
  # the audit log grew by two events; re-run its checks so the evidence copy and verdict include them
  audit_phase || true
}

# =============================================================== PHASE 10 ====
scan_evidence() {
  local hits; hits=$(grep -r -l -E -e "$TOKEN_SHAPE" -e 'Authorization' -e 'DISCORD_BOT_TOKEN=' "$EVIDENCE_DIR" 2>/dev/null || true)
  if [[ -n $hits ]]; then SCAN_RESULT=FAIL; bad "token-shaped or authorization data found in evidence:"; printf '%s\n' "$hits" | sed 's/^/      /'
  else SCAN_RESULT=PASS; ok "evidence directory contains no token-shaped or authorization data"; fi
}

cleanup_phase() {
  phase 10 "Cleanup and acceptance"
  say "  Created resources to delete in the Discord UI (deletion is outside the CLI surface):"
  local c; for c in "${CREATED[@]}"; do say "      - $c"; done
  if ask_yes_no "All CLI-created messages (including thread messages) have been deleted?"; then CLEANUP_MESSAGES=yes; else CLEANUP_MESSAGES=no; fi
  if ask_yes_no "Both threads ('$RUN_ID cli-thread' and the manually created denied thread) have been deleted?"; then CLEANUP_THREADS=yes; else CLEANUP_THREADS=no; fi
  if ask_yes_no "Will the bot application be retained exclusively for future isolated tests?"; then CLEANUP_BOT="retained for isolated tests"
  else CLEANUP_BOT="operator to remove bot from guild and reset token"; warn "remove the bot from the guild and reset its token in the Developer Portal"; fi
  scan_evidence
  END_UTC=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  write_report
  header "Verdict"
  case $VERDICT in
    PASS) say "  ${GREEN}${BOLD}PASS${RESET} — $VERDICT_WHY" ;;
    FAIL) say "  ${RED}${BOLD}FAIL${RESET} — $VERDICT_WHY"
          for c in "${CASE_ORDER[@]}"; do [[ ${RESULTS[$c]} == FAIL ]] && say "      $c: ${FAILURE_NOTES[$c]}"; done
          for c in "${UI_ORDER[@]}"; do [[ ${UI_RESULTS[$c]} == no ]] && say "      ui:$c: ${UI_TEXT[$c]}"; done
          [[ $AUDIT_RESULT == PASS ]] || say "      audit: $AUDIT_NOTES" ;;
    *)    say "  ${YELLOW}${BOLD}INCONCLUSIVE${RESET} — $VERDICT_WHY"
          for c in "${DEVIATIONS[@]}"; do say "      - $c"; done ;;
  esac
  say ""
  say "  Evidence: $EVIDENCE_DIR (report.md, run.env, audit.jsonl, per-case stdout/stderr/status)"
  [[ -n $AUDIT_MOVED ]] && note "earlier audit log kept at $AUDIT_MOVED"
  note "sanitize message data and IDs before sharing; remove .local/live-test only after evidence is recorded"
}

# ==================================================================== main ===
header "agent-cli-discord live test — guided run of docs/live-testing.md"
preflight
local_gates
runtime_tree
identity_phase
reads_phase
denied_phase
messages_phase
threads_phase
audit_phase || true
optional_phase
cleanup_phase
[[ $VERDICT == PASS ]]
