#!/usr/bin/env bash
# Build SimpleRemote.app (universal arm64 + x86_64) and zip it for the server.
# Run on macOS with Xcode command line tools installed.
#
#   scripts/build-macos-app.sh [version] [output-dir]
#
# Set DEFAULT_SERVER=https://support.example.com to bake in a server address
# (needed for notarized builds, which the server can't stamp).
set -euo pipefail
VERSION="${1:-dev}"
OUT="${2:-dist/agents}"
WORK="$(mktemp -d)"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.defaultServer=${DEFAULT_SERVER:-}"

for arch in arm64 amd64; do
  clang_arch=$([ "$arch" = amd64 ] && echo x86_64 || echo arm64)
  CGO_ENABLED=1 GOOS=darwin GOARCH=$arch \
    CGO_CFLAGS="-arch $clang_arch -mmacosx-version-min=12.3" CGO_LDFLAGS="-arch $clang_arch -mmacosx-version-min=12.3" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$WORK/agent-$arch" ./agent/cmd/simpleremote-agent
done

APP="$WORK/SimpleRemote.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
lipo -create -output "$APP/Contents/MacOS/simpleremote-agent" "$WORK/agent-arm64" "$WORK/agent-amd64"
sed "s/__VERSION__/${VERSION#v}/g" deploy/macos/Info.plist > "$APP/Contents/Info.plist"

if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
  if [ -z "${DEFAULT_SERVER:-}" ]; then
    echo "DEFAULT_SERVER is required for signed builds: the server can't stamp a notarized app" >&2
    exit 1
  fi
  # Tell the server not to modify this (signed) app.
  printf '%s\n' "$DEFAULT_SERVER" > "$APP/Contents/Resources/simpleremote-nostamp"
  codesign --force --options runtime --timestamp --sign "$MACOS_SIGN_IDENTITY" "$APP"
else
  # Ad-hoc signature: required for Apple silicon, but not trusted by Gatekeeper.
  codesign --force --sign - "$APP"
fi

mkdir -p "$OUT"
rm -f "$OUT/simpleremote-agent-darwin-universal.zip"
ditto -c -k --keepParent "$APP" "$OUT/simpleremote-agent-darwin-universal.zip"
echo "built $OUT/simpleremote-agent-darwin-universal.zip"
