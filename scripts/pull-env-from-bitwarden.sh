#!/usr/bin/env bash
# Pull a Bitwarden Secure Note into ./.env for Compose.
#
# Prerequisites:
#   - Bitwarden CLI (`bw`) installed: https://bitwarden.com/help/cli/
#   - A Secure Note whose body is KEY=value lines (see deploy/bitwarden-secure-note.example.env)
#
# Usage:
#   ./scripts/pull-env-from-bitwarden.sh "processmanager-remote-env"
#   ./scripts/pull-env-from-bitwarden.sh <item-id>
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

NOTE_NAME_OR_ID="${1:-}"
OUT="${2:-.env}"

if [[ -z "$NOTE_NAME_OR_ID" ]]; then
  echo "usage: $0 <secure-note-name-or-id> [output-file]" >&2
  exit 1
fi

if ! command -v bw >/dev/null 2>&1; then
  echo "bw (Bitwarden CLI) not found: https://bitwarden.com/help/cli/" >&2
  exit 1
fi

if [[ -z "${BW_SESSION:-}" ]]; then
  echo "BW_SESSION is unset. Unlock first:" >&2
  echo "  export BW_SESSION=\$(bw unlock --raw)" >&2
  exit 1
fi

# Prefer notes field by name/id. Falls back to item JSON .notes.
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

if ! bw get notes "$NOTE_NAME_OR_ID" >"$TMP" 2>/dev/null; then
  if command -v jq >/dev/null 2>&1; then
    bw get item "$NOTE_NAME_OR_ID" | jq -r '.notes // empty' >"$TMP"
  else
    echo "failed to read secure note '$NOTE_NAME_OR_ID' (install jq for item-id fallback)" >&2
    exit 1
  fi
fi

if [[ ! -s "$TMP" ]]; then
  echo "secure note '$NOTE_NAME_OR_ID' is empty" >&2
  exit 1
fi

# Keep only env-looking lines (KEY=VALUE), drop comment/marker lines.
grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$TMP" >"${OUT}.tmp"
mv "${OUT}.tmp" "$OUT"
chmod 600 "$OUT"

echo "Wrote $OUT ($(wc -l <"$OUT") keys). Run: ./scripts/remote-up.sh"
