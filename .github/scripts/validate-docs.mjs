import { readdir, readFile, stat } from 'node:fs/promises';
import path from 'node:path';

const docsRoot = path.resolve(process.argv[2] ?? 'docs/cli');
const summaryPath = path.join(docsRoot, 'SUMMARY.md');
const failures = [];

await assertDirectory(docsRoot);

const summary = await readFile(summaryPath, 'utf8');
const summaryEntries = parseSummary(summary);
const summaryPaths = new Set();

for (const entry of summaryEntries) {
  const normalizedPath = normalizeSummaryPath(entry.target, entry.line);
  if (!normalizedPath) {
    continue;
  }

  if (summaryPaths.has(normalizedPath)) {
    failures.push(
      `SUMMARY.md:${entry.line}: duplicate navigation target ${normalizedPath}`,
    );
    continue;
  }

  summaryPaths.add(normalizedPath);
  await assertFile(
    path.join(docsRoot, normalizedPath),
    `SUMMARY.md:${entry.line}: missing page ${normalizedPath}`,
  );
}

const contentFiles = (await walkMarkdown(docsRoot))
  .map((file) => path.relative(docsRoot, file).split(path.sep).join('/'))
  .filter((file) => file !== 'SUMMARY.md')
  .sort();

for (const contentFile of contentFiles) {
  if (!summaryPaths.has(contentFile)) {
    failures.push(`Page is not listed in SUMMARY.md: ${contentFile}`);
  }

  await validatePage(path.join(docsRoot, contentFile), contentFile);
}

for (const summaryPage of summaryPaths) {
  if (!contentFiles.includes(summaryPage)) {
    failures.push(`SUMMARY.md contains a non-content page: ${summaryPage}`);
  }
}

if (summaryEntries.length === 0) {
  failures.push('SUMMARY.md does not contain any linked pages');
}

if (failures.length > 0) {
  console.error('Documentation validation failed:');
  for (const failure of failures) {
    console.error(`  - ${failure}`);
  }
  process.exitCode = 1;
} else {
  console.log(
    `Validated ${contentFiles.length} documentation pages and ${summaryEntries.length} navigation entries.`,
  );
}

function parseSummary(markdown) {
  const entries = [];
  const lines = stripCodeFences(markdown).split(/\r?\n/);
  const linkPattern = /^\s*[-*+]\s+\[([^\]]+)\]\(([^)]+)\)\s*$/;

  for (const [index, line] of lines.entries()) {
    const match = line.match(linkPattern);
    if (match) {
      entries.push({
        label: match[1].trim(),
        target: cleanLinkTarget(match[2]),
        line: index + 1,
      });
    }
  }

  return entries;
}

function normalizeSummaryPath(target, line) {
  const withoutFragment = target.split(/[?#]/, 1)[0];
  let decoded;

  try {
    decoded = decodeURIComponent(withoutFragment);
  } catch {
    failures.push(`SUMMARY.md:${line}: malformed URL encoding in ${target}`);
    return null;
  }

  if (
    decoded === '' ||
    path.isAbsolute(decoded) ||
    decoded.includes('\\') ||
    path.extname(decoded).toLowerCase() !== '.md'
  ) {
    failures.push(
      `SUMMARY.md:${line}: target must be a relative Markdown file: ${target}`,
    );
    return null;
  }

  const normalized = path.posix.normalize(decoded);
  if (normalized === '..' || normalized.startsWith('../')) {
    failures.push(`SUMMARY.md:${line}: target escapes docs/cli: ${target}`);
    return null;
  }

  return normalized;
}

async function validatePage(absolutePath, relativePath) {
  const markdown = await readFile(absolutePath, 'utf8');
  const contentWithoutCode = stripCodeFences(markdown);
  const h1Lines = contentWithoutCode
    .split(/\r?\n/)
    .map((line, index) => ({ line, number: index + 1 }))
    .filter(({ line }) => /^#\s+\S/.test(line));

  if (h1Lines.length !== 1) {
    failures.push(
      `${relativePath}: expected exactly one level-one heading, found ${h1Lines.length}`,
    );
  }

  const markdownLinks =
    contentWithoutCode.matchAll(/!?\[[^\]]*\]\(([^)\n]+)\)/g);

  for (const match of markdownLinks) {
    const target = cleanLinkTarget(match[1]);
    if (
      target === '' ||
      target.startsWith('#') ||
      target.startsWith('/') ||
      /^(?:https?:|mailto:|tel:|data:)/i.test(target)
    ) {
      continue;
    }

    const targetWithoutFragment = target.split(/[?#]/, 1)[0];
    let decoded;
    try {
      decoded = decodeURIComponent(targetWithoutFragment);
    } catch {
      failures.push(`${relativePath}: malformed URL encoding in link ${target}`);
      continue;
    }

    const resolvedPath = path.resolve(path.dirname(absolutePath), decoded);
    if (
      resolvedPath !== docsRoot &&
      !resolvedPath.startsWith(`${docsRoot}${path.sep}`)
    ) {
      failures.push(`${relativePath}: local link escapes docs/cli: ${target}`);
      continue;
    }

    await assertFile(
      resolvedPath,
      `${relativePath}: unresolved local link ${target}`,
    );
  }
}

function cleanLinkTarget(rawTarget) {
  const trimmed = rawTarget.trim();
  if (trimmed.startsWith('<') && trimmed.endsWith('>')) {
    return trimmed.slice(1, -1);
  }

  // Markdown permits an optional quoted title after the URL. Documentation
  // paths in this repository do not contain spaces, so this is unambiguous.
  return trimmed.replace(/\s+(?:"[^"]*"|'[^']*')\s*$/, '');
}

function stripCodeFences(markdown) {
  const lines = markdown.split(/\r?\n/);
  let fence = null;

  return lines
    .map((line) => {
      const marker = line.match(/^\s*(`{3,}|~{3,})/);
      if (marker) {
        if (fence === null) {
          fence = marker[1][0];
        } else if (marker[1][0] === fence) {
          fence = null;
        }
        return '';
      }
      return fence === null ? line : '';
    })
    .join('\n');
}

async function walkMarkdown(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const absolutePath = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        return walkMarkdown(absolutePath);
      }
      return entry.isFile() && entry.name.endsWith('.md') ? [absolutePath] : [];
    }),
  );
  return files.flat();
}

async function assertDirectory(directory) {
  try {
    const metadata = await stat(directory);
    if (!metadata.isDirectory()) {
      throw new Error('not a directory');
    }
  } catch {
    console.error(`Documentation root is unavailable: ${directory}`);
    process.exit(1);
  }
}

async function assertFile(file, failureMessage) {
  try {
    const metadata = await stat(file);
    if (!metadata.isFile()) {
      failures.push(failureMessage);
    }
  } catch {
    failures.push(failureMessage);
  }
}
