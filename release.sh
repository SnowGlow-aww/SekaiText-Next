#!/usr/bin/env bash
set -euo pipefail

if [[ $(git branch --show-current) != "main" ]]; then
  echo "Release must run from the main branch."
  exit 1
fi
if [[ -n $(git status --porcelain) ]]; then
  echo "Release requires a clean working tree, including untracked files."
  exit 1
fi

read -r -p "Version (e.g. 5.9.0): " VERSION
node scripts/sync-version.mjs --release-plan "$VERSION" >/dev/null
TAG="v$VERSION"

if git show-ref --verify --quiet "refs/tags/$TAG"; then
  echo "Tag $TAG already exists locally."
  exit 1
fi
if ! REMOTE_TAG=$(git ls-remote --tags origin "refs/tags/$TAG"); then
  echo "Could not check $TAG on origin."
  exit 1
fi
if [[ -n "$REMOTE_TAG" ]]; then
  echo "Tag $TAG already exists on origin."
  exit 1
fi

CURRENT_VERSION=$(node -p "require('./package.json').version")
if [[ "$CURRENT_VERSION" != "$VERSION" ]]; then
  npm version "$VERSION" --no-git-tag-version
else
  echo "Version already $VERSION, skipping bump."
fi

node scripts/sync-version.mjs
node scripts/sync-version.mjs --check --tag "$TAG"

# Paths are controlled by sync-version.mjs and contain no whitespace.
# shellcheck disable=SC2046
git add -- $(node scripts/sync-version.mjs --release-files "$VERSION")
if ! git diff --cached --quiet; then
  git commit -m "$TAG"
else
  echo "No version changes to commit; tagging the current commit."
fi

git tag "$TAG"
git push --atomic origin main "$TAG"

echo "Release $TAG pushed; CI will build and publish it."
