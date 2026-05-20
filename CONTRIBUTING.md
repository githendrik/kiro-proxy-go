# Release Guide for Agents

This document describes how to release a new version of kiro-proxy.

## Prerequisites

- Write access to `githendrik/kiro-proxy-go` repository
- GitHub CLI (`gh`) installed and authenticated
- No network/proxy issues (ensure `github.com` is accessible)

## Release Process

### 1. Prepare the Release

```bash
# Ensure you're on main branch
git checkout main
git pull origin main

# Verify the working tree is clean
git status

# Run tests (if available)
go test ./...

# Build locally to verify
go build -o kiro-proxy .
```

### 2. Create and Push Tag

```bash
# Determine version number (follow semver: MAJOR.MINOR.PATCH)
# - MAJOR: breaking changes
# - MINOR: new features (backward compatible)
# - PATCH: bug fixes (backward compatible)

git tag v0.2.0  # Replace with your version
git push --tags
```

### 3. Monitor the Release

GitHub Actions will automatically:
- Build binaries for Linux/macOS (amd64 + arm64)
- Create GitHub release with binaries
- Update the Homebrew tap formula

```bash
# Watch the workflow
gh run watch -R githendrik/kiro-proxy-go

# Or list recent runs
gh run list -R githendrik/kiro-proxy-go --limit 5

# View release
gh release view v0.2.0 -R githendrik/kiro-proxy-go
```

### 4. Verify the Release

Check the following:

```bash
# 1. GitHub Release has all binaries
gh release view v0.2.0 -R githendrik/kiro-proxy-go

# Expected assets:
# - checksums.txt
# - kiro-proxy_Darwin_arm64.tar.gz
# - kiro-proxy_Darwin_x86_64.tar.gz
# - kiro-proxy_Linux_arm64.tar.gz
# - kiro-proxy_Linux_x86_64.tar.gz
# - LICENSE

# 2. Homebrew formula was updated
curl -s https://raw.githubusercontent.com/githendrik/homebrew-tap/main/Formula/kiro-proxy.rb | grep version
# Should show: version "0.2.0"

# 3. Test install via brew (optional)
brew tap githendrik/tap
brew install kiro-proxy
kiro-proxy --help
```

## Troubleshooting

### Workflow Fails

```bash
# Check workflow logs
gh run view <RUN_ID> -R githendrik/kiro-proxy-go --log

# Common issues:
# - Network/proxy issues (add github.com to NO_PROXY)
# - Invalid GoReleaser config
# - Missing secrets (HOMEBREW_TAP_GITHUB_TOKEN)
```

### Brew Formula Not Updated

Check if the workflow ran in the tap repo:

```bash
gh run list -R githendrik/homebrew-tap --limit 5
```

If no runs, check the secret is still valid:

```bash
gh secret list -R githendrik/kiro-proxy-go
```

### Need to Redo a Release

```bash
# Delete the tag locally and remotely
git tag -d v0.2.0
git push origin --delete v0.2.0

# Delete the release
gh release delete v0.2.0 -R githendrik/kiro-proxy-go --cleanup-tag

# Fix issues, then re-tag
git tag v0.2.0
git push --tags
```

## Example: Full Release Session

```bash
# Pull latest changes
git checkout main && git pull

# Build and test
go build -o kiro-proxy . && ./kiro-proxy --help

# Tag and push
git tag v0.2.0 && git push --tags

# Watch release
gh run watch -R githendrik/kiro-proxy-go

# Verify
gh release view v0.2.0 -R githendrik/kiro-proxy-go
curl -s https://raw.githubusercontent.com/githendrik/homebrew-tap/main/Formula/kiro-proxy.rb | grep version
```

## Automated Release Checklist

- [ ] Working tree clean, all changes committed
- [ ] Tests pass (if available)
- [ ] Version number follows semver
- [ ] Tag created and pushed
- [ ] GitHub Actions workflow completes successfully
- [ ] Release has all binaries (4 platforms + checksums)
- [ ] Homebrew formula updated with correct version/SHA256
- [ ] Release notes are accurate (auto-generated from commits)
