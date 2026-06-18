#!/usr/bin/env python3
"""Sync the slk-mcp entry's key in ~/.claude.json to "slack v<version>".

Why this exists: Claude Code's /mcp dialog labels each server by its
*config key* in ~/.claude.json, NOT by the serverInfo.name the server
reports during initialize. So embedding the version in serverInfo.name
(main.go) surfaces it in the instructions header but never in the /mcp
dialog. The only lever for the dialog label is the config key — and a
hand-typed version in the key goes stale the moment the binary is bumped.

This script keeps the key truthful automatically: it locates the slk-mcp
entry by its *binary path* (not by the key, which may already carry a
version), asks that exact binary its version (`-version`), and renames
the key to "slack v<version>". Idempotent, atomic write, keeps a .bak.

Run it via `make install` (build + sync), then RESTART Claude Code.
NB: a `/mcp` reconnect is NOT enough — the dialog label is read from the
config key at session start, so the renamed key only shows after a full
restart (a reconnect just re-runs the server process). See ADR 024.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

CLAUDE = os.path.expanduser("~/.claude.json")
BINARY_MATCH = "slk-mcp"  # path fragment that identifies the slack server entry


def mcp_containers(cfg):
    """Every mcpServers dict in the config: the root one plus per-project ones."""
    out = []
    if isinstance(cfg.get("mcpServers"), dict):
        out.append(cfg["mcpServers"])
    for proj in (cfg.get("projects") or {}).values():
        if isinstance(proj, dict) and isinstance(proj.get("mcpServers"), dict):
            out.append(proj["mcpServers"])
    return out


def binary_version(command):
    try:
        r = subprocess.run([command, "-version"], capture_output=True, text=True, timeout=10)
        return r.stdout.strip()
    except Exception as e:  # noqa: BLE001 — best-effort tooling
        print(f"  ! could not read version from {command}: {e}")
        return ""


def rename_in(container):
    """Rename the slk-mcp entry's key to 'slack v<version>'. Returns True if changed."""
    changed = False
    for key in list(container.keys()):
        entry = container[key]
        command = entry.get("command", "") if isinstance(entry, dict) else ""
        if BINARY_MATCH not in command:
            continue
        version = binary_version(command)
        if not version:
            continue
        new_key = f"slack v{version}"
        if key == new_key:
            print(f"  = already '{new_key}'")
            continue
        # Preserve insertion order: rebuild the dict with just this key renamed.
        rebuilt = {(new_key if k == key else k): v for k, v in container.items()}
        container.clear()
        container.update(rebuilt)
        print(f"  ✓ '{key}' → '{new_key}'")
        changed = True
    return changed


def main():
    if not os.path.exists(CLAUDE):
        print(f"no config at {CLAUDE} — nothing to do")
        return 0
    with open(CLAUDE) as f:
        cfg = json.load(f)

    if not any(rename_in(c) for c in mcp_containers(cfg)):
        print("nothing to update (slk-mcp entry not found or label already current)")
        return 0

    shutil.copy2(CLAUDE, CLAUDE + ".bak")
    # Atomic replace so a crash never leaves ~/.claude.json half-written.
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(CLAUDE), suffix=".tmp")
    with os.fdopen(fd, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
    os.replace(tmp, CLAUDE)
    print(f"updated {CLAUDE} (backup: {CLAUDE}.bak)")
    print("→ RESTART Claude Code to see the new label (a /mcp reconnect is NOT enough)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
