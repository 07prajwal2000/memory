#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "..", "..");
const packagingDir = path.resolve(__dirname, "..");
const configPath = path.join(packagingDir, "package-config.json");

if (!fs.existsSync(configPath)) {
  console.error(`[build] Missing configuration file: ${configPath}`);
  process.exit(1);
}

const config = JSON.parse(fs.readFileSync(configPath, "utf8"));
const publishReady = process.argv.includes("--publish-ready");

const isPlaceholder =
  (config.scope && config.scope.includes("CHANGE-ME")) ||
  (config.repository && config.repository.includes("CHANGE-ME")) ||
  (config.author && config.author.includes("CHANGE-ME"));

const isPrivate = !publishReady || isPlaceholder;

if (isPrivate) {
  console.log(
    `[build] Note: Building with "private": true (publish-ready=${publishReady}, placeholder=${isPlaceholder})`
  );
}

const webEmbedPath = path.join(repoRoot, "web", "embed.go");
if (!fs.existsSync(webEmbedPath)) {
  console.warn(
    "[build] Warning: web/embed.go not found. The web dashboard will not be embedded in this build."
  );
} else {
  console.log("[build] web/embed.go detected. Embedding dashboard assets.");
}

const distDir = path.join(packagingDir, "dist");
const packagesDir = path.join(distDir, "packages");

fs.rmSync(distDir, { recursive: true, force: true });
fs.mkdirSync(packagesDir, { recursive: true });

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

const sizeReports = [];

console.log(`[build] Starting build for version ${config.version}...`);

const optionalDependencies = {};

// 1. Build and assemble each platform package
for (const target of config.platforms) {
  const platformArch = `${target.platform}-${target.arch}`;
  const pkgName = computePackageName(config.scope, config.name, platformArch);
  const dirName = computeDirName(config.scope, config.name, platformArch);
  const pkgDir = path.join(packagesDir, dirName);
  const binDir = path.join(pkgDir, "bin");

  fs.mkdirSync(binDir, { recursive: true });

  optionalDependencies[pkgName] = config.version;

  const binaryFilename = target.exe ? `${config.bin}.exe` : config.bin;
  const binaryDest = path.join(binDir, binaryFilename);

  console.log(
    `[build] Compiling ${target.goos}/${target.goarch} -> ${binaryFilename}...`
  );

  const env = {
    ...process.env,
    CGO_ENABLED: "0",
    GOOS: target.goos,
    GOARCH: target.goarch,
  };

  const ldflags = `-s -w -X memory/internal/cli.Version=${config.version}`;

  try {
    execFileSync(
      "go",
      ["build", "-trimpath", "-ldflags", ldflags, "-o", binaryDest, "./cmd/mem"],
      {
        cwd: repoRoot,
        env,
        stdio: "pipe",
      }
    );
  } catch (err) {
    console.error(
      `[build] Failed compiling for ${target.goos}/${target.goarch}:`,
      err.stderr ? err.stderr.toString() : err.message
    );
    process.exit(1);
  }

  // Ensure file executable permission
  if (!target.exe) {
    try {
      fs.chmodSync(binaryDest, 0o755);
    } catch {
      // Ignore on systems that do not support posix permissions
    }
  }

  const stat = fs.statSync(binaryDest);
  const sizeMb = (stat.size / (1024 * 1024)).toFixed(2);
  sizeReports.push({
    target: platformArch,
    package: pkgName,
    binary: binaryFilename,
    bytes: stat.size,
    sizeMb: `${sizeMb} MB`,
  });

  // Generate platform package.json
  const platformPkgJson = {
    name: pkgName,
    version: config.version,
    description: `Prebuilt native binary for ${config.name} (${platformArch})`,
    os: [target.os],
    cpu: [target.cpu],
    files: ["bin/", "README.md", "LICENSE"],
    license: config.license,
    repository: {
      type: "git",
      url: config.repository,
    },
  };

  if (isPrivate) {
    platformPkgJson.private = true;
  }

  fs.writeFileSync(
    path.join(pkgDir, "package.json"),
    JSON.stringify(platformPkgJson, null, 2) + "\n"
  );

  // Platform README & LICENSE
  const platformReadme = `# ${pkgName}

Standalone binary asset for ${config.name} on ${platformArch}.
`;
  fs.writeFileSync(path.join(pkgDir, "README.md"), platformReadme);

  const rootLicense = path.join(repoRoot, "LICENSE");
  if (fs.existsSync(rootLicense)) {
    fs.copyFileSync(rootLicense, path.join(pkgDir, "LICENSE"));
  } else {
    fs.writeFileSync(path.join(pkgDir, "LICENSE"), `${config.license} License\n`);
  }
}

// 2. Assemble the main package
console.log(`[build] Assembling primary package: ${mainPackageName}...`);
fs.mkdirSync(path.join(mainPkgDir, "bin"), { recursive: true });

const shimSrc = path.join(packagingDir, "npm", "main", "bin", "mem.js");
const shimDest = path.join(mainPkgDir, "bin", `${config.bin}.js`);

fs.copyFileSync(shimSrc, shimDest);
try {
  fs.chmodSync(shimDest, 0o755);
} catch {
  // Ignore permission adjustments on Windows
}

const sortedOptionalDeps = {};
for (const key of Object.keys(optionalDependencies).sort()) {
  sortedOptionalDeps[key] = optionalDependencies[key];
}

const mainPkgJson = {
  name: mainPackageName,
  version: config.version,
  description: config.description,
  bin: {
    [config.bin]: `./bin/${config.bin}.js`,
  },
  files: ["bin/", "README.md", "LICENSE"],
  engines: config.engines,
  license: config.license,
  repository: {
    type: "git",
    url: config.repository,
  },
  keywords: config.keywords,
  optionalDependencies: sortedOptionalDeps,
};

if (isPrivate) {
  mainPkgJson.private = true;
}

fs.writeFileSync(
  path.join(mainPkgDir, "package.json"),
  JSON.stringify(mainPkgJson, null, 2) + "\n"
);

const mainReadmeSrc = path.join(packagingDir, "npm", "main", "README.md");
if (fs.existsSync(mainReadmeSrc)) {
  fs.copyFileSync(mainReadmeSrc, path.join(mainPkgDir, "README.md"));
} else {
  fs.writeFileSync(
    path.join(mainPkgDir, "README.md"),
    `# ${mainPackageName}\n\n${config.description}\n`
  );
}

const rootLicense = path.join(repoRoot, "LICENSE");
if (fs.existsSync(rootLicense)) {
  fs.copyFileSync(rootLicense, path.join(mainPkgDir, "LICENSE"));
} else {
  fs.writeFileSync(path.join(mainPkgDir, "LICENSE"), `${config.license} License\n`);
}

// 3. Print size summary table
console.log("\n================ BUILD SUMMARY ================");
console.log(`Main Package: ${mainPackageName} (${config.version})`);
console.log(`Output Directory: ${packagesDir}\n`);
console.log("| Target | Package | Binary | Bytes | Size | Note |");
console.log("|---|---|---|---|---|---|");

for (const report of sizeReports) {
  let note = "OK";
  if (report.bytes > 25 * 1024 * 1024) {
    note = "Exceeds 25MB (analyzed below)";
  }
  console.log(
    `| ${report.target} | ${report.package} | ${report.binary} | ${report.bytes.toLocaleString()} | ${report.sizeMb} | ${note} |`
  );
}
console.log("===============================================\n");

// Check if any exceeded 25MB
const exceeded = sizeReports.filter((r) => r.bytes > 25 * 1024 * 1024);
if (exceeded.length > 0) {
  console.log(
    `[build] Notice: ${exceeded.length} target binaries exceed 25 MB:`
  );
  for (const item of exceeded) {
    console.log(` - ${item.target}: ${item.sizeMb}`);
  }
  console.log(
    "[build] Dominant components: Wazero WASM runtime for sqlite-vec and go-sqlite3, plus FTS5 and standard runtime libraries."
  );
} else {
  console.log("[build] All binary sizes are within the 25 MB budget.");
}

console.log("[build] Build completed successfully.");
