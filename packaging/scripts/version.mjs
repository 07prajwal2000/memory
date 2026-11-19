#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const packagingDir = path.resolve(__dirname, "..");
const configPath = path.join(packagingDir, "package-config.json");

if (!fs.existsSync(configPath)) {
  console.error(`[version] Missing config file: ${configPath}`);
  process.exit(1);
}

const config = JSON.parse(fs.readFileSync(configPath, "utf8"));
const args = process.argv.slice(2);

function isValidSemver(v) {
  return /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/.test(
    v
  );
}

function parseSemver(v) {
  const match = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/.exec(
    v
  );
  if (!match) return null;
  return {
    major: parseInt(match[1], 10),
    minor: parseInt(match[2], 10),
    patch: parseInt(match[3], 10),
    prerelease: match[4] || null,
    build: match[5] || null,
  };
}

if (args.length === 0) {
  console.log(config.version);
  process.exit(0);
}

let newVersion = null;

if (args[0] === "--bump") {
  const bumpType = args[1] || "patch";
  const parsed = parseSemver(config.version);
  if (!parsed) {
    console.error(`[version] Current version "${config.version}" is not valid semver.`);
    process.exit(1);
  }

  if (bumpType === "major") {
    newVersion = `${parsed.major + 1}.0.0`;
  } else if (bumpType === "minor") {
    newVersion = `${parsed.major}.${parsed.minor + 1}.0`;
  } else if (bumpType === "patch") {
    newVersion = `${parsed.major}.${parsed.minor}.${parsed.patch + 1}`;
  } else {
    console.error(`[version] Unknown bump type "${bumpType}". Use patch, minor, or major.`);
    process.exit(1);
  }
} else {
  let candidate = args[0];
  if (candidate === "--set" && args[1]) {
    candidate = args[1];
  }
  candidate = candidate.replace(/^v/, "");
  if (!isValidSemver(candidate)) {
    console.error(
      `[version] Error: "${candidate}" is not a valid semver version string (e.g. 1.2.3 or 1.2.3-alpha.1).`
    );
    process.exit(1);
  }
  newVersion = candidate;
}

const oldVersion = config.version;
config.version = newVersion;
fs.writeFileSync(configPath, JSON.stringify(config, null, 2) + "\n");

// Also keep template package.json files aligned if present
const mainPkgJsonPath = path.join(packagingDir, "npm", "main", "package.json");
if (fs.existsSync(mainPkgJsonPath)) {
  const mainPkg = JSON.parse(fs.readFileSync(mainPkgJsonPath, "utf8"));
  mainPkg.version = newVersion;
  fs.writeFileSync(mainPkgJsonPath, JSON.stringify(mainPkg, null, 2) + "\n");
}

const platformPkgJsonPath = path.join(packagingDir, "npm", "platform", "package.json");
if (fs.existsSync(platformPkgJsonPath)) {
  const platPkg = JSON.parse(fs.readFileSync(platformPkgJsonPath, "utf8"));
  platPkg.version = newVersion;
  fs.writeFileSync(platformPkgJsonPath, JSON.stringify(platPkg, null, 2) + "\n");
}

console.log(`[version] Updated version: ${oldVersion} -> ${newVersion}`);
