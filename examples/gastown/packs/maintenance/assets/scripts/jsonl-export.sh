#!/usr/bin/env bash
# jsonl-export — export Dolt databases to JSONL and push to git archive.
#
# Replaces mol-dog-jsonl formula. All operations are deterministic:
# dolt sql exports, jq record-count comparisons against spike threshold,
# git add/commit/push. No LLM judgment needed.
#
# Runs as an exec order (no LLM, no agent, no wisp).
set -euo pipefail

CITY="${GC_CITY:-.}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/dolt-target.sh"

# jq is a hard dependency: count_jsonl_rows below relies on it, and a missing
# jq would silently zero every record count and could mask spikes on a stale
# baseline. Fail loud at startup instead.
if ! command -v jq >/dev/null 2>&1; then
    echo "jsonl-export: jq is required but not found in PATH" >&2
    exit 1
fi
PACK_STATE_DIR="${GC_PACK_STATE_DIR:-${GC_CITY_RUNTIME_DIR:-$CITY/.gc/runtime}/packs/maintenance}"
LEGACY_ARCHIVE_REPO="$CITY/.gc/jsonl-archive"
LEGACY_STATE_FILE="$CITY/.gc/jsonl-export-state.json"

# Configurable via environment (defaults match the old formula).
SPIKE_THRESHOLD="${GC_JSONL_SPIKE_THRESHOLD:-20}"  # percentage (0-100)
# Skip the percentage spike check when the previous record count is below
# this absolute floor — small-N percentages are noise. Set to 0 to disable.
MIN_PREV_FOR_SPIKE_CHECK="${GC_JSONL_MIN_PREV_FOR_SPIKE:-10}"
MAX_PUSH_FAILURES="${GC_JSONL_MAX_PUSH_FAILURES:-3}"
SCRUB="${GC_JSONL_SCRUB:-true}"
ARCHIVE_REPO="${GC_JSONL_ARCHIVE_REPO:-$PACK_STATE_DIR/jsonl-archive}"

# Count records in a `dolt sql -r json` payload. The output is `{"rows":[...]}`
# on (typically) a single physical line, so `wc -l` measures formatting, not
# records. Falls back to 0 on empty/missing/unparseable input; jq parse errors
# are forwarded to stderr so a corrupt archive surfaces in operator logs
# instead of being silently scored as zero rows.
count_jsonl_rows() {
    jq -s -r 'if length == 0 then 0 else ((.[0].rows // []) | length) end' || echo "0"
}

# Scrub test-only rows while preserving the JSON export structure and legitimate
# rows in the same payload. The input is one JSON object with a .rows array, not
# newline-delimited JSON, so row-level filtering must happen inside jq.
scrub_exported_issues() {
    jq -c '
        if (.rows? | type) == "array" then
            .rows |= map(
                select(
                    ((.title // "") | test("^(Test Issue|test_)") | not) and
                    (
                        (
                            (.id // "") == "bd-1" or
                            (.id // "") == "bd-abc12" or
                            ((.id // "") | test("^(testdb_|beads_t)"))
                        ) | not
                    )
                )
            )
        else
            .
        end
    '
}

validate_exported_issues() {
    jq -c '.'
}

read_state_json() {
    if [ -f "$STATE_FILE" ]; then
        if jq -c '.' "$STATE_FILE" 2>/dev/null; then
            return
        fi
        echo "jsonl-export: state file malformed; resetting to empty state" >&2
    fi
    echo '{}'
}

write_state_json() {
    local tmpfile

    tmpfile=$(mktemp "${STATE_FILE}.tmp.XXXXXX")
    if ! printf '%s\n' "$1" > "$tmpfile"; then
        rm -f "$tmpfile"
        return 1
    fi
    if ! mv -f "$tmpfile" "$STATE_FILE"; then
        rm -f "$tmpfile"
        return 1
    fi
}

set_consecutive_push_failures() {
    local count="$1"
    write_state_json "$(read_state_json | jq -c --argjson count "$count" '.consecutive_push_failures = $count')"
}

set_pending_spike_alert() {
    local db="$1"
    local prev_count="$2"
    local current_count="$3"
    local delta="$4"
    local threshold="$5"

    write_state_json "$(
        read_state_json | jq -c \
            --arg db "$db" \
            --argjson prev_count "$prev_count" \
            --argjson current_count "$current_count" \
            --argjson delta "$delta" \
            --argjson threshold "$threshold" \
            '.pending_spike_alert = {
                database: $db,
                prev_count: $prev_count,
                current_count: $current_count,
                delta: $delta,
                threshold: $threshold
            }'
    )"
}

clear_pending_spike_alert() {
    write_state_json "$(read_state_json | jq -c 'del(.pending_spike_alert)')"
}

send_spike_alert() {
    local db="$1"
    local prev_count="$2"
    local current_count="$3"
    local delta="$4"
    local threshold="$5"

    gc mail send mayor/ -s "ESCALATION: JSONL spike detected [HIGH]" \
        -m "Database: $db, prev: $prev_count, current: $current_count, delta: ${delta}%, threshold: ${threshold}%" \
        2>/dev/null
}

retry_pending_spike_alert() {
    local state_json
    local db
    local prev_count
    local current_count
    local delta
    local threshold

    state_json=$(read_state_json)
    db=$(printf '%s\n' "$state_json" | jq -r '.pending_spike_alert.database // empty')
    if [ -z "$db" ]; then
        return
    fi
    prev_count=$(printf '%s\n' "$state_json" | jq -r '.pending_spike_alert.prev_count // 0')
    current_count=$(printf '%s\n' "$state_json" | jq -r '.pending_spike_alert.current_count // 0')
    delta=$(printf '%s\n' "$state_json" | jq -r '.pending_spike_alert.delta // 0')
    threshold=$(printf '%s\n' "$state_json" | jq -r '.pending_spike_alert.threshold // 0')

    if send_spike_alert "$db" "$prev_count" "$current_count" "$delta" "$threshold"; then
        clear_pending_spike_alert
        return
    fi
    echo "jsonl-export: pending spike alert delivery failed" >&2
}

commit_archive_snapshot() {
    local message="$1"
    local context="$2"

    if ! GIT_AUTHOR_NAME="Gas Town Daemon" \
        GIT_AUTHOR_EMAIL="daemon@gastown.local" \
        GIT_COMMITTER_NAME="Gas Town Daemon" \
        GIT_COMMITTER_EMAIL="daemon@gastown.local" \
        git commit -q -m "$message"; then
        echo "jsonl-export: $context commit failed" >&2
        return 1
    fi
}

discard_failed_db_outputs() {
    local db="$1"

    rm -rf "$ARCHIVE_REPO/$db"
    rm -f "$ARCHIVE_REPO/$db.jsonl"

    if git -C "$ARCHIVE_REPO" cat-file -e "HEAD:$db/issues.jsonl" 2>/dev/null; then
        git -C "$ARCHIVE_REPO" restore --source=HEAD --worktree -- "$db" >/dev/null 2>&1 || true
    fi
    if git -C "$ARCHIVE_REPO" cat-file -e "HEAD:$db.jsonl" 2>/dev/null; then
        git -C "$ARCHIVE_REPO" restore --source=HEAD --worktree -- "$db.jsonl" >/dev/null 2>&1 || true
    fi
}

discard_staged_archive_outputs() {
    local path

    if [ "${#STAGE_PATHS[@]}" -eq 0 ]; then
        return
    fi

    git reset -q -- "${STAGE_PATHS[@]}" >/dev/null 2>&1 || true
    for path in "${STAGE_PATHS[@]}"; do
        if git cat-file -e "HEAD:$path" 2>/dev/null; then
            git restore --source=HEAD --staged --worktree -- "$path" >/dev/null 2>&1 || true
            git clean -fd -- "$path" >/dev/null 2>&1 || true
            continue
        fi
        rm -rf "$path"
    done
}

# State file for tracking consecutive push failures.
STATE_FILE="$PACK_STATE_DIR/jsonl-export-state.json"

if [ -z "${GC_JSONL_ARCHIVE_REPO:-}" ] && [ ! -d "$ARCHIVE_REPO/.git" ] && [ -d "$LEGACY_ARCHIVE_REPO/.git" ]; then
    ARCHIVE_REPO="$LEGACY_ARCHIVE_REPO"
fi
if [ ! -e "$STATE_FILE" ] && [ -e "$LEGACY_STATE_FILE" ]; then
    STATE_FILE="$LEGACY_STATE_FILE"
fi
mkdir -p "$(dirname "$STATE_FILE")"

retry_pending_spike_alert

# Discover databases. Exclude Dolt/MySQL system schemas, Gas City's internal
# health-probe database, and test-fixture scratch databases (benchdb,
# testdb_*, lowercase beads_t[0-9a-f]{8,}, beads_pt*, beads_vr*,
# doctest_*, doctortest_* — matching the Go cleanup planner contract); the
# remaining databases are expected to be bead stores.
DATABASES=$(dolt_sql -r csv -q "SHOW DATABASES" 2>/dev/null | tail -n +2 \
    | grep -vi '^information_schema$\|^mysql$\|^dolt_cluster$\|^performance_schema$\|^sys$\|^__gc_probe$\|^benchdb$\|^testdb_\|^beads_pt\|^beads_vr\|^doctest_\|^doctortest_' \
    | grep -v '^beads_t[0-9a-f]\{8,\}$' || true)
if [ -z "$DATABASES" ]; then
    exit 0
fi

# Ensure archive repo exists.
if [ ! -d "$ARCHIVE_REPO/.git" ]; then
    mkdir -p "$ARCHIVE_REPO"
    git -C "$ARCHIVE_REPO" init -q 2>/dev/null || true
fi

# Build scrub filter for the issues table.
SCRUB_FILTER=""
if [ "$SCRUB" = "true" ]; then
    SCRUB_FILTER="WHERE type NOT IN ('message', 'event', 'wisp', 'agent') AND title NOT LIKE 'gc:%'"
fi

TOTAL_EXPORTED=0
TOTAL_DBS=0
FAILED_DBS=""
HALTED=0
STAGE_PATHS=()
HALT_DB=""
HALT_PREV_COUNT=0
HALT_CURRENT_COUNT=0
HALT_DELTA=0

for DB in $DATABASES; do
    TOTAL_DBS=$((TOTAL_DBS + 1))
    DB_DIR="$ARCHIVE_REPO/$DB"
    mkdir -p "$DB_DIR"

    # Step 1: Export issues table.
    if ! dolt_sql -r json -q "SELECT * FROM \`$DB\`.issues $SCRUB_FILTER" > "$DB_DIR/issues.jsonl" 2>/dev/null; then
        FAILED_DBS="${FAILED_DBS}$DB "
        continue
    fi

    # Export supplemental tables (best-effort).
    for TABLE in comments config dependencies labels metadata; do
        dolt_sql -r json -q "SELECT * FROM \`$DB\`.\`$TABLE\`" > "$DB_DIR/$TABLE.jsonl" 2>/dev/null || true
    done

    # Step 2: Validate the exported JSON payload and optionally scrub it. Even
    # when SCRUB=false we still fail the DB on malformed JSON so corrupt live
    # exports cannot silently score as zero rows and become the new baseline.
    TMPFILE=$(mktemp)
    if [ "$SCRUB" = "true" ]; then
        if ! scrub_exported_issues < "$DB_DIR/issues.jsonl" > "$TMPFILE"; then
            rm -f "$TMPFILE"
            discard_failed_db_outputs "$DB"
            FAILED_DBS="${FAILED_DBS}$DB "
            continue
        fi
    elif ! validate_exported_issues < "$DB_DIR/issues.jsonl" > "$TMPFILE"; then
        rm -f "$TMPFILE"
        discard_failed_db_outputs "$DB"
        FAILED_DBS="${FAILED_DBS}$DB "
        continue
    fi
    mv -f "$TMPFILE" "$DB_DIR/issues.jsonl"

    # Legacy flat file mirrors the scrubbed per-db export. Keep the two output
    # shapes in sync so any downstream reader sees the same filtered payload.
    if ! cp -f "$DB_DIR/issues.jsonl" "$ARCHIVE_REPO/$DB.jsonl" 2>/dev/null; then
        discard_failed_db_outputs "$DB"
        FAILED_DBS="${FAILED_DBS}$DB "
        continue
    fi

    # Count records from the final persisted payload (post-scrub / post-
    # validation) so commit messages and DOG_DONE summaries reflect what was
    # actually archived, not the pre-scrub raw export.
    CURRENT_COUNT=$(count_jsonl_rows < "$DB_DIR/issues.jsonl")
    TOTAL_EXPORTED=$((TOTAL_EXPORTED + CURRENT_COUNT))

    STAGE_PATHS+=("$DB" "$DB.jsonl")

    # Step 3: Spike detection — compare record counts against previous commit.
    PREV_COUNT=0
    if git -C "$ARCHIVE_REPO" cat-file -e "HEAD:$DB/issues.jsonl" 2>/dev/null; then
        PREV_COUNT=$(git -C "$ARCHIVE_REPO" show "HEAD:$DB/issues.jsonl" 2>/dev/null | count_jsonl_rows || echo "0")
    fi

    # Skip the percentage check on the first run (no prior commit) and when
    # the previous count is below the absolute floor — a 1→2 swing is 100% but
    # meaningless on a tiny database. The PREV_COUNT > 0 guard also avoids the
    # division-by-zero on line `DELTA=...` when the floor is set to 0 to
    # disable the small-N skip.
    if [ "$PREV_COUNT" -gt 0 ] && [ "$PREV_COUNT" -ge "$MIN_PREV_FOR_SPIKE_CHECK" ]; then
        FILTERED_COUNT=$(count_jsonl_rows < "$DB_DIR/issues.jsonl")
        DELTA=$(( (FILTERED_COUNT - PREV_COUNT) * 100 / PREV_COUNT ))
        if [ "$DELTA" -lt 0 ]; then
            DELTA=$(( -DELTA ))
        fi
        if [ "$DELTA" -gt "$SPIKE_THRESHOLD" ]; then
            HALTED=1
            HALT_DB="$DB"
            HALT_PREV_COUNT="$PREV_COUNT"
            HALT_CURRENT_COUNT="$FILTERED_COUNT"
            HALT_DELTA="$DELTA"
            echo "jsonl-export: HALTED — spike in $DB (${DELTA}% > ${SPIKE_THRESHOLD}%)"
            break
        fi
    fi
done

cd "$ARCHIVE_REPO"
if [ "${#STAGE_PATHS[@]}" -gt 0 ]; then
    if ! git add -A -- "${STAGE_PATHS[@]}"; then
        discard_staged_archive_outputs
        echo "jsonl-export: staging archive outputs failed" >&2
        exit 1
    fi
fi

# On HALT we still commit the new export so PREV_COUNT advances on the next
# run — otherwise the same spike re-fires every cooldown and floods the inbox
# (#1547 root cause #3). Push is skipped, so the spike snapshot stays local
# until a later successful non-HALT run pushes the archive forward.
if [ "$HALTED" -eq 1 ]; then
    if ! git diff --cached --quiet 2>/dev/null; then
        EXPORTED_DBS=$((TOTAL_DBS - $(echo "$FAILED_DBS" | wc -w)))
        commit_archive_snapshot \
            "[HALT] backup $(date -u +%Y-%m-%dT%H:%M:%SZ): exported=$EXPORTED_DBS/$TOTAL_DBS records=$TOTAL_EXPORTED (spike detected; push skipped)" \
            "HALT baseline" || {
            discard_staged_archive_outputs
            exit 1
        }
    fi
    set_pending_spike_alert "$HALT_DB" "$HALT_PREV_COUNT" "$HALT_CURRENT_COUNT" "$HALT_DELTA" "$SPIKE_THRESHOLD"
    if send_spike_alert "$HALT_DB" "$HALT_PREV_COUNT" "$HALT_CURRENT_COUNT" "$HALT_DELTA" "$SPIKE_THRESHOLD"; then
        clear_pending_spike_alert
    else
        echo "jsonl-export: spike alert delivery failed; will retry from state" >&2
    fi
    gc session nudge deacon/ "DOG_DONE: jsonl — HALTED on spike detection" 2>/dev/null || true
    exit 0
fi

if git diff --cached --quiet 2>/dev/null; then
    if [ -n "$FAILED_DBS" ]; then
        EXPORTED_DBS=$((TOTAL_DBS - $(echo "$FAILED_DBS" | wc -w)))
        SUMMARY="jsonl — exported $EXPORTED_DBS/$TOTAL_DBS, records: $TOTAL_EXPORTED, push: skipped, failed: $FAILED_DBS"
        gc session nudge deacon/ "DOG_DONE: $SUMMARY" 2>/dev/null || true
        echo "jsonl-export: $SUMMARY"
        exit 0
    fi
    # No changes.
    gc session nudge deacon/ "DOG_DONE: jsonl — no changes" 2>/dev/null || true
    exit 0
fi

EXPORTED_DBS=$((TOTAL_DBS - $(echo "$FAILED_DBS" | wc -w)))
commit_archive_snapshot \
    "backup $(date -u +%Y-%m-%dT%H:%M:%SZ): exported=$EXPORTED_DBS/$TOTAL_DBS records=$TOTAL_EXPORTED" \
    "archive snapshot" || {
    discard_staged_archive_outputs
    exit 1
}

PUSH_STATUS="ok"
if ! git push origin main -q 2>/dev/null; then
    PUSH_STATUS="failed"

    # Track consecutive failures.
    CONSECUTIVE=0
    if [ -f "$STATE_FILE" ]; then
        CONSECUTIVE=$(read_state_json | jq -r '.consecutive_push_failures // 0' || echo "0")
    fi
    CONSECUTIVE=$((CONSECUTIVE + 1))
    set_consecutive_push_failures "$CONSECUTIVE"

    if [ "$CONSECUTIVE" -ge "$MAX_PUSH_FAILURES" ]; then
        gc mail send mayor/ -s "ESCALATION: JSONL push failed [HIGH]" \
            -m "Consecutive failures: $CONSECUTIVE (threshold: $MAX_PUSH_FAILURES)" \
            2>/dev/null || true
    fi
else
    # Reset failure counter on success.
    set_consecutive_push_failures "0"
fi

SUMMARY="jsonl — exported $EXPORTED_DBS/$TOTAL_DBS, records: $TOTAL_EXPORTED, push: $PUSH_STATUS"
if [ -n "$FAILED_DBS" ]; then
    SUMMARY="$SUMMARY, failed: $FAILED_DBS"
fi

gc session nudge deacon/ "DOG_DONE: $SUMMARY" 2>/dev/null || true
echo "jsonl-export: $SUMMARY"
