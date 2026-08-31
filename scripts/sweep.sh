#!/usr/bin/env bash
#
# Sensitive-data sweep — run before every push (see CLAUDE.md).
#
#   scripts/sweep.sh                 scan tracked files at HEAD
#   scripts/sweep.sh --history       also scan every blob and commit message
#   scripts/sweep.sh --shapes-only   credential shapes only, no deny-list
#   scripts/sweep.sh --quiet         report file:line only, never the match
#
# Two pattern sources:
#
#   1. Credential SHAPES, below. Safe to publish because each describes a
#      format, never a value. Written to require real-token entropy so the
#      repo's own placeholders (xoxb-test, xoxp-secondary) don't trip them.
#   2. .sweep-patterns.local — UNTRACKED, gitignored. Deployment-specific
#      strings: workspace labels, channel names, hostnames, product and
#      ticket codes. A committed deny-list publishes exactly what it
#      guards, in a file whose purpose announces the strings are sensitive.
#      Copy .sweep-patterns.example and fill it in.
#
# Pattern file format (one extended regex per line; # and blank ignored):
#   plain line   → matched case-INSENSITIVE (products, hostnames, brands)
#   CS: prefix   → matched case-SENSITIVE (uppercase ticket / project codes;
#                  a blanket (?i) clobbers legitimate lowercase words that
#                  merely share the prefix)
#
# FAILS CLOSED. A missing or empty pattern file exits 2 rather than
# scanning a subset and reporting "clean" — the aborted run is the common
# case, so a green result there would be the most dangerous output the
# script can produce. --shapes-only is the explicit, named way to run the
# reduced scan; it never happens by accident.
#
# `sweep:allow` is now a HISTORY-ONLY exemption, and only for SHAPE rules.
#
# The working tree honours NO exemption: the redaction suite used to need
# one, because it must contain credential-shaped strings to prove it
# catches them, but those fixtures are assembled at runtime now
# (internal/export/redact_test.go). Nothing tracked needs the marker, so
# a credential shape in a tracked file always fails — no escape hatch to
# reach for, and none to review.
#
# History still needs it: old objects carry those fixtures as literals,
# blobs are immutable, and rewriting history to scrub synthetic strings
# is not worth the cost. Deny-list patterns apply to marked lines even
# there, or the marker would become a way to smuggle a real identifier
# past every rule.
#
# Exit 0 = clean, 1 = matches found, 2 = misconfigured (no patterns).

set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

PATTERNS_FILE=.sweep-patterns.local
EXAMPLE=.sweep-patterns.example
ALLOW='sweep:allow'

SCAN_HISTORY=0
SHAPES_ONLY=0
QUIET=0
for arg in "$@"; do
  case "$arg" in
    --history)     SCAN_HISTORY=1 ;;
    --shapes-only) SHAPES_ONLY=1 ;;
    --quiet)       QUIET=1 ;;
    *) echo "sweep: unknown argument $arg" >&2; exit 2 ;;
  esac
done

# Credential shapes. Case-sensitive: every one of these is anchored on a
# literal prefix that is itself case-significant.
SHAPES=(
  'xox[bpasr]-[0-9]{9,}-[0-9A-Za-z-]{10,}'
  'glpat-[A-Za-z0-9_-]{20,}'
  'gh[pousr]_[A-Za-z0-9]{36}'
  'AKIA[0-9A-Z]{16}'
  'sk-[A-Za-z0-9]{32,}'
  '-----BEGIN [A-Z ]*PRIVATE KEY-----'
)
SHAPE_PATTERN="$(IFS='|'; echo "${SHAPES[*]}")"

CI_PATTERN=""
CS_PATTERN=""

if (( SHAPES_ONLY )); then
  echo "sweep: --shapes-only — credential shapes only, deny-list NOT applied" >&2
else
  if [[ ! -f "$PATTERNS_FILE" ]]; then
    echo "sweep: $PATTERNS_FILE not found — copy $EXAMPLE and fill in real patterns." >&2
    echo "sweep: refusing to report a partial scan as clean (use --shapes-only to opt in)." >&2
    exit 2
  fi
  CI_PATTERN="$(grep -vE '^[[:space:]]*(#|$)' "$PATTERNS_FILE" | grep -vE '^CS:' | paste -sd'|' - || true)"
  CS_PATTERN="$(grep -E '^CS:' "$PATTERNS_FILE" | sed 's/^CS://' | paste -sd'|' - || true)"
  if [[ -z "$CI_PATTERN" && -z "$CS_PATTERN" ]]; then
    echo "sweep: $PATTERNS_FILE has no patterns." >&2
    exit 2
  fi
fi

status=0

# ---------------------------------------------------------------------------
# Working tree
# ---------------------------------------------------------------------------
# The example template is excluded: its illustrative patterns would match
# themselves. Plain grep over `git ls-files`, not `git grep` — the fleet has
# seen `git grep` silently return nothing under a wrapper, and a security
# scan that returns nothing is indistinguishable from a clean one.
files() { git ls-files -z | grep -zv "^${EXAMPLE}\$"; }

report() { # <label> <matches>
  [[ -z "$2" ]] && return 0
  echo "sweep: $1" >&2
  if (( QUIET )); then
    # A public repo's Actions logs are PUBLIC. Printing the matched line
    # there would publish the exact string the guard exists to keep out,
    # in a log no history rewrite ever reaches. Locations only.
    echo "$2" | cut -d: -f1,2 | sed 's/^/  /' >&2
    echo "  (content withheld — rerun locally without --quiet to see it)" >&2
  else
    echo "$2" >&2
  fi
  status=1
}

# No exemption in the tree, for shapes or anything else.
M="$(files | xargs -0 grep -nE "$SHAPE_PATTERN" -- 2>/dev/null || true)"
report "CREDENTIAL SHAPE — do not push:" "$M"

if [[ -n "$CI_PATTERN" ]]; then
  M="$(files | xargs -0 grep -niE "$CI_PATTERN" -- 2>/dev/null || true)"
  report "BANNED STRING — do not push:" "$M"
fi
if [[ -n "$CS_PATTERN" ]]; then
  M="$(files | xargs -0 grep -nE "$CS_PATTERN" -- 2>/dev/null || true)"
  report "TICKET-ID / PROJECT-CODE LEAK — do not push:" "$M"
fi

# ---------------------------------------------------------------------------
# History
# ---------------------------------------------------------------------------
# Commit messages and the object store are separate stores: a string
# scrubbed from the tree survives in either, and they are fixed by
# different mechanisms. perl, not grep -P — BSD grep has no -P.
if (( SCAN_HISTORY )); then
  msgs=$(mktemp) || exit 2
  objs=$(mktemp) || exit 2
  trap 'rm -f "$msgs" "$objs"' EXIT

  git log --all --format='%H%n%B' > "$msgs"
  git cat-file --batch-all-objects --batch --buffer > "$objs" 2>/dev/null

  scan_history() { # <regex> <case-flag i|""> <label> <honour-allow 0|1>
    local rx="$1" ci="$2" label="$3" allow="$4" store hits
    for store in "$msgs" "$objs"; do
      if (( allow )); then
        hits="$(grep -av "$ALLOW" "$store" | grep -ac"$ci"E -- "$rx" || true)"
      else
        hits="$(grep -ac"$ci"E -- "$rx" "$store" || true)"
      fi
      if [[ "$hits" != "0" ]]; then
        local where="commit messages"; [[ "$store" == "$objs" ]] && where="history objects"
        echo "sweep: $label in $where — $hits line(s)" >&2
        status=1
      fi
    done
  }

  scan_history "$SHAPE_PATTERN" "" "CREDENTIAL SHAPE" 1
  [[ -n "$CI_PATTERN" ]] && scan_history "$CI_PATTERN" "i" "BANNED STRING" 0
  [[ -n "$CS_PATTERN" ]] && scan_history "$CS_PATTERN" "" "TICKET-ID / PROJECT-CODE" 0
fi

(( status )) && exit 1

tracked="$(git ls-files | wc -l | tr -d ' ')"
scope="tree"
(( SCAN_HISTORY )) && scope="tree + history"
echo "sweep: clean ($tracked tracked files, $scope)"
