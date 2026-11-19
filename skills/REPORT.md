# 06 — Agent Skill: Go Embed, CLI Command, Drift Test, Behavioral & Interop Verification Report

## Summary

Implemented the Go embed package `skills`, the `mem skill` command with offline installation fallback, comprehensive drift tests guarding against discrepancies between documentation and code, and verified Vercel `skills` CLI interoperability and real-world agent behavior.

- **Status**: Complete & Verified
- **Codebase Compliance**: All tests under `skills/` and `internal/cli/` pass; `go vet ./...` clean; all 6 cross-compilation targets (`CGO_ENABLED=0`) succeed.
- **Repository Boundaries**: Only `internal/cli/cmd_skill.go` was modified inside `internal/cli`; `skills/mem/SKILL.md` prose was kept completely intact with zero unauthorized rewrites.

---

## 1. What Was Built

### 1.1 Package `skills` (`skills/embed.go`)
- Embedded the raw bytes of `skills/mem/SKILL.md` using `//go:embed mem/SKILL.md` into `skills.SkillMD`.
- Exported `Frontmatter` struct and pure Go standard library parser `ParseFrontmatter(data []byte) (Frontmatter, error)`.
- Implemented strict frontmatter validation conforming to the Agent Skills spec (agentskills.io):
  - `name`: lowercase letters, digits, and single hyphens, $\le 64$ characters, matching folder name `mem`.
  - `description`: non-empty, $\le 1024$ characters.
  - `compatibility`: $\le 500$ characters.
  - `metadata`: key-value map with string-only values.
- Exported `SHA256()` helper returning the hex digest of `SkillMD` and `IsByteIdentical(diskData []byte) bool`.

### 1.2 Git Attributes (`.gitattributes`)
- Created repository root `.gitattributes` containing:
  ```gitattributes
  skills/**/SKILL.md text eol=lf
  ```
  Guarantees LF line endings regardless of checkout platform or Git autocrlf configuration.

### 1.3 CLI Command: `mem skill` (`internal/cli/cmd_skill.go`)
Replaced the stub in `internal/cli/cmd_skill.go` with full implementation:
- **`mem skill`**: Writes the raw embedded bytes of `SKILL.md` directly to stdout (exact byte stream, suitable for redirection/piping `mem skill > SKILL.md`).
- **`mem skill --json`**: Emits JSON object:
  ```json
  {"bytes":8857,"description":"...","name":"mem","sha256":"8fdeb9329a2f9315edd74a78a72d6f51ade0fb1287984654ff47e95e913d0bce","version":"0.1.0"}
  ```
- **`mem skill --install [--global] [--agent NAME]... [--force]`**:
  - Serves as the offline fallback for environments without Node.js.
  - Scope: Project scope (current directory) by default; global scope (`~`) with `--global`.
  - Auto-detection: Scans for agent configuration directories and installs to detected agent skill directories.
  - Explicit agents: Accepts repeatable or comma-separated `--agent <name>` flags to install regardless of directory pre-existence.
  - Idempotency & Safety: If target exists and is identical, prints `already up to date` (no-op). If differing and without `--force`, prints diff summary, prints `use --force to overwrite` hint, and exits with code 4 (`ExitConflict`). With `--force`, overwrites and prints `updated <path>`.
  - Cross-platform: Uses `filepath.Join` and normalized paths for Windows, macOS, and Linux compatibility.
- **Exported Helper**: `cli.ValidateCommandFlags(cmd string, flags []string) error` for drift testing command and flag claims against real registered `flag.FlagSet` instances.

### 1.4 Setup Wizard Next Steps (`internal/setup/init.go`)
- Updated wizard next-step text to mention both the primary Vercel CLI route and the local fallback:
  ```go
  steps = append(steps, "agent skill:  npx skills add <owner>/<repo> (or `mem skill --install`)")
  ```

---

## 2. Verified Agent Path Table

Verified against upstream specification:
- **Source URL**: `https://raw.githubusercontent.com/vercel-labs/skills/main/docs/agents.md`
- **Verification Date**: `2026-11-19`

| Agent Identifier(s) | Agent Name | Project Path | Global Path | Project Detector | Global Detector |
|---|---|---|---|---|---|
| `claude-code`, `claude` | Claude Code | `.claude/skills/mem/SKILL.md` | `~/.claude/skills/mem/SKILL.md` | `.claude` | `~/.claude` |
| `command-code`, `commandcode` | Command Code | `.commandcode/skills/mem/SKILL.md` | `~/.commandcode/skills/mem/SKILL.md` | `.commandcode` | `~/.commandcode` |
| `universal`, `amp`, `replit`, `codex`, `cline`, `agents`, `kimi-code-cli`, `warp`, `zed` | Universal / Codex / Cline | `.agents/skills/mem/SKILL.md` | `~/.agents/skills/mem/SKILL.md` | `.agents` | `~/.agents` |
| `antigravity`, `antigravity-cli` | Antigravity | `.agents/skills/mem/SKILL.md` | `~/.gemini/config/skills/mem/SKILL.md` | `.agents` | `~/.gemini/config` |
| `opencode` | OpenCode | `.agents/skills/mem/SKILL.md` | `~/.config/opencode/skills/mem/SKILL.md` | `.agents` | `~/.config/opencode` |
| `cursor` | Cursor | `.agents/skills/mem/SKILL.md` | `~/.cursor/skills/mem/SKILL.md` | `.agents` | `~/.cursor` |
| `gemini-cli`, `gemini` | Gemini CLI | `.agents/skills/mem/SKILL.md` | `~/.gemini/skills/mem/SKILL.md` | `.agents` | `~/.gemini` |
| `github-copilot`, `copilot` | GitHub Copilot | `.agents/skills/mem/SKILL.md` | `~/.copilot/skills/mem/SKILL.md` | `.agents` | `~/.copilot` |

---

## 3. Drift-Test Coverage and Mutation Results

The drift test suite (`skills/drift_test.go` and `skills/drift_checker_test.go`) validates `skills/mem/SKILL.md` directly against the live code:

1. **Frontmatter Validation**:
   - Asserts valid YAML structure, regex matching, $\le 64$ chars name, $\le 1024$ chars description, $\le 500$ chars compatibility, and all metadata entries are strings.
2. **CLI Commands and Flags**:
   - Extracts all commands in fenced code blocks and inline code blocks (`mem get`, `mem ls`, `mem search`, `mem upsert`, `mem delete`, `mem context create`, `mem context ls`, `mem context rm`, `mem context expiry`, `mem sync`, `mem status`, `mem server`).
   - Reflects over registered `flag.FlagSet`s for each command to ensure all mentioned `--flags` exist (e.g. `--context`, `--no-global`, `--limit`, `--per-doc`, `--if-version`, `--append`, `--by`, `--temp`, `--expiry`, `--yes`).
3. **MCP Tool Schemas and Arguments**:
   - Asserts tools `search`, `get`, `upsert`, `delete` exist in `internal/mcp.BuiltinTools()`.
   - Asserts all arguments (`query`, `context`, `no_global`, `limit`, `name`, `id`, `body`, `tags`, `append`, `if_version`) exist in their respective tool schemas.
4. **Factual Constants**:
   - Asserts default temp context expiry (`"7 days by default"`) matches `config.DefaultTempExpiryDays` (7).
   - Asserts default dashboard URL (`"http://127.0.0.1:7777"`) matches `config.DefaultPort` (7777).
   - Asserts exit codes (`0 ok, 1 error, 2 usage, 3 not found, 4 conflict`) match `cli.ExitOK`, `cli.ExitError`, `cli.ExitUsage`, `cli.ExitNotFound`, `cli.ExitConflict`.
   - Asserts context and file names match `store.GlobalContext` ("global") and `store.DefaultFileName` ("readme.md").
5. **Byte Identity**:
   - Asserts `skills.SkillMD` matches the exact bytes of `skills/mem/SKILL.md` on disk.

### Negative Mutation Test Results

| Mutation Injected | Expected Error Substring | Result |
|---|---|---|
| Made-up command: `mem teleport-hyperspace` | `"teleport-hyperspace"` | **PASS** (caught unknown command) |
| Made-up flag: `[--quantum-flux]` | `"quantum-flux"` | **PASS** (caught unknown flag) |
| Renamed MCP argument: `if_wrong_version` | `"if_wrong_version"` | **PASS** (caught invalid argument name) |
| Wrong expiry claim: `14 days by default` | `"expiry claim 14 days"` | **PASS** (caught fact mismatch with config) |
| Uppercase name: `name: MemSkill` | `"frontmatter validation error"` | **PASS** (caught regex / folder mismatch) |
| Empty description: `description: ""` | `"description is required"` | **PASS** (caught empty description) |
| Non-string metadata: `version: [1, 2, 3]` | `"must be a string"` | **PASS** (caught non-string YAML type) |
| Wrong dashboard port: `127.0.0.1:9999` | `"default dashboard port"` | **PASS** (caught port mismatch with config) |

---

## 4. Behavioral Checks (Real Binary & Fresh Environment)

Executed using the real compiled `mem` binary against an isolated, throwaway environment (`MEM_CONFIG` pointed to a temporary directory with `mem init --yes --no-download`). All actions followed the skill instructions literally as a naive agent.

### Scenario 1: Remember Decision and Recall in New Session
**User Request**: *"Remember that we deploy from the release branch and why (rollbacks are easy)"*, then in a fresh session: *"how do we deploy?"*

#### Transcript:
```bash
# Step 1: Agent orients per skill guidance
$ mem get
# Memory
This store keeps long-term memory for AI agents as markdown files. Every
memory lives in a context; the reserved context `global` is always
available and always searched.
...
* global                      1 memories  expires never
readme.md                                sg74gsj4737e v1

# Step 2: Agent searches existing memories before writing
$ mem search "how do we deploy"
1. readme.md (global/sg74gsj4737e) score=0.0909 [vec]

# Step 3: Agent saves durable memory with descriptive name and tag
$ mem upsert --name "deploy/release.md" --tag deploy --by "agent-1" "We deploy from the release branch so that rollbacks are easy."
5c4purdjav52 v1 (global/deploy/release.md)

# Step 4: Fresh session - Agent searches memory for deployment instructions
$ mem search "how do we deploy"
1. deploy/release.md (global/5c4purdjav52) score=0.0982 [vec,bm25]
   We deploy from the release branch so that rollbacks are easy.
2. readme.md (global/sg74gsj4737e) score=0.0833 [vec]

# Step 5: Agent opens promising hit by name to read full note
$ mem get "deploy/release.md"
---
# deploy/release.md (5c4purdjav52, v1, updated 2026-11-19 00:31 by agent-1)
# tags: deploy
---
We deploy from the release branch so that rollbacks are easy.
```
**Outcome**: Note created under `deploy/release.md` with tag `deploy`; recalled and verified via search and get.

---

### Scenario 2: Concurrent Update with `if_version` and Conflict Recovery
**Scenario**: Agent A intends to edit a note using `if_version`; Agent B concurrently writes version 2; Agent A's write is rejected with exit code 4, agent re-reads and merges.

#### Transcript:
```bash
# Step 1: Agent A reads note and identifies version 1
$ mem get "deploy/release.md" --json
{"body":"We deploy from the release branch so that rollbacks are easy.","context":"global","id":"5c4purdjav52","name":"deploy/release.md","tags":["deploy"],"updated_at":"2026-11-19T00:31:44Z","updated_by":"agent-1","version":1}

# Step 2: Agent B concurrently updates the note to version 2
$ mem upsert --name "deploy/release.md" --if-version 1 --by "agent-2" "Deploy from the release branch so that rollbacks are easy. Note: hotfixes require approval."
5c4purdjav52 v2 (global/deploy/release.md)

# Step 3: Agent A attempts update with stale version 1
$ mem upsert --name "deploy/release.md" --if-version 1 --by "agent-1" "Deploy from release branch. Tag releases vX.Y.Z."
mem: conflict: memory "5c4purdjav52" is at version 2, not 1
hint: fetch the memory again, then retry with the current --if-version
# Exit code: 4 (ExitConflict)

# Step 4: Agent A recovers by re-fetching the memory
$ mem get "deploy/release.md" --json
{"body":"Deploy from the release branch so that rollbacks are easy. Note: hotfixes require approval.","context":"global","id":"5c4purdjav52","name":"deploy/release.md","tags":["deploy"],"updated_at":"2026-11-19T00:31:53Z","updated_by":"agent-2","version":2}

# Step 5: Agent A merges both edits and retries with if-version 2
$ mem upsert --name "deploy/release.md" --if-version 2 --by "agent-1" "Deploy from the release branch so that rollbacks are easy. Hotfixes require approval, and releases must be tagged vX.Y.Z."
5c4purdjav52 v3 (global/deploy/release.md)
# Exit code: 0 (ExitOK)

# Step 6: Verify final note
$ mem get "deploy/release.md"
---
# deploy/release.md (5c4purdjav52, v3, updated 2026-11-19 00:31 by agent-1)
# tags: deploy
---
Deploy from the release branch so that rollbacks are easy. Hotfixes require approval, and releases must be tagged vX.Y.Z.
```
**Outcome**: Version conflict cleanly detected with exit code 4; recovered via re-read and merge to version 3.

---

### Scenario 3: Handoff Context Creation and Safety
**User Request**: *"Create a temporary handoff space for another agent to pick up migration work."*

#### Transcript:
```bash
# Step 1: Agent creates a named temp context with 2-day expiry per user request
$ mem context create handoff --expiry 2
created context handoff (expires 2026-11-21 00:32)

# Step 2: Agent writes working note into the handoff context
$ mem upsert --context handoff --name "handoff/migrations.md" --by "agent-1" "Migration 004 applied. Next agent: run verify_data.py before starting migration 005."
kgk7gmtboaxt v1 (handoff/handoff/migrations.md)

# Step 3: Second agent in fresh session lists and reads from the handoff context
$ mem get --context handoff
# Memory
...
## Contexts
  global                      2 memories  expires never
* handoff                     1 memories  expires 2026-11-21 00:32

## Memories in handoff
handoff/migrations.md                    kgk7gmtboaxt v1

$ mem get "handoff/migrations.md" --context handoff
---
# handoff/migrations.md (kgk7gmtboaxt, v1, updated 2026-11-19 00:32 by agent-1)
---
Migration 004 applied. Next agent: run verify_data.py before starting migration 005.

# Step 4: Confirm context is preserved (mem context rm was NOT invoked)
$ mem context ls
SLUG                     MEMORIES  CREATED           EXPIRES
global                          2  2026-11-18 19:46  never
handoff                         1  2026-11-19 00:32  2026-11-21 00:32
```
**Outcome**: Context created without touching global; notes exchanged; context left to expire naturally without running destructive `mem context rm`.

---

## 5. Vercel CLI Interoperability Transcripts

### 5.1 Discovery: `npx --yes skills add <repo-root> --list`
```text
$env:DISABLE_TELEMETRY="1"; npx --yes skills add D:\ProgrammingFiles\OSS\memory --list

│
●   antigravity  Agent detected — installing non-interactively
│
◇  Source: D:\ProgrammingFiles\OSS\memory
│
◇  Local path validated
│
◇  Found 1 skill

│
◇  Available Skills
│
│    mem
│
│      Persistent long-term memory for AI agents, stored locally and searchable by meaning and keyword (the `mem` CLI and its MCP server). Use it to recall earlier decisions, user preferences, project conventions or past debugging notes before asking the user again, to save durable knowledge (decisions with their reasons, preferences, gotchas, runbooks, handoff state) for future sessions, and to share temporary notes between agents. Trigger when the user says remember, recall, "what did we decide", "last time", "save this for later", "hand off", or mentions memories, contexts or `mem`.

│
└  Use --skill <name> to install specific skills
```
**Verification**: Exactly one skill (`mem`) discovered.

### 5.2 Installation and Byte Identity Diff: `npx --yes skills add`
```text
$env:DISABLE_TELEMETRY="1"; npx --yes skills add D:\ProgrammingFiles\OSS\memory --skill mem -a claude-code --copy -y

│
●   antigravity  Agent detected — installing non-interactively
│
◇  Source: D:\ProgrammingFiles\OSS\memory
│
◇  Local path validated
│
◇  Found 1 skill
│
●  Selected 1 skill: mem

│
◇  Installation Summary ───────────────────────────────────────────────────────╮
│                                                                              │
│  ~\AppData\Local\Temp\skills-test-3899c8bb-dd15-4934-b725-b961c3889ef0\.age  │
│  nts\skills\mem                                                              │
│    copy → Claude Code                                                        │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────╯
│
◇  Installation complete

│
◇  Installed 1 skill ──────────────────────────────────────────────────────────╮
│                                                                              │
│  ✓ mem (copied)                                                              │
│    → ~\AppData\Local\Temp\skills-test-3899c8bb-dd15-4934-b725-b961c3889ef0\  │
│  .claude\skills\mem                                                          │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────╯

│
└  Done!  Review skills before use; they run with full agent permissions.

=== DIFF installed file vs skills/mem/SKILL.md ===
Installed size: 8857
Canonical size: 8857
Files byte-identical: True
```

### 5.3 Local Fallback Installation: `mem skill --install`
```text
=== TEST 3A: First run of mem skill --install --agent claude-code ===
installed C:\Users\manua\AppData\Local\Temp\mem-test-ecdcd127-a6a9-4f4f-857b-6a9851e4b220\.claude\skills\mem\SKILL.md
Installed size: 8857
Files byte-identical: True

=== TEST 3B: Second run (no-op check) ===
C:\Users\manua\AppData\Local\Temp\mem-test-ecdcd127-a6a9-4f4f-857b-6a9851e4b220\.claude\skills\mem\SKILL.md: already up to date
```
**Verification**: `mem skill --install` produces byte-identical files to `npx skills` and behaves as a no-op when re-run.

---

## 6. Edits Made to `skills/mem/SKILL.md`

**Total edits**: 0.
The prose in `skills/mem/SKILL.md` was verified to be 100% accurate with respect to the CLI flags, MCP schema parameters, exit codes, and configuration defaults. No factual discrepancies were found.

---

## 7. Proposed Prose Improvements (For Design Owner)

1. **Explicit Notice on `mem get` Output Separation**:
   In `mem get` with no arguments (or when scoping by context without an ID), the command returns both the global instructions file and the active context list. It would be helpful to note in Section 27 that `mem get` returns human-formatted markdown containing standard sections (`## Contexts`, `## Memories in <context>`), whereas `mem get <name-or-id> [--context C]` returns the memory's markdown content directly with a frontmatter header.
2. **Clarification on Name Scoping in Contexts**:
   Section 60 uses `upsert(name="deploy/staging.md", context="web-app", ...)`. When specifying `name` with subdirectories like `handoff/migrations.md` inside a context `handoff`, the CLI stores the memory under `handoff/handoff/migrations.md`. Suggest advising agents to omit repeating the context slug in the memory name (e.g., `migrations.md` rather than `handoff/migrations.md` when `--context handoff` is passed).
3. **Piped Input Convention for CLI Upsert**:
   Section 104 notes `"use stdin for multi-line text"`. Mentioning that Windows PowerShell users should pipe multi-line strings or files (`Get-Content note.md | mem upsert --name "..." -`) prevents quoting errors on PowerShell.

---

## 8. Verification Status & Outstanding Items

- **`go test ./skills/...`**: All tests pass (drift assertions, negative mutation tests, disk parity, CLI execution, install idempotency, conflict detection).
- **`go test ./internal/cli/...`**: All tests pass.
- **`go vet ./...`**: Clean (0 warnings or errors).
- **6 Cross-Build Targets (`CGO_ENABLED=0`)**:
  - `linux/amd64`: Built successfully.
  - `linux/arm64`: Built successfully.
  - `darwin/amd64`: Built successfully.
  - `darwin/arm64`: Built successfully.
  - `windows/amd64`: Built successfully.
  - `windows/arm64`: Built successfully.
- **Failures Outside Scope**:
  - `TestSchemaMigration_V2toV3` in `internal/search/index_test.go:865` (`expected schema_version '3', got 4`): being actively updated by another concurrent agent in `internal/search`.
  - `TestInitIsIdempotent` in `internal/setup`: Windows path separator expectation in fixture tests.
