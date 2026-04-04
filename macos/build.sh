#!/bin/bash
set -e

# Build script for RepoView macOS app
# Usage: ./build.sh [--release]

cd "$(dirname "$0")"

RELEASE_MODE=false
if [[ "$1" == "--release" ]]; then
    RELEASE_MODE=true
fi

echo "==> Building repoview Go binary for macOS..."
cd ..

# Build universal binary (both Intel and Apple Silicon)
GOOS=darwin GOARCH=amd64 go build -o repoview-amd64 .
GOOS=darwin GOARCH=arm64 go build -o repoview-arm64 .
lipo -create -output macos/repoview repoview-amd64 repoview-arm64
rm repoview-amd64 repoview-arm64

echo "==> Building macOS app..."
cd macos/RepoView

if $RELEASE_MODE; then
    xcodebuild -project RepoView.xcodeproj \
        -scheme RepoView \
        -configuration Release \
        -derivedDataPath build \
        clean build

    APP_PATH="build/Build/Products/Release/RepoView.app"
else
    xcodebuild -project RepoView.xcodeproj \
        -scheme RepoView \
        -configuration Debug \
        -derivedDataPath build \
        build

    APP_PATH="build/Build/Products/Debug/RepoView.app"
fi

echo ""
echo "==> Build complete!"
echo "    App location: $(pwd)/$APP_PATH"

if $RELEASE_MODE; then
    echo ""
    echo "==> Creating distributable zip..."
    cd build/Build/Products/Release
    zip -r RepoView.zip RepoView.app
    echo "    Zip location: $(pwd)/RepoView.zip"
fi
