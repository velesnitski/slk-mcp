# 158. Desktop extension built and attested in CI

Date: 2026-11-27

## Status

Accepted. Supersedes the local build-and-upload step of ADR 156 and 157.

## Context

The `.mcpb` was built on a maintainer's Mac and uploaded by hand. An
independent review of the install flow found the source and the guide
sound but could not verify the binary: nothing tied the file in the
release to the code in the repository.

## Decision

- The release workflow gets a second job, after the release is created,
  on `macos-latest` (lipo builds the universal binary; the repository is
  public, so the runner is free). It:
  - fails unless the tag equals the version in main.go;
  - runs `scripts/build-mcpb.sh`, the same script as `make mcpb`;
  - validates the manifest with the pinned `@anthropic-ai/mcpb` CLI;
  - writes the stable-name copy and `SHA256SUMS`;
  - attests build provenance for both `.mcpb` files with
    `actions/attest-build-provenance`;
  - uploads the files and the checksums to the release.
- docs/INSTALL.md explains `gh attestation verify slk-mcp.mcpb -R
  velesnitski/slk-mcp`, the checksum fallback and what each proves, and
  tells an agent to verify before asking the user to open the file.

## Consequences

- Anyone can prove a release file was built by this repository's
  workflow from the tagged commit.
- Releases no longer need a manual upload; `make mcpb` stays for local
  builds and for users who build from source.
- Provenance shows where a binary came from, not that the code is safe;
  reviewing the source is still the reader's call.
