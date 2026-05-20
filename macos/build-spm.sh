#!/bin/bash
set -e

cd "$(dirname "$0")/.."

echo "==> Building repoview Go binary..."
GOOS=darwin GOARCH=arm64 go build -o macos/repoview-go .

echo "==> Building RepoView Swift app..."
cd macos
swift build -c release 2>&1

SWIFT_BIN=".build/release/RepoView"
APP_DIR="build/RepoView.app/Contents/MacOS"

echo "==> Assembling RepoView.app bundle..."
rm -rf build/RepoView.app
mkdir -p "$APP_DIR"
mkdir -p "build/RepoView.app/Contents/Resources"

cp "$SWIFT_BIN" "$APP_DIR/RepoView"
cp repoview-go "$APP_DIR/repoviewd"
cp Sources/RepoView/Info.plist "build/RepoView.app/Contents/Info.plist"

echo ""
echo "==> Done! App bundle: macos/build/RepoView.app"
echo "    Run with: open macos/build/RepoView.app"
