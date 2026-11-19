# Packaging and Distribution Pipeline for `mem`

This directory manages multi-platform compilation, npm packaging, and release automation for `mem`.

## Overview

The distribution uses the architecture pioneered by modern tools (such as esbuild and biome):
- A lightweight primary package (`mem` or `@scope/mem`) containing an executable Node.js wrapper (`bin/mem.js`).
- Precompiled standalone platform binaries packaged individually into separate npm packages.
- Platform packages are declared as `optionalDependencies` of the main package, pinned to the identical version.
- When a user runs `npx <pkg>` or `npm install -g <pkg>`, the package manager installs the main wrapper and only the matching native package for the host operating system and CPU architecture.
- The wrapper shim locates the platform binary with `require.resolve`, enforces Unix execution permissions if needed, and executes the binary via `spawn` with inherited stdio (`stdio: 'inherit'`).

## Platform Targets

All Go binaries are compiled statically with `CGO_ENABLED=0` and stripped via `-trimpath -ldflags "-s -w"`:

| Platform Target | Node OS | Node Arch | Go GOOS | Go GOARCH | Binary |
|---|---|---|---|---|---|
| linux-x64 | linux | x64 | linux | amd64 | `bin/mem` |
| linux-arm64 | linux | arm64 | linux | arm64 | `bin/mem` |
| darwin-x64 | darwin | x64 | darwin | amd64 | `bin/mem` |
| darwin-arm64 | darwin | arm64 | darwin | arm64 | `bin/mem` |
| win32-x64 | win32 | x64 | windows | amd64 | `bin/mem.exe` |
| win32-arm64 | win32 | arm64 | windows | arm64 | `bin/mem.exe` |

*Note on Linux glibc vs musl:* Because the Go binary is statically compiled without CGO (`CGO_ENABLED=0`), the binary runs identically across both glibc and musl (Alpine) distributions without requiring separate packages.

## Directory Structure

```
packaging/
  package-config.json         # Single configuration source (names, version, repo, targets)
  npm/
    main/                     # Main wrapper package source and templates
      bin/mem.js              # Node.js transparent wrapper shim
      package.json            # Base package.json template
      README.md               # User-facing package README
    platform/                 # Per-platform package template
      package.json
      README.md
  scripts/
    build.mjs                 # Cross-compilation and package assembly into packaging/dist/
    pack.mjs                  # Tarball generation and security auditing
    verify.mjs                # Verification and unit tests for mappings and layouts
    version.mjs               # Version inspection and bumping utility
  .github/workflows/
    release.yml               # GitHub Actions release pipeline template
  README.md                   # This documentation file
  REPORT.md                   # Detailed packaging evaluation report
```

## Configuration: `package-config.json`

All metadata is controlled in a single file: `packaging/package-config.json`:
- `scope`: npm scope (default placeholder: `@CHANGE-ME`).
- `name`: Package base name (`mem`).
- `bin`: Binary command name (`mem`).
- `version`: Version string injected into Go binaries and packages.
- `repository`: Source repository URL.
- `license`: Software license.
- `engines`: Supported Node.js engine versions (`>=18`).

**Safety Feature:** Any build with a placeholder (`CHANGE-ME`) or without the `--publish-ready` flag automatically has `"private": true` injected into all package manifests, preventing unintended publication to npm.

## Local Workflow

### 1. Build Packages
Cross-compile all 6 platforms and build package directories:
```bash
node packaging/scripts/build.mjs
```
To build in release-ready mode (omits `"private": true` if placeholders are replaced):
```bash
node packaging/scripts/build.mjs --publish-ready
```

### 2. Verify Packaging
Validate manifests, versions, platform mappings, and host execution:
```bash
node packaging/scripts/verify.mjs
```

### 3. Pack Tarballs
Run security auditing and generate `.tgz` archives in `packaging/dist/tarballs/`:
```bash
node packaging/scripts/pack.mjs
```

### 4. Version Management
Inspect or bump the project version:
```bash
node packaging/scripts/version.mjs                # Output current version
node packaging/scripts/version.mjs 0.2.0          # Set specific version
node packaging/scripts/version.mjs --bump minor   # Bump minor version
```

## Release Workflow & Actions

The GitHub Actions release workflow (`.github/workflows/release.yml`) triggers automatically on git tags matching `v*`:
1. Matrix runners across Ubuntu, macOS, and Windows test and compile the codebase.
2. Validates tests, builds packages, packs tarballs, and performs native installation smoke tests.
3. The publish stage signs and publishes platform packages followed by the primary wrapper to npm with provenance.
4. Generates a SHA256 checksum file and uploads raw binaries and tarballs to GitHub Releases.

## Required Owner Actions Before Publishing

1. **Configure npm Scope**:
   Create the organization / user scope on [npmjs.com](https://www.npmjs.com).
2. **Update `packaging/package-config.json`**:
   Replace `@CHANGE-ME` with the chosen npm scope/name and set the official repository URL.
3. **Repository Secret**:
   Add an `NPM_TOKEN` secret to the GitHub repository with publish permissions.
