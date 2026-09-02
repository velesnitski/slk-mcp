#!/usr/bin/env bash
#
# Print the CHANGELOG section for one version — the release notes.
#
#   scripts/changelog-section.sh 1.40.0
#   scripts/changelog-section.sh v1.40.0    # the leading v is optional
#
# Used by the release workflow and by anyone backfilling a release by
# hand, so the notes on GitHub and the notes in CHANGELOG.md cannot
# drift: there is one source and both read it.
#
# Exit 0 = section printed, 1 = no such version, 2 = usage.

set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

version="${1:-}"
[[ -z "$version" ]] && { echo "usage: $0 <version>" >&2; exit 2; }
version="${version#v}"

# Print everything between this version's header and the next one. The
# header line itself is dropped: GitHub already shows the tag and date.
section="$(awk -v v="$version" '
  $0 ~ "^## \\[" v "\\]" { inside = 1; next }
  inside && /^## \[/     { exit }
  inside                 { print }
' CHANGELOG.md)"

# Trim leading and trailing blank lines.
section="$(printf '%s' "$section" | awk 'NF {found=1} found' | awk '{ lines[NR] = $0 } END { last = NR; while (last > 0 && lines[last] ~ /^[[:space:]]*$/) last--; for (i = 1; i <= last; i++) print lines[i] }')"

if [[ -z "$section" ]]; then
  echo "changelog-section: no entry for $version in CHANGELOG.md" >&2
  exit 1
fi

printf '%s\n' "$section"
