#!/usr/bin/env node
// Verifies that every relative link and image target in the docs resolves to a
// real file, and that local #anchors match a heading. No dependencies.
// Usage: node docs/scripts/check-links.mjs [--verbose]

import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, dirname, resolve, relative } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const docsDir = resolve(here, "..");
const verbose = process.argv.includes("--verbose");

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

function slug(text) {
  return text
    .trim()
    .replace(/`([^`]*)`/g, "$1")
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, "")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

function anchorsOf(file) {
  const out = new Set();
  for (const line of readFileSync(file, "utf8").split(/\r?\n/)) {
    const m = line.match(/^#{1,6}\s+(.*?)\s*#*\s*$/);
    if (m) out.add(slug(m[1]));
  }
  return out;
}

const anchorCache = new Map();
function anchorExists(file, anchor) {
  if (!anchorCache.has(file)) anchorCache.set(file, anchorsOf(file));
  return anchorCache.get(file).has(anchor.toLowerCase());
}

const files = walk(docsDir).sort();
const failures = [];
const warnings = [];
let links = 0;

for (const file of files) {
  const text = readFileSync(file, "utf8");
  const lines = text.split(/\r?\n/);
  lines.forEach((line, index) => {
    if (/^\s*```/.test(line)) return; // fences are handled by not tracking blocks; links inside code are rare
    const re = /!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g;
    let m;
    while ((m = re.exec(line)) !== null) {
      const raw = m[1];
      if (/^(https?:|mailto:|tel:|data:|javascript:)/i.test(raw)) continue;
      links++;
      const where = `${relative(process.cwd(), file)}:${index + 1}`;
      const [rawPath, rawAnchor] = raw.split("#");
      let target = rawPath;
      if (target === "") target = file;
      if (target === "/") target = "README.md";
      if (target.endsWith("/")) target = join(target, "README.md");
      let decoded;
      try {
        decoded = decodeURIComponent(target);
      } catch {
        decoded = target;
      }
      const targetFile = resolve(dirname(file), decoded);
      if (!existsSync(targetFile)) {
        failures.push(`${where}: missing file for "${raw}" -> ${relative(docsDir, targetFile)}`);
        continue;
      }
      if (rawAnchor) {
        if (!anchorExists(targetFile, rawAnchor)) {
          failures.push(`${where}: missing anchor "#${rawAnchor}" in ${relative(docsDir, targetFile)}`);
        }
      }
      if (verbose) console.log(`ok  ${where}  ${raw}`);
    }
  });
}

// Sidebar and navbar are covered by the walk, but check that they exist.
for (const name of ["_sidebar.md", "_navbar.md", "README.md", "index.html", ".nojekyll"]) {
  if (!existsSync(join(docsDir, name))) failures.push(`docs/${name} is missing`);
}

// llms.txt links are absolute site URLs; each one must map back to a real page.
const LLMS_BASE = "https://07prajwal2000.github.io/memory/";
const llmsFile = join(docsDir, "llms.txt");
let llmsLinks = 0;
if (existsSync(llmsFile)) {
  const text = readFileSync(llmsFile, "utf8");
  const re = /\]\(([^)\s]+)\)/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    const url = m[1];
    if (!url.startsWith(LLMS_BASE)) continue;
    const rel = decodeURIComponent(url.slice(LLMS_BASE.length));
    const target = resolve(docsDir, rel);
    if (!existsSync(target)) {
      failures.push(`docs/llms.txt: "${url}" has no page at docs/${rel}`);
      continue;
    }
    links++;
    llmsLinks++;
  }
}

// llms-full.txt is one long Markdown file; its code fences must stay balanced.
const llmsFullFile = join(docsDir, "llms-full.txt");
if (existsSync(llmsFullFile)) {
  let open = null;
  readFileSync(llmsFullFile, "utf8").split(/\r?\n/).forEach((line, index) => {
    const fence = line.match(/^\s*(```+|~~~+)\s*[\w-]*\s*$/);
    if (!fence) return;
    if (!open) {
      open = { marker: fence[1][0], length: fence[1].length, line: index + 1 };
    } else if (fence[1][0] === open.marker && fence[1].length >= open.length) {
      open = null;
    }
  });
  if (open) {
    failures.push(`docs/llms-full.txt: code fence opened at line ${open.line} is never closed`);
  }
}

console.log(
  `Checked ${links} links across ${files.length} markdown pages` +
    (existsSync(llmsFile) ? ` (including ${llmsLinks} in llms.txt)` : "") +
    "."
);
for (const w of warnings) console.warn(`warning: ${w}`);
if (failures.length) {
  console.error(`\n${failures.length} broken link(s):`);
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log("All relative links and anchors resolve.");
