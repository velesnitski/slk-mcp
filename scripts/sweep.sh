#!/usr/bin/env bash
#
# sweep.sh — pre-push scan for content that must not reach a public repo.
#
#   scripts/sweep.sh              scan tracked files at HEAD
#   scripts/sweep.sh --history    also scan every blob and commit message
#
# Two pattern sources:
#
#   1. The built-ins below: credential SHAPES only. They are safe to
#      publish because they describe a format, never a value.
#   2. .sweep-patterns.local (gitignored, one PCRE per line, # comments).
#      Deployment-specific strings — workspace labels, channel names,
#      hostnames, product codes — live ONLY here. Writing them into a
#      committed deny-list would publish the very strings the deny-list
#      exists to keep out, so the guard must never carry them.
#
# Exit 0 = clean, 1 = hits found.

set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

SCAN_HISTORY=0
[[ "${1:-}" == "--history" ]] && SCAN_HISTORY=1

# Credential shapes. Deliberately require real-token entropy so the
# repo's own placeholders (xoxb-test, xoxp-secondary, glpat-xxx) pass.
PATTERNS=(
  'xox[bpasr]-[0-9]{9,}-[0-9A-Za-z-]{10,}'
  'glpat-[A-Za-z0-9_-]{20,}'
  'gh[pousr]_[A-Za-z0-9]{36}'
  'AKIA[0-9A-Z]{16}'
  'sk-[A-Za-z0-9]{32,}'
  '-----BEGIN [A-Z ]*PRIVATE KEY-----'
)

LOCAL=.sweep-patterns.local
if [[ -f "$LOCAL" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    PATTERNS+=("$line")
  done < "$LOCAL"
else
  echo "note: $LOCAL not present — scanning credential shapes only" >&2
fi

status=0

for p in "${PATTERNS[@]}"; do
  if out=$(git grep -nIP -- "$p" 2>/dev/null) && [[ -n "$out" ]]; then
    echo "HIT (working tree) /$p/"
    echo "$out" | sed 's/^/  /'
    status=1
  fi
done

if (( SCAN_HISTORY )); then
  # Commit messages and blobs are separate stores: a string scrubbed from
  # the tree survives in either. Dumped once, then matched with perl —
  # BSD grep has no -P, and only git's grep carries PCRE.
  msgs=$(mktemp) || exit 2
  objs=$(mktemp) || exit 2
  trap 'rm -f "$msgs" "$objs"' EXIT

  git log --all --format='%H%n%B' > "$msgs"
  git cat-file --batch-all-objects --batch --buffer > "$objs" 2>/dev/null

  for p in "${PATTERNS[@]}"; do
    if ! SWEEP_PAT="$p" perl -ne 'BEGIN{$re=qr/$ENV{SWEEP_PAT}/} exit 1 if /$re/' "$msgs"; then
      echo "HIT (commit messages) /$p/"
      SWEEP_PAT="$p" perl -ne 'BEGIN{$re=qr/$ENV{SWEEP_PAT}/} print "  $_" if /$re/' "$msgs" | head -5
      status=1
    fi
    if ! SWEEP_PAT="$p" perl -ne 'BEGIN{$re=qr/$ENV{SWEEP_PAT}/} exit 1 if /$re/' "$objs"; then
      echo "HIT (history objects) /$p/"
      status=1
    fi
  done
fi

(( status )) || echo "sweep: clean"
exit $status
