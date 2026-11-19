#!/usr/bin/env node
"use strict";

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

function getPlatformString() {
  const platform = process.platform;
  const arch = process.arch;

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

function resolveBinary() {
  if (process.env.MEM_BINARY_PATH) {
    const customPath = path.resolve(process.env.MEM_BINARY_PATH);
    if (!fs.existsSync(customPath)) {
      console.error(
        `Error: MEM_BINARY_PATH is set to "${process.env.MEM_BINARY_PATH}", but the file does not exist.`
      );
      process.exit(1);
    }
    return customPath;
  }

  const platformArch = getPlatformString();
  if (!platformArch) {
    console.error(
      `Error: Unsupported platform or architecture (${process.platform}-${process.arch}).`
    );
    console.error(
      "Supported combinations: linux-x64, linux-arm64, darwin-x64, darwin-arm64, win32-x64, win32-arm64."
    );
    console.error(
      "You can compile the binary from source and set MEM_BINARY_PATH=/path/to/mem"
    );
    process.exit(1);
  }

  const mainPkgJsonPath = path.join(__dirname, "..", "package.json");
  let mainPkg = {};
  try {
    mainPkg = JSON.parse(fs.readFileSync(mainPkgJsonPath, "utf8"));
  } catch (err) {
    console.error("Error: Failed to read package.json for main package:", err.message);
    process.exit(1);
  }

  const fullPkgName = mainPkg.name || "@07prajwal2000/agent-memory";
  const binName = "mem";

  let platformPkgName = "";
  if (fullPkgName.startsWith("@")) {
    const parts = fullPkgName.split("/");
    const scope = parts[0];
    const baseName = parts[1] || "agent-memory";
    platformPkgName = `${scope}/${baseName}-${platformArch}`;
  } else {
    platformPkgName = `${fullPkgName}-${platformArch}`;
  }

  let platformPkgJson = "";
  try {
    platformPkgJson = require.resolve(`${platformPkgName}/package.json`);
  } catch (err) {
    console.error(`Error: Could not resolve platform package "${platformPkgName}".`);
    console.error(
      `This package contains the prebuilt ${platformArch} binary for ${fullPkgName}.`
    );
    console.error("");
    console.error("This usually occurs when optional dependencies are omitted");
    console.error("(such as running npm install with --no-optional or --omit=optional).");
    console.error("");
    console.error("To resolve this issue:");
    console.error(`  1. Reinstall with optional dependencies enabled:`);
    console.error(`     npm install ${fullPkgName}`);
    console.error(`  2. Or install the matching platform package explicitly:`);
    console.error(`     npm install ${platformPkgName}`);
    console.error(`  3. Or specify a prebuilt binary using the MEM_BINARY_PATH environment variable:`);
    console.error(`     MEM_BINARY_PATH=/path/to/${binName} ${binName} ...`);
    process.exit(1);
  }

  const pkgDir = path.dirname(platformPkgJson);
  const exeExt = process.platform === "win32" ? ".exe" : "";
  const binaryFile = path.join(pkgDir, "bin", `${binName}${exeExt}`);

  if (!fs.existsSync(binaryFile)) {
    console.error(`Error: Executable binary not found at "${binaryFile}".`);
    process.exit(1);
  }

  if (process.platform !== "win32") {
    try {
      const stats = fs.statSync(binaryFile);
      if ((stats.mode & 0o111) === 0) {
        fs.chmodSync(binaryFile, stats.mode | 0o755);
      }
    } catch {
      // Ignore chmod failures on read-only environments
    }
  }

  return binaryFile;
}

function main() {
  const binaryPath = resolveBinary();
  const args = process.argv.slice(2);

  const child = spawn(binaryPath, args, {
    stdio: "inherit",
    windowsHide: true,
  });

  child.on("error", (err) => {
    console.error(`Error starting child process (${binaryPath}):`, err.message);
    process.exit(1);
  });

  child.on("close", (code, signal) => {
    if (signal) {
      if (process.platform !== "win32") {
        process.kill(process.pid, signal);
      } else {
        process.exit(1);
      }
    } else {
      process.exit(code ?? 0);
    }
  });

  const forward = (sig) => {
    if (child && child.pid) {
      try {
        child.kill(sig);
      } catch {
        // Child process may already have terminated
      }
    }
  };

  process.on("SIGINT", () => forward("SIGINT"));
  process.on("SIGTERM", () => forward("SIGTERM"));
}

main();
