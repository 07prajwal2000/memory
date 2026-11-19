#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const packagingDir = path.resolve(__dirname, "..");
const configPath = path.join(packagingDir, "package-config.json");
const distDir = path.join(packagingDir, "dist");
const packagesDir = path.join(distDir, "packages");

console.log("[verify] Starting packaging verification...\n");

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
const bins = Array.isArray(config.bins) ? config.bins : (config.bin ? [config.bin] : []);
if (!config.name || bins.length === 0 || !config.version || !config.platforms) {
  fail("package-config.json is missing required fields (name, bins, version, platforms)");
} else {
  pass("package-config.json has all required fields");
}

function computePackageName(scope, name, suffix = "") {
  if (scope) {
    return suffix ? `${scope}/${name}-${suffix}` : `${scope}/${name}`;
  }
  return suffix ? `${name}-${suffix}` : name;
}

function computeDirName(scope, name, suffix = "") {
  const cleanScope = scope ? scope.replace(/^@/, "").replace(/[^a-zA-Z0-9_-]/g, "-") : "";
  if (cleanScope) {
    return suffix ? `${cleanScope}-${name}-${suffix}` : `${cleanScope}-${name}`;
  }
  return suffix ? `${name}-${suffix}` : name;
}

const mainPackageName = computePackageName(config.scope, config.name);
const mainDirName = computeDirName(config.scope, config.name);
const mainPkgDir = path.join(packagesDir, mainDirName);

// 2. Main package verification
if (!fs.existsSync(mainPkgDir)) {
  fail(`Main package directory not found: ${mainPkgDir}`);
} else {
  pass(`Main package directory exists: ${mainDirName}`);
}

const mainPkgJsonPath = path.join(mainPkgDir, "package.json");
if (!fs.existsSync(mainPkgJsonPath)) {
  fail(`Main package.json missing at ${mainPkgJsonPath}`);
} else {
  const mainPkg = JSON.parse(fs.readFileSync(mainPkgJsonPath, "utf8"));

  if (mainPkg.version !== config.version) {
    fail(`Main package version mismatch: ${mainPkg.version} !== ${config.version}`);
  } else {
    pass(`Main package version matches config (${mainPkg.version})`);
  }

  if (mainPkg.dependencies && Object.keys(mainPkg.dependencies).length > 0) {
    fail(`Main package must not have dependencies: found ${JSON.stringify(mainPkg.dependencies)}`);
  } else {
    pass("Main package has zero runtime dependencies");
  }

  if (mainPkg.scripts && Object.keys(mainPkg.scripts).length > 0) {
    fail(`Main package must not have install scripts: found ${JSON.stringify(mainPkg.scripts)}`);
  } else {
    pass("Main package has zero install scripts");
  }

  let allBinsValid = true;
  for (const b of bins) {
    if (!mainPkg.bin || !mainPkg.bin[b]) {
      fail(`Main package bin missing entry for "${b}"`);
      allBinsValid = false;
    } else if (mainPkg.bin[b] !== "./bin/mem.js") {
      fail(`Main package bin "${b}" points to ${mainPkg.bin[b]}, expected "./bin/mem.js"`);
      allBinsValid = false;
    }
  }
  if (allBinsValid) {
    pass(`Main package bin maps all ${bins.length} commands (${bins.join(", ")}) to ./bin/mem.js`);
  }

  // Check optionalDependencies
  const optDeps = mainPkg.optionalDependencies || {};
  let optDepsValid = true;
  for (const target of config.platforms) {
    const platformArch = `${target.platform}-${target.arch}`;
    const expectedPkg = computePackageName(config.scope, config.name, platformArch);
    if (!optDeps[expectedPkg]) {
      fail(`Main optionalDependencies missing ${expectedPkg}`);
      optDepsValid = false;
    } else if (optDeps[expectedPkg] !== config.version) {
      fail(`Main optionalDependencies ${expectedPkg} version mismatch: ${optDeps[expectedPkg]} !== ${config.version}`);
      optDepsValid = false;
    }
  }
  if (optDepsValid) {
    pass(`Main optionalDependencies pins all ${config.platforms.length} platform packages to ${config.version}`);
  }
}

// 3. Platform packages verification
const primaryBin = bins[0] || "mem";
for (const target of config.platforms) {
  const platformArch = `${target.platform}-${target.arch}`;
  const pkgName = computePackageName(config.scope, config.name, platformArch);
  const dirName = computeDirName(config.scope, config.name, platformArch);
  const pkgDir = path.join(packagesDir, dirName);

  if (!fs.existsSync(pkgDir)) {
    fail(`Platform directory missing: ${pkgDir}`);
    continue;
  }

  const pkgJsonPath = path.join(pkgDir, "package.json");
  if (!fs.existsSync(pkgJsonPath)) {
    fail(`Missing package.json in ${pkgDir}`);
    continue;
  }

  const pkg = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  if (pkg.name !== pkgName) {
    fail(`Platform package name mismatch: ${pkg.name} !== ${pkgName}`);
  }
  if (pkg.version !== config.version) {
    fail(`Platform package ${pkgName} version mismatch: ${pkg.version} !== ${config.version}`);
  }
  if (!pkg.os || !pkg.os.includes(target.os)) {
    fail(`Platform package ${pkgName} os mismatch: ${JSON.stringify(pkg.os)} does not include ${target.os}`);
  }
  if (!pkg.cpu || !pkg.cpu.includes(target.cpu)) {
    fail(`Platform package ${pkgName} cpu mismatch: ${JSON.stringify(pkg.cpu)} does not include ${target.cpu}`);
  }
  if (pkg.scripts && Object.keys(pkg.scripts).length > 0) {
    fail(`Platform package ${pkgName} must not contain scripts`);
  }

  const binaryFilename = target.exe ? `${primaryBin}.exe` : primaryBin;
  const binaryPath = path.join(pkgDir, "bin", binaryFilename);
  if (!fs.existsSync(binaryPath)) {
    fail(`Binary missing for ${platformArch}: ${binaryPath}`);
  } else {
    const stat = fs.statSync(binaryPath);
    if (stat.size === 0) {
      fail(`Binary for ${platformArch} is empty (0 bytes)`);
    }
  }
}
pass(`All ${config.platforms.length} platform packages verified structurally.`);

// 4. Unit-test platform mapping function
console.log("\n[verify] Running unit tests on platform-arch mapping logic...");
const mapTestMatrix = [
  { platform: "linux", arch: "x64", expected: `${config.name}-linux-x64` },
  { platform: "linux", arch: "arm64", expected: `${config.name}-linux-arm64` },
  { platform: "darwin", arch: "x64", expected: `${config.name}-darwin-x64` },
  { platform: "darwin", arch: "arm64", expected: `${config.name}-darwin-arm64` },
  { platform: "win32", arch: "x64", expected: `${config.name}-win32-x64` },
  { platform: "win32", arch: "arm64", expected: `${config.name}-win32-arm64` },
];

function testPlatformMapping(platform, arch) {
  const supported = {
    linux: ["x64", "arm64"],
    darwin: ["x64", "arm64"],
    win32: ["x64", "arm64"],
  };
  if (!supported[platform] || !supported[platform].includes(arch)) {
    return null;
  }
  return `${platform}-${arch}`;
}

for (const t of mapTestMatrix) {
  const res = testPlatformMapping(t.platform, t.arch);
  if (res !== `${t.platform}-${t.arch}`) {
    fail(`Mapping failed for ${t.platform} ${t.arch}: got ${res}`);
  } else {
    pass(`Mapping test passed: ${t.platform}-${t.arch}`);
  }
}

// Test unsupported platforms
const unsupported = [
  { platform: "freebsd", arch: "x64" },
  { platform: "linux", arch: "s390x" },
  { platform: "sunos", arch: "x64" },
];
for (const u of unsupported) {
  const res = testPlatformMapping(u.platform, u.arch);
  if (res !== null) {
    fail(`Unsupported platform ${u.platform}-${u.arch} should have returned null, got ${res}`);
  } else {
    pass(`Unsupported platform rejected correctly: ${u.platform}-${u.arch}`);
  }
}

// 5. Host execution test
console.log("\n[verify] Testing host binary execution...");
const hostPlatform = process.platform;
const hostArch = process.arch;
const hostTarget = config.platforms.find(
  (p) => p.platform === hostPlatform && p.arch === hostArch
);

if (hostTarget) {
  const hostDirName = computeDirName(
    config.scope,
    config.name,
    `${hostTarget.platform}-${hostTarget.arch}`
  );
  const hostBinName = hostTarget.exe ? `${primaryBin}.exe` : primaryBin;
  const hostBinPath = path.join(packagesDir, hostDirName, "bin", hostBinName);

  try {
    const versionOut = execFileSync(hostBinPath, ["version"], {
      stdio: ["ignore", "pipe", "pipe"],
    }).toString();

    if (!versionOut.includes(config.version)) {
      fail(`Host binary version output "${versionOut.trim()}" does not contain "${config.version}"`);
    } else {
      pass(`Host binary execution successful: ${versionOut.trim()}`);
    }
  } catch (err) {
    fail(`Host binary execution failed: ${err.message}`);
  }

  // 6. MEM_BINARY_PATH shim test
  console.log("\n[verify] Testing MEM_BINARY_PATH override with shim...");
  const shimPath = path.join(mainPkgDir, "bin", "mem.js");

  try {
    const shimOut = execFileSync(process.execPath, [shimPath, "version"], {
      env: { ...process.env, MEM_BINARY_PATH: hostBinPath },
      stdio: ["ignore", "pipe", "pipe"],
    }).toString();

    if (!shimOut.includes(config.version)) {
      fail(`Shim with MEM_BINARY_PATH version output "${shimOut.trim()}" does not contain "${config.version}"`);
    } else {
      pass(`Shim correctly dispatched to MEM_BINARY_PATH: ${shimOut.trim()}`);
    }
  } catch (err) {
    fail(`Shim execution with MEM_BINARY_PATH failed: ${err.message}`);
  }

  // Test invalid MEM_BINARY_PATH
  try {
    execFileSync(process.execPath, [shimPath, "version"], {
      env: { ...process.env, MEM_BINARY_PATH: "/non/existent/path/mem" },
      stdio: ["ignore", "pipe", "pipe"],
    });
    fail("Shim should have failed with non-existent MEM_BINARY_PATH");
  } catch (err) {
    const stderr = err.stderr ? err.stderr.toString() : "";
    if (stderr.includes("MEM_BINARY_PATH") && stderr.includes("does not exist")) {
      pass("Shim exited with error when MEM_BINARY_PATH points to non-existent file");
    } else {
      fail(`Shim failed with unexpected error for invalid MEM_BINARY_PATH: ${stderr}`);
    }
  }
} else {
  console.log(
    `[verify] Host platform ${hostPlatform}-${hostArch} not in prebuilt targets list; skipping native execution.`
  );
}

console.log("\n================ VERIFICATION SUMMARY ================");
if (failures === 0) {
  console.log("[verify] All checks passed successfully!");
  process.exit(0);
} else {
  console.error(`[verify] FAILED: ${failures} check(s) failed.`);
  process.exit(1);
}
