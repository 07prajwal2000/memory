#!/usr/bin/env node

import { execFileSync, execSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "..", "..");
const packagingDir = path.resolve(__dirname, "..");
const configPath = path.join(packagingDir, "package-config.json");

console.log("[release-check] Checking release readiness...\n");

let failures = 0;
function pass(msg) {
  console.log(`  [PASS] ${msg}`);
}
function fail(msg) {
  console.error(`  [FAIL] ${msg}`);
  failures++;
}

// 1. Config validation
if (!fs.existsSync(configPath)) {
  fail(`package-config.json not found at ${configPath}`);
  process.exit(1);
}
const config = JSON.parse(fs.readFileSync(configPath, "utf8"));
const version = config.version;

if (!version || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
  fail(`Invalid semver version in package-config.json: "${version}"`);
} else {
  pass(`package-config.json version is valid semver: ${version}`);
}

const args = process.argv.slice(2);
let expectedTag = `v${version}`;
if (args.length > 0) {
  const argVersion = args[0].replace(/^v/, "");
  if (argVersion !== version) {
    fail(`Specified version "${args[0]}" differs from package-config.json version "${version}"`);
  } else {
    pass(`Target version "${args[0]}" matches package-config.json version "${version}"`);
  }
  expectedTag = args[0].startsWith("v") ? args[0] : `v${args[0]}`;
}

// 2. Working tree cleanliness
try {
  const statusOut = execSync("git status --porcelain", {
    cwd: repoRoot,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();

  if (statusOut.length > 0) {
    fail(
      "Working tree is dirty (uncommitted changes detected). Stash or commit changes before releasing."
    );
  } else {
    pass("Working tree is clean");
  }
} catch (err) {
  fail(`Failed checking git status: ${err.message}`);
}

// 3. Tag existence check
try {
  const existingTags = execSync(`git tag -l "${expectedTag}"`, {
    cwd: repoRoot,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();

  if (existingTags.length > 0) {
    fail(`Git tag "${expectedTag}" already exists.`);
  } else {
    pass(`Git tag "${expectedTag}" does not exist yet`);
  }
} catch (err) {
  fail(`Failed checking git tag: ${err.message}`);
}

// 4. Branch reachability
try {
  const headCommit = execSync("git rev-parse HEAD", {
    cwd: repoRoot,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();

  // Check if main branch exists locally or remotely
  let mainRef = "main";
  try {
    execSync("git rev-parse --verify main", { cwd: repoRoot, stdio: "ignore" });
  } catch {
    try {
      execSync("git rev-parse --verify origin/main", { cwd: repoRoot, stdio: "ignore" });
      mainRef = "origin/main";
    } catch {
      mainRef = null;
    }
  }

  if (mainRef) {
    try {
      execSync(`git merge-base --is-ancestor HEAD ${mainRef}`, {
        cwd: repoRoot,
        stdio: "ignore",
      });
      pass(`HEAD (${headCommit.slice(0, 7)}) is reachable from ${mainRef}`);
    } catch {
      fail(`HEAD (${headCommit.slice(0, 7)}) is NOT reachable from ${mainRef}`);
    }
  } else {
    pass(`HEAD is at ${headCommit.slice(0, 7)} (main branch not found locally to test merge-base)`);
  }
} catch (err) {
  fail(`Failed checking git HEAD: ${err.message}`);
}

console.log("\n================ RELEASE COMMANDS ================");
console.log(`To release version ${version} (${expectedTag}):\n`);
console.log(`  git commit -am "release: ${expectedTag}"`);
console.log(`  git tag -a ${expectedTag} -m "${expectedTag}"`);
console.log(`  git push origin main`);
console.log(`  git push origin ${expectedTag}\n`);
console.log("==================================================");

if (failures === 0) {
  console.log("\n[release-check] All release checks passed!");
  process.exit(0);
} else {
  console.error(`\n[release-check] ${failures} check(s) failed. Resolve before releasing.`);
  process.exit(1);
}
