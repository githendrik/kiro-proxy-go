# Release Setup Guide

## One-Time Setup

### 1. Create Homebrew Tap Repository

Create a new GitHub repository named `homebrew-tap`:

```bash
# Go to https://github.com/new
# Repository name: homebrew-tap
# Description: Homebrew tap for kiro-proxy
# Public
# Create repository
```

### 2. Initialize the Tap Repository

```bash
# Clone the tap repository
git clone https://github.com/githendrik/homebrew-tap.git
cd homebrew-tap

# Create directory structure
mkdir -p Formula

# Add README
cat > README.md << 'EOF'
# Homebrew Tap

Homebrew tap for kiro-proxy-go.

## Usage

```bash
brew tap githendrik/tap
brew install kiro-proxy-go
```
EOF

# Initial commit
git add README.md
git commit -m "Initial commit"
git push origin main
```

### 3. Configure GitHub Secrets

Go to your `kiro-proxy-go` repository settings:
1. Navigate to: **Settings** → **Secrets and variables** → **Actions**
2. Click **New repository secret**
3. Add the following secret:

| Name | Value |
|------|-------|
| `HOMEBREW_TAP_GITHUB_TOKEN` | A GitHub Personal Access Token (PAT) with `repo` scope |

**To create the PAT:**
1. Go to: https://github.com/settings/tokens/new
2. Description: `goreleaser-tap`
3. Expiration: No expiration (or choose expiry)
4. Scopes: Select `repo` (Full control of private repositories)
5. Click **Generate token**
6. Copy the token and add it as the secret above

### 4. Add Initial Formula to Tap

Copy the formula to your tap repository:

```bash
cd /path/to/homebrew-tap
cp /path/to/kiro-proxy-go/Formula/kiro-proxy.rb Formula/
git add Formula/
git commit -m "Add kiro-proxy formula"
git push origin main
```

## Making a Release

### 1. Tag the Release

```bash
git tag v0.2.0
git push --tags
```

### 2. Automatic Build & Release

GitHub Actions will automatically:
- Build binaries for Linux and macOS (amd64 + arm64)
- Create a GitHub release with binaries attached
- Generate checksums
- Update the brew tap formula with correct version and SHA256

### 3. Verify Release

1. Check the **Actions** tab in your repository
2. Wait for the "Release" workflow to complete
3. Verify the release at: https://github.com/githendrik/kiro-proxy-go/releases
4. Verify the formula was updated in: https://github.com/githendrik/homebrew-tap/blob/main/Formula/kiro-proxy.rb

## Installation (End Users)

```bash
# Add the tap
brew tap githendrik/tap

# Install
brew install kiro-proxy

# Upgrade when new release
brew upgrade kiro-proxy
```

## Testing Local Release

Before pushing a tag, test the release locally:

```bash
# Install GoReleaser
brew install goreleaser

# Test build (without publishing)
goreleaser release --snapshot --clean

# Verify binaries in ./dist/
ls -la dist/
```

## Troubleshooting

### Release workflow fails

1. Check the Actions log for errors
2. Verify `HOMEBREW_TAP_GITHUB_TOKEN` secret is set correctly
3. Ensure the tap repository exists and is accessible

### Formula SHA256 mismatch

GoReleaser automatically calculates and updates the SHA256. If there's a mismatch:
1. Delete the formula from the tap repo
2. Re-run the release workflow
3. Or manually update the SHA256 in the formula

### Binary not found after install

Verify the formula installs to the correct path:
```bash
brew info kiro-proxy
which kiro-proxy
```
