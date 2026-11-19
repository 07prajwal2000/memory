#!/usr/bin/env node
// Generates docs/llms.txt and docs/llms-full.txt from the docs pages.
// llms.txt follows the llmstxt.org convention; llms-full.txt concatenates
// every page in sidebar order for tools that read the docs in one fetch.
// No dependencies. Works on Windows, macOS and Linux.
// Usage: node docs/scripts/build-llms.mjs [--check] [--base URL]

import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// The GitHub Pages site for 07prajwal2000/memory. Links point at the raw
// Markdown files, not at Docsify's #/ routes, because LLMs fetch Markdown.
const BASE_URL = "https://07prajwal2000.github.io/memory";

// Secondary pages, listed under "## Optional" in llms.txt.
const OPTIONAL_PAGES = new Set(["recipes.md", "faq.md", "contributing.md", "changelog.md"]);

// Pages without a one-sentence summary after their H1.
const DESCRIPTION_OVERRIDES = {
  "faq.md": "Common questions about privacy, offline use, scale, agents and licensing.",
  "changelog.md": "Release notes and notable changes, newest first.",
};

const here = dirname(fileURLToPath(import.meta.url));
const docsDir = resolve(here, "..");

function pagePath(file) {
  return join(docsDir, file);
}

function parseSidebar() {
  const text = readFileSync(join(docsDir, "_sidebar.md"), "utf8");
  const groups = [];
  let current = null;
  for (const raw of text.split(/\r?\n/)) {
    if (!raw.trim()) continue;
    const entry = raw.match(/^\s+-\s+\[([^\]]+)\]\(([^)]+)\)\s*$/);
    if (entry) {
      if (!current) {
        current = { title: "", pages: [] };
        groups.push(current);
      }
      current.pages.push({ title: entry[1].trim(), file: entry[2].trim() });
      continue;
    }
    const group = raw.match(/^-\s+(.+?)\s*$/);
    if (group) {
      current = { title: group[1].trim(), pages: [] };
      groups.push(current);
    }
  }
  return groups.filter((group) => group.pages.length);
}

function cleanInline(text) {
  return text
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/\*\*/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function summaryOf(file) {
  if (DESCRIPTION_OVERRIDES[file]) return DESCRIPTION_OVERRIDES[file];
  if (!existsSync(pagePath(file))) throw new Error(`sidebar points at missing page: ${file}`);
  const lines = readFileSync(pagePath(file), "utf8").split(/\r?\n/);

  let i = 0;
  while (i < lines.length && !/^#\s+\S/.test(lines[i])) i++;
  i++;

  const paragraph = [];
  let fence = false;
  for (; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*(```|~~~)/.test(line)) {
      fence = !fence;
      if (paragraph.length) break;
      continue;
    }
    if (fence) continue;
    if (!line.trim()) {
      if (paragraph.length) break;
      continue;
    }
    if (/^#{1,6}\s/.test(line) || /^>/.test(line) || /^\s*([-*+]|\d+\.)\s/.test(line) || /^\s*\|/.test(line) || /^</.test(line)) {
      if (paragraph.length) break;
      continue;
    }
    paragraph.push(line.trim());
  }

  const text = cleanInline(paragraph.join(" "));
  const sentence = text.match(/^[\s\S]*?[.!?](?=\s|$)/);
  return (sentence ? sentence[0] : text).trim();
}

const CALLOUT_LABELS = { NOTE: "Note", WARNING: "Warning", TIP: "Tip", IMPORTANT: "Important" };

function cleanForFull(text) {
  let out = text.replace(/<!--[\s\S]*?-->/g, "");
  out = out.replace(/[ \t]*:id=[A-Za-z0-9_-]+/g, "");
  out = out
    .split(/\r?\n/)
    .map((line) => {
      const callout = line.match(/^(>\s*)\[!(NOTE|WARNING|TIP|IMPORTANT)\]\s*(.*)$/i);
      if (!callout) return line;
      const label = CALLOUT_LABELS[callout[2].toUpperCase()];
      const rest = callout[3].replace(/\s+$/, "");
      return `${callout[1]}**${label}:**${rest ? " " + rest : ""}`;
    })
    .join("\n");
  return out.trim();
}

function build(baseOverride) {
  const base = (baseOverride || BASE_URL).replace(/\/+$/, "");
  const intro = readFileSync(join(docsDir, "llms-intro.md"), "utf8").trim();
  const groups = parseSidebar();
  const allPages = groups.flatMap((group) => group.pages);

  const sections = [];
  const optional = [];
  for (const group of groups) {
    const kept = [];
    for (const page of group.pages) {
      if (OPTIONAL_PAGES.has(page.file)) optional.push(page);
      else kept.push(page);
    }
    if (kept.length) sections.push({ title: group.title, pages: kept });
  }
  if (optional.length) sections.push({ title: "Optional", pages: optional });

  let links = `${intro}\n`;
  for (const section of sections) {
    links += `\n## ${section.title}\n\n`;
    for (const page of section.pages) {
      links += `- [${page.title}](${base}/${page.file}): ${summaryOf(page.file)}\n`;
    }
  }
  links = links.replace(/\n{3,}/g, "\n\n").replace(/\s+$/, "") + "\n";

  let full =
    "# mem — full documentation\n\n" +
    "> Generated from the Markdown sources of https://07prajwal2000.github.io/memory/ " +
    "for tools that read the docs in one fetch. Regenerate with " +
    "`node docs/scripts/build-llms.mjs`; do not edit by hand.\n\n" +
    "Every documentation page in sidebar order, each preceded by its source URL.\n";
  for (const page of allPages) {
    full += `\n---\n\nSource: ${base}/${page.file}\n\n${cleanForFull(readFileSync(pagePath(page.file), "utf8"))}\n`;
  }
  full = full.replace(/\s+$/, "") + "\n";

  return { "llms.txt": links, "llms-full.txt": full };
}

const args = process.argv.slice(2);
const check = args.includes("--check");
const baseIndex = args.indexOf("--base");
if (baseIndex >= 0 && !args[baseIndex + 1]) {
  console.error("--base needs a URL, for example --base https://example.com/docs");
  process.exit(2);
}

const outputs = build(baseIndex >= 0 ? args[baseIndex + 1] : BASE_URL);

if (check) {
  const stale = [];
  for (const [name, content] of Object.entries(outputs)) {
    const path = join(docsDir, name);
    const onDisk = existsSync(path) ? readFileSync(path, "utf8") : null;
    if (onDisk !== content) stale.push(name);
  }
  if (stale.length) {
    console.error(`${stale.join(" and ")} out of date; run node docs/scripts/build-llms.mjs`);
    process.exit(1);
  }
  console.log("llms.txt and llms-full.txt are up to date.");
} else {
  for (const [name, content] of Object.entries(outputs)) {
    writeFileSync(join(docsDir, name), content);
    console.log(`wrote docs/${name} (${Buffer.byteLength(content, "utf8")} bytes)`);
  }
}
