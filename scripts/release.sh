#!/bin/bash
#
# Release script for brf
# Usage: ./scripts/release.sh <patch|minor|major> [--dry-run]
#
# Bumps the version, tags the commit, and hands off to goreleaser, which
# builds the binaries, creates the GitHub release, and updates the
# Homebrew formula in zhubert/homebrew-tap.
#
# Examples:
#   ./scripts/release.sh patch      # v0.1.0 -> v0.1.1
#   ./scripts/release.sh minor      # v0.1.0 -> v0.2.0
#   ./scripts/release.sh major      # v0.1.0 -> v1.0.0

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Parse arguments
BUMP_TYPE=""
DRY_RUN=false

for arg in "$@"; do
    case $arg in
        --dry-run)
            DRY_RUN=true
            ;;
        patch|minor|major)
            BUMP_TYPE="$arg"
            ;;
        "")
            ;;
        *)
            echo -e "${RED}Unknown argument: $arg${NC}"
            echo "Usage: ./scripts/release.sh <patch|minor|major> [--dry-run]"
            exit 1
            ;;
    esac
done

if [ -z "$BUMP_TYPE" ]; then
    echo -e "${RED}Error: Bump type argument required (patch, minor, or major)${NC}"
    echo "Usage: ./scripts/release.sh <patch|minor|major> [--dry-run]"
    exit 1
fi

# Get the latest version tag
LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

if ! [[ "$LATEST_TAG" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    echo -e "${RED}Error: Latest tag '$LATEST_TAG' is not in format vX.Y.Z${NC}"
    exit 1
fi

MAJOR="${BASH_REMATCH[1]}"
MINOR="${BASH_REMATCH[2]}"
PATCH="${BASH_REMATCH[3]}"

case $BUMP_TYPE in
    patch) PATCH=$((PATCH + 1)) ;;
    minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
    major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
esac

VERSION="v${MAJOR}.${MINOR}.${PATCH}"

echo -e "Current version: ${YELLOW}${LATEST_TAG}${NC}"
echo -e "New version:     ${GREEN}${VERSION}${NC} (${BUMP_TYPE} bump)"
echo ""

# Check prerequisites
echo "Checking prerequisites..."

if ! command -v gh &> /dev/null; then
    echo -e "${RED}Error: gh CLI is not installed${NC}"
    echo "Install with: brew install gh"
    exit 1
fi
echo "  gh CLI: found"

if ! gh auth status &> /dev/null; then
    echo -e "${RED}Error: Not authenticated with gh CLI${NC}"
    echo "Run: gh auth login"
    exit 1
fi
echo "  gh auth: authenticated"

if ! command -v goreleaser &> /dev/null; then
    echo -e "${RED}Error: goreleaser is not installed${NC}"
    echo "Install with: brew install goreleaser"
    exit 1
fi
echo "  goreleaser: found"

if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: go is not installed${NC}"
    exit 1
fi
echo "  go: found"

if [ -n "$(git status --porcelain)" ]; then
    echo -e "${RED}Error: Working directory is not clean${NC}"
    git status --short
    exit 1
fi
echo "  Working directory: clean"

CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" != "main" ]; then
    echo -e "${RED}Error: Not on main branch (currently on: $CURRENT_BRANCH)${NC}"
    exit 1
fi
echo "  Branch: main"

if git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo -e "${RED}Error: Tag $VERSION already exists${NC}"
    exit 1
fi
echo "  Tag $VERSION: available"

# goreleaser needs a token with write access to both this repo (for the
# release) and the homebrew tap (for the formula PR/push).
TOKEN_VAR=""
if [ -n "${GORELEASER_GITHUB_TOKEN:-}" ]; then
    TOKEN_VAR="GORELEASER_GITHUB_TOKEN"
elif [ -n "${GITHUB_TOKEN:-}" ]; then
    TOKEN_VAR="GITHUB_TOKEN"
elif command -v gh &> /dev/null && gh auth token &> /dev/null; then
    TOKEN_VAR="gh auth token"
else
    echo -e "${RED}Error: No GitHub token available for goreleaser${NC}"
    echo "Set GITHUB_TOKEN or run 'gh auth login' (token must have write access to zhubert/homebrew-tap)"
    exit 1
fi
echo "  GitHub token: $TOKEN_VAR"

echo ""
echo -e "${GREEN}Prerequisites check passed${NC}"

if [ "$DRY_RUN" = true ]; then
    echo ""
    echo -e "${YELLOW}Dry run - would perform:${NC}"
    echo "  1. Create and push tag $VERSION"
    echo "  2. Run goreleaser release --clean"
    echo "     - builds darwin/linux (amd64, arm64) binaries"
    echo "     - creates GitHub release $VERSION with archives + checksums"
    echo "     - updates Formula/brf.rb in zhubert/homebrew-tap"
    echo ""
    echo "Running goreleaser check (no tag, no release)..."
    goreleaser check
    exit 0
fi

# Step 1: Tag and push
echo ""
echo "Step 1: Creating and pushing tag ${VERSION}..."
git tag -a "$VERSION" -m "brf ${VERSION}"
git push origin "$VERSION"
echo "  Done"

# Step 2: Run goreleaser
echo ""
echo "Step 2: Running goreleaser..."
if [ -z "${GITHUB_TOKEN:-}" ] && [ -z "${GORELEASER_GITHUB_TOKEN:-}" ]; then
    GITHUB_TOKEN="$(gh auth token)" goreleaser release --clean
else
    goreleaser release --clean
fi
echo "  Done"

echo ""
echo -e "${GREEN}Release ${VERSION} completed!${NC}"
echo ""
echo "Users can now run:"
echo "  brew tap zhubert/tap"
echo "  brew install brf"
