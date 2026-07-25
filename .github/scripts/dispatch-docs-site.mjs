const token = requireEnvironment('MARGINLAB_PREVIEW_TOKEN');
const docsSha = requireSha('DOCS_SHA');
const dispatchEvent = requireDispatchEvent('DISPATCH_EVENT');
const sourceRepository =
  process.env.SOURCE_REPOSITORY ?? 'Margin-Lab/evals';
const docsRepository =
  process.env.DOCS_REPOSITORY ?? sourceRepository;
const targetRepository =
  process.env.TARGET_REPOSITORY ?? 'Margin-Lab/marginlab';
const previewId = normalizePreviewId(
  process.env.PREVIEW_ID ?? `docs-${docsSha.slice(0, 12)}`,
);
const sourceRef = process.env.SOURCE_REF || null;
const sourceSha = process.env.SOURCE_SHA?.toLowerCase() || null;

if (sourceRepository !== 'Margin-Lab/evals') {
  throw new Error(`Unexpected source repository: ${sourceRepository}`);
}
if (targetRepository !== 'Margin-Lab/marginlab') {
  throw new Error(`Unexpected target repository: ${targetRepository}`);
}
if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(docsRepository)) {
  throw new Error(`Invalid documentation repository: ${docsRepository}`);
}
if (
  dispatchEvent === 'docs-release'
  && (
    docsRepository !== 'Margin-Lab/evals'
    || process.env.DOCS_PR
    || previewId !== 'docs-main'
    || sourceRef !== 'refs/heads/main'
    || sourceSha !== docsSha
  )
) {
  throw new Error(
    'docs-release must target Margin-Lab/evals main without a pull request',
  );
}

const response = await fetch(
  `https://api.github.com/repos/${targetRepository}/dispatches`,
  {
    method: 'POST',
    headers: {
      Accept: 'application/vnd.github+json',
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
      'User-Agent': 'marginlab-docs-site-dispatch',
      'X-GitHub-Api-Version': '2022-11-28',
    },
    body: JSON.stringify({
      event_type: dispatchEvent,
      client_payload: {
        source_repository: sourceRepository,
        docs_repository: docsRepository,
        docs_sha: docsSha,
        preview_id: previewId,
        docs_pr: process.env.DOCS_PR || null,
        source_ref: sourceRef,
        source_sha: sourceSha,
      },
    }),
  },
);

if (!response.ok) {
  const responseBody = await response.text();
  throw new Error(
    `GitHub repository dispatch failed (${response.status}): ${responseBody}`,
  );
}

console.log(
  `Requested MarginLab ${dispatchEvent} build ${previewId} for documentation ${docsSha}.`,
);

function requireEnvironment(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

function requireSha(name) {
  const value = requireEnvironment(name).toLowerCase();
  if (!/^[0-9a-f]{40}$/.test(value)) {
    throw new Error(`${name} must be a full 40-character Git commit SHA`);
  }
  return value;
}

function requireDispatchEvent(name) {
  const value = requireEnvironment(name);
  if (!['docs-preview', 'docs-release'].includes(value)) {
    throw new Error(
      `${name} must be either docs-preview or docs-release`,
    );
  }
  return value;
}

function normalizePreviewId(value) {
  const normalized = value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-+/g, '-')
    .slice(0, 40)
    .replace(/-+$/g, '');

  if (!normalized) {
    throw new Error('Preview ID is empty after normalization');
  }
  if (['live', 'main', 'prod', 'production'].includes(normalized)) {
    throw new Error(`${normalized} is reserved and cannot identify a preview`);
  }
  return normalized;
}
