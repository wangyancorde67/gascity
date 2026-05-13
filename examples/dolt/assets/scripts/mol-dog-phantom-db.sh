#!/usr/bin/env bash
# mol-dog-phantom-db — detect and escalate phantom Dolt database directories.
#
# Read-only scan of the data dir: surfaces any <db>/.dolt/ without a
# noms/manifest, plus any *.replaced-YYYYMMDDTHHMMSSZ leftover, via
# escalation mail to the mayor. Operator decides remediation.
#
# Runs as an exec order (no LLM, no agent, no wisp).
set -euo pipefail

PACK_DIR="${GC_PACK_DIR:-$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
. "$PACK_DIR/assets/scripts/runtime.sh"

DATA_DIR="${GC_PHANTOM_DATA_DIR:-$DOLT_DATA_DIR}"

# --- Step 1: Scan for phantom database directories ---

if [ ! -d "$DATA_DIR" ]; then
    echo "phantom-db: data dir $DATA_DIR not found, skipping"
    exit 0
fi

SCANNED=0
UNSERVABLES=""
PHANTOM_COUNT=0
RETIRED_COUNT=0
UNSERVABLE_COUNT=0
VALID=0

for dir in "$DATA_DIR"/*/; do
    [ -d "$dir" ] || continue
    db_name=$(basename "$dir")
    if [ "$db_name" = ".quarantine" ]; then
        continue
    fi
    SCANNED=$((SCANNED + 1))
    is_unservable=0
    if [ -d "$dir/.dolt" ]; then
        if [ ! -f "$dir/.dolt/noms/manifest" ]; then
            PHANTOM_COUNT=$((PHANTOM_COUNT + 1))
            is_unservable=1
        fi
        case "$db_name" in
            *.replaced-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z)
                RETIRED_COUNT=$((RETIRED_COUNT + 1))
                is_unservable=1
                ;;
        esac
    fi
    if [ "$is_unservable" -eq 1 ]; then
        UNSERVABLES="$UNSERVABLES $db_name"
        UNSERVABLE_COUNT=$((UNSERVABLE_COUNT + 1))
    else
        VALID=$((VALID + 1))
    fi
done

if [ "$UNSERVABLE_COUNT" -eq 0 ]; then
    SUMMARY="phantom-db — scanned: $SCANNED, phantoms: 0, retired: 0, valid: $VALID"
    gc session nudge deacon/ "DOG_DONE: $SUMMARY" 2>/dev/null || true
    echo "phantom-db: $SUMMARY"
    exit 0
fi

# --- Step 2: Escalate to operator ---

gc mail send mayor/ \
    -s "ESCALATION: Unservable Dolt databases detected [HIGH]" \
    -m "Dolt: detected $UNSERVABLE_COUNT phantom database(s) in $DATA_DIR:$UNSERVABLES — requires Dolt server restart to resolve.

On the next server start, Dolt's ListDatabases skips phantom directories
with a stderr warning; restarting is sufficient to clear the immediate
crash risk. To reclaim disk space after the restart:

  dolt sql -q 'DROP DATABASE \`<db_name>\`;'

Investigate root cause (incomplete dolt init, interrupted replacement,
manual filesystem edit) before re-creating affected databases." \
    2>/dev/null || true

# --- Step 3: Report ---

SUMMARY="phantom-db — scanned: $SCANNED, phantoms: $PHANTOM_COUNT, retired: $RETIRED_COUNT, escalated: $UNSERVABLE_COUNT"
gc session nudge deacon/ "DOG_DONE: $SUMMARY" 2>/dev/null || true
echo "phantom-db: $SUMMARY"
