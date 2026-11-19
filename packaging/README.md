# Packaging and Distribution Pipeline for `agent-memory`

This directory manages multi-platform compilation, npm packaging, and release automation for `agent-memory` (CLI commands: `mem`, `memory`, `agent-memory`).

## Overview

The distribution uses an optionalDependencies architecture:
- A primary wrapper package: `@07prajwal2000/agent-memory` containing the Node.js wrapper shim (`bin/mem.js`).
- Maps three equivalent command aliases: `mem`, `memory`, and `agent-memory` in `package.json` `bin`.
- Precompiled standalone platform binaries packaged individually into separate scoped packages: `@07prajwal2000/agent-memory-<platform>-<arch>`.
- Platform packages are declared as `optionalDependencies` of the primary package, pinned to the exact version.
- When running `npx @07prajwal2000/agent-memory` or `npm i -g @07prajwal2000/agent-memory`, the package manager installs the main wrapper and only the matching native package for the host operating system and CPU architecture.
- The wrapper shim locates the platform binary with `require.resolve`, ensures execution permissions, and spawns the Go executable with inherited stdio (`stdio: 'inherit'`).

## Platform Targets

All Go binaries are compiled statically with `CGO_ENABLED=0` and stripped via `-trimpath -ldflags "-s -w"`:

| Platform Target | Package Name | Go OS | Go Arch | Binary |
|---|---|---|---|---|
| linux-x64 | `@07prajwal2000/agent-memory-linux-x64` | linux | amd64 | `bin/mem` |
| linux-arm64 | `@07prajwal2000/agent-memory-linux-arm64` | linux | arm64 | `bin/mem` |
| darwin-x64 | `@07prajwal2000/agent-memory-darwin-x64` | darwin | amd64 | `bin/mem` |
| darwin-arm64 | `@07prajwal2000/agent-memory-darwin-arm64` | darwin | arm64 | `bin/mem` |
| win32-x64 | `@07prajwal2000/agent-memory-win32-x64` | windows | amd64 | `bin/mem.exe` |
| win32-arm64 | `@07prajwal2000/agent-memory-win32-arm64` | windows | arm64 | `bin/mem.exe` |

*Static binary architecture:* Compiled with `CGO_ENABLED=0`, Linux binaries are fully static ELF executables compatible with both glibc and musl (Alpine).

## Directory Structure

```
packaging/
  package-config.json         # Single configuration source (names, bins, version, repo, targets)
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
    version.mjs               # Version inspection, exact setting, and bumping utility
    release-check.mjs         # Pre-release validation (tag vs config, clean tree, reachability)
  README.md                   # This documentation file
.github/workflows/
  release.yml                 # Root GitHub Actions tag-driven release workflow
```

## Configuration: `package-config.json`

Metadata is controlled in `packaging/package-config.json`:
- `scope`: `@07prajwal2000`
- `name`: `agent-memory`
- `bins`: `["mem", "memory", "agent-memory"]`
- `version`: Version string injected into Go binaries and packages (`0.1.0`).
- `repository`: `https://github.com/07prajwal2000/memory`
- `license`: `MIT`
- `author`: `07prajwal2000`
- `engines`: Supported Node.js versions (`>=18`).

**Safety Guard:** Building without the explicit `--publish-ready` flag automatically marks all packages with `"private": true`, preventing unintended publication to npm.

## Local Workflow

### 1. Build Packages
Cross-compile all 6 platform binaries and assemble packages:
```bash
node packaging/scripts/build.mjs
```
To build in release-ready mode (omits `"private": true`):
```bash
node packaging/scripts/build.mjs --publish-ready
```

### 2. Verify Packaging
Validate manifests, versions, platform mappings, host execution, and shim fallback:
```bash
node packaging/scripts/verify.mjs
```

### 3. Pack Tarballs
Run security auditing and generate `.tgz` archives in `packaging/dist/tarballs/`:
```bash
node packaging/scripts/pack.mjs
```

### 4. Version Management
Inspect, set, or bump the project version:
```bash
node packaging/scripts/version.mjs                # Output current version
node packaging/scripts/version.mjs 0.2.0          # Set exact version across configs
node packaging/scripts/version.mjs --bump minor   # Bump minor version
```

### 5. Pre-Release Validation
Validate local readiness before tagging:
```bash
node packaging/scripts/release-check.mjs
```

---

## Release Pipeline & Publishing Flow

Release automation is strictly **tag-driven** via [.github/workflows/release.yml](file:///D:/ProgrammingFiles/OSS/memory/.github/workflows/release.yml).

### Workflow Architecture

1. **Trigger**:
   - Push of git tags matching `v[0-9]+.[0-9]+.[0-9]+*`.
   - Manual `workflow_dispatch`: executes build, test, verification, and smoke tests, but never releases or publishes.

2. **Job: `guard`**:
   - Derives `VERSION` by stripping leading `v`.
   - Fails if `VERSION` differs from `packaging/package-config.json` version.
   - Fails if Go binary injection (`-ldflags -X memory/internal/cli.Version=...`) outputs a differing version.
   - Fails if the tag commit is not reachable from `main`.
   - Detects prereleases (`is_prerelease=true` if `VERSION` contains a hyphen `-`).

3. **Job: `test-and-build`**:
   - Multi-OS matrix (`ubuntu-latest`, `macos-latest`, `windows-latest`).
   - Runs Go and frontend tests, builds all targets via `build.mjs --publish-ready`, verifies mappings, packs tarballs, and performs smoke test installations.
   - Uploads tarballs and compiled packages as artifacts.

4. **Job: `release`**:
   - Requires `guard` and `test-and-build` to pass.
   - Permissions: `contents: write` only.
   - Idempotently creates or updates the GitHub Release for the tag with generated release notes.
   - Attaches:
     - All 7 npm tarballs (6 platform `.tgz` + primary `07prajwal2000-agent-memory-<version>.tgz`).
     - Standalone raw executables for all 6 targets named:
       `mem_<version>_<os>_<arch>[.exe]` (e.g. `mem_0.1.0_linux_x64`, `mem_0.1.0_win32_x64.exe`).
     - `SHA256SUMS` checksum manifest.

5. **Job: `publish` (npm Trusted Publishing / OIDC)**:
   - Separate job requiring `release` to succeed.
   - Permissions: `id-token: write` and `contents: read` only (no secrets needed).
   - Runs on `node-version: 24` (with npm >= 11.5.1).
   - Gated by repository variable: runs ONLY when repository variable `PUBLISH_NPM == 'true'`. If not `'true'`, it logs a notice and succeeds without publishing.
   - Uses standard `npm publish <tgz> --access public` (npm automatically generates provenance via OIDC).
   - Skips packages that already exist on the npm registry (`npm view`).
   - Publishes platform packages first, and the primary wrapper package last.
   - *Note on Legacy Tokens:* The old `NPM_TOKEN` secret path is intentionally not supported.

---

## Manual Publishing Flow (Initial Release with 2FA)

Because npm Trusted Publishing requires a package to already exist on the registry before configuring GitHub Actions OIDC, the initial version of each package is published manually using 2FA:

1. **Bump Version**:
   ```bash
   node packaging/scripts/version.mjs 0.1.0
   ```

2. **Run Pre-Release Check**:
   ```bash
   node packaging/scripts/release-check.mjs
   ```

3. **Commit and Tag**:
   ```bash
   git commit -am "release: v0.1.0"
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin main
   git push origin v0.1.0
   ```

4. **Wait for GitHub Release**:
   Wait for the GitHub Actions workflow to finish creating the GitHub Release.

5. **Download Tarballs**:
   Download the 7 `.tgz` files from the GitHub Release assets.

6. **Publish via npm CLI with 2FA**:
   Publish each platform package first, then the main package (enter your 2FA OTP for each prompt):
   ```bash
   npm publish 07prajwal2000-agent-memory-linux-x64-0.1.0.tgz --access public
   npm publish 07prajwal2000-agent-memory-linux-arm64-0.1.0.tgz --access public
   npm publish 07prajwal2000-agent-memory-darwin-x64-0.1.0.tgz --access public
   npm publish 07prajwal2000-agent-memory-darwin-arm64-0.1.0.tgz --access public
   npm publish 07prajwal2000-agent-memory-win32-x64-0.1.0.tgz --access public
   npm publish 07prajwal2000-agent-memory-win32-arm64-0.1.0.tgz --access public
   npm publish 07prajwal2000-agent-memory-0.1.0.tgz --access public
   ```

---

## One-Time Setup: npm Trusted Publishing (OIDC)

After the initial manual publish, configure automated tag-driven publishing:

1. Log into [npmjs.com](https://www.npmjs.com).
2. For **EACH of the 7 packages** (`@07prajwal2000/agent-memory` and the 6 `@07prajwal2000/agent-memory-<platform>-<arch>` platform packages):
   - Navigate to package **Settings** > **Publishing Access** > **Trusted Publishers**.
   - Select **GitHub Actions**.
   - Enter:
     - **Owner**: `07prajwal2000`
     - **Repository**: `memory`
     - **Workflow filename**: `release.yml`
     - **Environment**: *(leave blank unless using a GitHub environment)*
3. In GitHub repository settings (`https://github.com/07prajwal2000/memory/settings/variables/actions`):
   - Add Repository Variable: `PUBLISH_NPM` with value `true`.
4. From this point forward, pushing any tag matching `v*` on `main` will build, release, and publish all packages automatically through GitHub Actions OIDC without storing any npm tokens.

---

## Installation & Usage

Install globally:
```bash
npm i -g @07prajwal2000/agent-memory
mem init
```

One-off execution:
```bash
npx @07prajwal2000/agent-memory init
```

The commands `mem`, `memory`, and `agent-memory` are equivalent aliases.

> **Note on global install conflicts:** A global install may fail with `EEXIST` if another global package already owns one of those names. Use `npm i -g --force @07prajwal2000/agent-memory` or local `npx` to avoid conflicts.

### Model Context Protocol (MCP) Setup

- **Global:**
  ```bash
  claude mcp add mem -- mem mcp
  ```
- **npx:**
  ```bash
  claude mcp add mem -- npx -y @07prajwal2000/agent-memory mcp
  ```

Generic JSON configuration:
```json
{
  "mcpServers": {
    "mem": {
      "command": "mem",
      "args": ["mcp"]
    }
  }
}
```
or via npx:
```json
{
  "mcpServers": {
    "mem": {
      "command": "npx",
      "args": ["-y", "@07prajwal2000/agent-memory", "mcp"]
    }
  }
}
```

### Agent Skill
```bash
npx skills add 07prajwal2000/memory
```

### Web Dashboard
```bash
mem server
```

### Data Storage
All data is stored locally in `~/.mem` (override with `--data-dir` or `MEM_DATA_DIR`).
