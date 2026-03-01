#!/usr/bin/env bash
set -euo pipefail

# release.sh — create a new versioned release
#
# Usage:
#   ./release.sh          # auto-bump patch: v0.1.0 → v0.1.1
#   ./release.sh minor    # bump minor:      v0.1.0 → v0.2.0
#   ./release.sh major    # bump major:      v0.1.0 → v1.0.0
#   ./release.sh 2.0.0    # explicit version

BUMP="${1:-patch}"

# Fetch latest tags from remote
git fetch --tags --quiet

# Find latest vX.Y.Z tag (sorted by semver), default to v0.0.0
LATEST=$(git tag -l 'v*' | sort -V | tail -n1)
LATEST="${LATEST:-v0.0.0}"

# Parse major.minor.patch
IFS='.' read -r MAJOR MINOR PATCH <<< "${LATEST#v}"

# Determine next version
case "$BUMP" in
  patch) PATCH=$((PATCH + 1)) ; NEXT="${MAJOR}.${MINOR}.${PATCH}" ;;
  minor) MINOR=$((MINOR + 1)) ; NEXT="${MAJOR}.${MINOR}.0" ;;
  major) MAJOR=$((MAJOR + 1)) ; NEXT="${MAJOR}.0.0" ;;
  *)     NEXT="$BUMP" ;;  # explicit version
esac

TAG="v${NEXT}"
DATE=$(date +%Y-%m-%d)

echo "Releasing ${TAG} (previous: ${LATEST})"

# Update CHANGELOG.md: replace [Unreleased] with version header, add new [Unreleased]
if ! grep -q '^\## \[Unreleased\]' CHANGELOG.md; then
  echo "Error: no [Unreleased] section found in CHANGELOG.md" >&2
  exit 1
fi

# Portable in-place edit (works on both macOS and Linux)
TMP=$(mktemp)
sed "s/^## \[Unreleased\]/## [${TAG}] - ${DATE}/" CHANGELOG.md > "$TMP" && mv "$TMP" CHANGELOG.md

# Insert new [Unreleased] section after the Changelog header
TMP=$(mktemp)
awk '/^# Changelog/ { print; print ""; print "## [Unreleased]"; print ""; next } 1' CHANGELOG.md > "$TMP" && mv "$TMP" CHANGELOG.md

# Extract changelog for this version (content between version header and next version header)
CHANGELOG=$(awk "/^## \[${TAG}\]/{flag=1; next} /^## \[v[0-9]/{flag=0} flag" CHANGELOG.md | sed '/^$/N;/^\n$/d')

# Commit and tag (include changelog in tag message)
git add CHANGELOG.md
git commit -m "Release ${TAG}"
git tag -a "${TAG}" -m "${TAG}

${CHANGELOG}"

echo "Pushing commit and tag..."
git push
git push origin "${TAG}"

echo ""
echo "Done! ${TAG} pushed — GitHub Actions will build the release."
