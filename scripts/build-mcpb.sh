#!/usr/bin/env bash
# Build dist/slk-mcp-<version>.mcpb: a desktop extension for Claude
# Desktop / Cowork on macOS and Windows (ADR 156).
#
# One file serves both: a universal macOS binary (arm64 + amd64, joined
# with lipo, so this script runs on macOS) and a Windows amd64 .exe,
# which Windows on ARM runs under emulation. The manifest picks the .exe
# on win32 through platform_overrides.
set -euo pipefail
cd "$(dirname "$0")/.."

version=$(grep -m1 'version = ' main.go | sed -E 's/.*"([^"]+)".*/\1/')
ldflags="-s -w -X main.version=${version}"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/server" dist

command -v lipo >/dev/null || { echo "build-mcpb: lipo not found (run on macOS)" >&2; exit 2; }

for arch in arm64 amd64; do
	GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$stage/slk-mcp-$arch" .
done
lipo -create -output "$stage/server/slk-mcp" "$stage/slk-mcp-arm64" "$stage/slk-mcp-amd64"
rm "$stage"/slk-mcp-arm64 "$stage"/slk-mcp-amd64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$stage/server/slk-mcp.exe" .

sed "s/@VERSION@/${version}/" packaging/mcpb/manifest.json > "$stage/manifest.json"
cp LICENSE "$stage/"

out="dist/slk-mcp-${version}.mcpb"
rm -f "$out"
(cd "$stage" && zip -qr -X "$OLDPWD/$out" manifest.json LICENSE server)
echo "$out"
