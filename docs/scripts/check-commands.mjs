#!/usr/bin/env node
// Checks every `mem ...` invocation in the docs against the real binary.
// Usage: node docs/scripts/check-commands.mjs --bin ./mem [--verbose]
// Node 18+, no dependencies.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, dirname, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";

const here = dirname(fileURLToPath(import.meta.url));
const docsDir = join(here, "..");

const args = process.argv.slice(2);
const binIndex = args.indexOf("--bin");
const memBin = binIndex >= 0 ? args[binIndex + 1] : process.env.MEM_BIN || "mem";
const verbose = args.includes("--verbose");

const SHELL_LANGS = new Set(["bash", "sh", "shell", "powershell", "pwsh", "ps1"]);
const GLOBAL_FLAGS_WITH_VALUE = new Set(["--data-dir", "--config"]);
const GLOBAL_BOOLEAN_FLAGS = new Set(["--json", "-q", "--quiet", "-v", "--verbose"]);

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      if (entry === "scripts" || entry === "assets") continue;
      walk(full, out);
    } else if (entry.endsWith(".md")) {
      out.push(full);
    }
  }
  return out;
}

function codeBlocks(markdown) {
  const lines = markdown.split(/\r?\n/);
  const blocks = [];
  let current = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = line.match(/^\s*(```+|~~~+)\s*([\w-]*)\s*$/);
    if (fence && !current) {
      current = { lang: (fence[2] || "").toLowerCase(), start: i + 1, lines: [] };
      continue;
    }
    if (fence && current) {
      blocks.push(current);
      current = null;
      continue;
    }
    if (current) current.lines.push({ text: line, line: i + 1 });
  }
  return blocks;
}

function logicalLines(block) {
  const out = [];
  let buffer = null;
  for (const { text, line } of block.lines) {
    if (buffer) {
      buffer.text += " " + text.trim();
      buffer.end = line;
    } else {
      buffer = { text: text.trim(), start: line, end: line };
    }
    if (buffer.text.endsWith("\\")) {
      buffer.text = buffer.text.slice(0, -1).trimEnd();
      continue;
    }
    out.push(buffer);
    buffer = null;
  }
  if (buffer) out.push(buffer);
  return out;
}

function shellSegments(text) {
  return text
    .split(/\s+&&\s+|;\s*/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function extractMemCommand(segment) {
  let s = segment.trim();
  if (!s || s.startsWith("#")) return null;

  // Part after a `--` separator, e.g. `claude mcp add mem -- mem mcp`.
  const dashDash = s.lastIndexOf(" -- ");
  if (dashDash >= 0) s = s.slice(dashDash + 4).trim();

  // Command substitution, e.g. `hits=$(mem search ...)`.
  const sub = s.match(/\$\(([^)]*)\)/);
  if (sub) s = sub[1].trim();

  // Leading environment assignments, e.g. `MEM_CONFIG=/tmp/x mem status`.
  while (/^[A-Za-z_][A-Za-z0-9_]*=\S*\s+/.test(s)) {
    s = s.replace(/^[A-Za-z_][A-Za-z0-9_]*=\S*\s+/, "").trim();
  }

  // `mem` must be the command itself or follow a pipe. This avoids treating
  // arguments like `go build -o mem ./cmd/mem` as invocations.
  const invocation = /(?:^|\|\s*)((?:\.\/)?mem(?:\.exe)?)\s+/;
  const match = s.match(invocation);
  if (!match) return null;
  return s.slice(match.index + (match[0].length - match[0].trimStart().length)).trim();
}

function tokenize(command) {
  return command
    .match(/(?:"[^"]*"|'[^']*'|\S+)/g)
    ?.map((t) => t.replace(/^["']|["']$/g, "")) ?? [];
}

function helpFor(command, cache) {
  if (cache.has(command)) return cache.get(command);
  try {
    const text = execFileSync(memBin, ["help", command], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
    cache.set(command, text);
    return text;
  } catch (error) {
    const text = `${error.stdout || ""}`;
    cache.set(command, text);
    return text;
  }
}

function short(text, n = 60) {
  return text.length > n ? text.slice(0, n - 1) + "…" : text;
}

const topHelp = helpFor("", new Map());
const topLevel = new Set();
for (const line of topHelp.split(/\r?\n/)) {
  const m = line.match(/^  ([a-z][a-z-]*)\b/);
  if (m) topLevel.add(m[1]);
}
topLevel.add("help");

const failures = [];
const checked = [];
const files = walk(docsDir).sort();

for (const file of files) {
  const markdown = readFileSync(file, "utf8");
  for (const block of codeBlocks(markdown)) {
    if (!SHELL_LANGS.has(block.lang)) continue;
    for (const logical of logicalLines(block)) {
      if (logical.text.includes("# example only")) continue;
      if (logical.text.startsWith("#")) continue;
      for (const segment of shellSegments(logical.text)) {
        const command = extractMemCommand(segment);
        if (!command) continue;

        const tokens = tokenize(command);
        if (tokens.length && /^(\.\/)?mem(\.exe)?$/i.test(tokens[0])) tokens.shift();
        else continue;

        let i = 0;
        while (i < tokens.length) {
          const token = tokens[i];
          if (GLOBAL_BOOLEAN_FLAGS.has(token)) { i++; continue; }
          if (GLOBAL_FLAGS_WITH_VALUE.has(token)) { i += 2; continue; }
          if (token.startsWith("-")) { i++; continue; }
          break;
        }
        if (i >= tokens.length) continue;

        const name = tokens[i];
        i++;
        const flags = tokens.slice(i).filter((t) => t.startsWith("-") && t !== "-");

        const where = `${relative(process.cwd(), file)}:${logical.start}`;
        const record = { where, name, flags: [...new Set(flags)], command };
        checked.push(record);

        if (!topLevel.has(name)) {
          failures.push(`${where}: "${name}" is not a command (${short(command)})`);
          continue;
        }

        const text = helpFor(name, new Map());
        if (!text.trim()) {
          failures.push(`${where}: \`mem help ${name}\` produced no help text`);
          continue;
        }

        if (name === "context") {
          const sub = tokens[i];
          if (!sub || !/^(create|ls|list|rm|expiry)$/.test(sub)) {
            failures.push(`${where}: unexpected context subcommand "${sub ?? "(none)"}"`);
          } else if (!text.includes(sub)) {
            failures.push(`${where}: \`mem help context\` does not mention "${sub}"`);
          }
        }

        for (const flag of record.flags) {
          if (GLOBAL_BOOLEAN_FLAGS.has(flag) || GLOBAL_FLAGS_WITH_VALUE.has(flag)) continue;
          const base = flag.split("=")[0];
          if (!text.includes(base)) {
            failures.push(`${where}: flag ${base} is not in \`mem help ${name}\``);
          }
        }
      }
    }
  }
}

if (verbose) {
  for (const c of checked) console.log(`ok  ${c.where}  mem ${c.name} ${c.flags.join(" ")}`);
}

console.log(`Checked ${checked.length} command invocations from ${files.length} pages using ${memBin}.`);
if (failures.length) {
  console.error(`\n${failures.length} problem(s):`);
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log("All documented mem commands and flags exist in the binary.");
