#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const packagingDir = path.resolve(__dirname, "..");
const distDir = path.join(packagingDir, "dist");
const packagesDir = path.join(distDir, "packages");
const tarballsDir = path.join(distDir, "tarballs");

if (!fs.existsSync(packagesDir)) {
  console.error(
    `[pack] Error: Packages directory "${packagesDir}" not found. Run build.mjs first.`
  );
  process.exit(1);
}

fs.rmSync(tarballsDir, { recursive: true, force: true });
fs.mkdirSync(tarballsDir, { recursive: true });

const packageDirs = fs
  .readdirSync(packagesDir, { withFileTypes: true })
  .filter((entry) => entry.isDirectory())
  .map((entry) => entry.name);

if (packageDirs.length === 0) {
  console.error(`[pack] Error: No package directories found in "${packagesDir}".`);
  process.exit(1);
}

const disallowedPatterns = [
  /\.map$/i,
  /node_modules/i,
  /^\.env/i,
  /\.pem$/i,
  /\.key$/i,
  /\.secret/i,
];

const results = [];
let hasErrors = false;

console.log(`[pack] Packing ${packageDirs.length} packages into "${tarballsDir}"...\n`);

for (const dirName of packageDirs) {
  const pkgDir = path.join(packagesDir, dirName);
  const pkgJsonPath = path.join(pkgDir, "package.json");

  if (!fs.existsSync(pkgJsonPath)) {
    console.error(`[pack] Missing package.json in ${pkgDir}`);
    hasErrors = true;
    continue;
  }

  const pkgJson = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  console.log(`[pack] Inspecting ${pkgJson.name}...`);

  // Step 1: Run npm pack --dry-run --json to audit contents
  let dryRunData;
  try {
    const stdout = execFileSync(
      process.platform === "win32" ? "npm.cmd" : "npm",
      ["pack", "--dry-run", "--json"],
      {
        cwd: pkgDir,
        shell: process.platform === "win32",
        stdio: ["ignore", "pipe", "pipe"],
      }
    );
    dryRunData = JSON.parse(stdout.toString());
  } catch (err) {
    console.error(
      `[pack] npm pack --dry-run failed for ${pkgJson.name}:`,
      err.stderr ? err.stderr.toString() : err.message
    );
    hasErrors = true;
    continue;
  }

  const packSummary = Array.isArray(dryRunData) ? dryRunData[0] : dryRunData;
  const packagedFiles = (packSummary.files || []).map((f) => f.path);

  // Validate packaged files
  const suspiciousFiles = [];
  for (const file of packagedFiles) {
    for (const pattern of disallowedPatterns) {
      if (pattern.test(file)) {
        suspiciousFiles.push(`${file} (matches ${pattern})`);
      }
    }
  }

  if (suspiciousFiles.length > 0) {
    console.error(
      `[pack] Security check failed for ${pkgJson.name}. Disallowed files found:`
    );
    for (const f of suspiciousFiles) {
      console.error(`  - ${f}`);
    }
    hasErrors = true;
    continue;
  }

  // Step 2: Create the tarball
  try {
    const stdout = execFileSync(
      process.platform === "win32" ? "npm.cmd" : "npm",
      ["pack", "--pack-destination", tarballsDir],
      {
        cwd: pkgDir,
        shell: process.platform === "win32",
        stdio: ["ignore", "pipe", "pipe"],
      }
    );

    const tarballFileName = stdout.toString().trim().split(/\r?\n/).pop();
    const tarballPath = path.join(tarballsDir, tarballFileName);
    const stat = fs.statSync(tarballPath);

    results.push({
      pkgName: pkgJson.name,
      tarball: tarballFileName,
      fileCount: packagedFiles.length,
      files: packagedFiles,
      sizeBytes: stat.size,
      sizeKb: (stat.size / 1024).toFixed(1) + " KB",
    });
  } catch (err) {
    console.error(
      `[pack] Failed to pack ${pkgJson.name}:`,
      err.stderr ? err.stderr.toString() : err.message
    );
    hasErrors = true;
  }
}

if (hasErrors) {
  console.error("\n[pack] Packaging failed with errors.");
  process.exit(1);
}

console.log("\n================ TARBALL SUMMARY ================");
console.log("| Package | Tarball | Files | Size |");
console.log("|---|---|---|---|");
for (const r of results) {
  console.log(`| ${r.pkgName} | ${r.tarball} | ${r.fileCount} | ${r.sizeKb} |`);
}
console.log("=================================================");

console.log("\n[pack] All tarballs generated and verified successfully.");
