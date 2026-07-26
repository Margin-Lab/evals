import assert from 'node:assert/strict';
import test from 'node:test';

const docsSha = 'b68519d5194bf8dbc61e56201951f01c5c497817';
const scriptUrl = new URL('./dispatch-docs-site.mjs', import.meta.url);

test('docs-release carries an independently verifiable main-branch identity', async () => {
  let request;
  await runDispatch(
    {
      DISPATCH_EVENT: 'docs-release',
      DOCS_SHA: docsSha,
      PREVIEW_ID: 'docs-main',
      SOURCE_REF: 'refs/heads/main',
      SOURCE_SHA: docsSha,
    },
    (url, options) => {
      request = { url, options };
      return { ok: true };
    },
  );

  assert.equal(
    request.url,
    'https://api.github.com/repos/Margin-Lab/marginlab/dispatches',
  );
  const payload = JSON.parse(request.options.body);
  assert.deepEqual(payload, {
    event_type: 'docs-release',
    client_payload: {
      source_repository: 'Margin-Lab/evals',
      docs_repository: 'Margin-Lab/evals',
      docs_sha: docsSha,
      preview_id: 'docs-main',
      docs_pr: null,
      source_ref: 'refs/heads/main',
      source_sha: docsSha,
    },
  });
});

test('docs-release rejects a non-main source identity before dispatch', async () => {
  await assert.rejects(
    runDispatch(
      {
        DISPATCH_EVENT: 'docs-release',
        DOCS_SHA: docsSha,
        PREVIEW_ID: 'docs-main',
        SOURCE_REF: 'refs/heads/feature',
        SOURCE_SHA: docsSha,
      },
      () => {
        throw new Error('fetch must not run');
      },
    ),
    /docs-release must target Margin-Lab\/evals main/,
  );
});

async function runDispatch(overrides, fetchImplementation) {
  const names = [
    'MARGINLAB_PREVIEW_TOKEN',
    'DISPATCH_EVENT',
    'DOCS_SHA',
    'DOCS_REPOSITORY',
    'DOCS_PR',
    'PREVIEW_ID',
    'SOURCE_REF',
    'SOURCE_REPOSITORY',
    'SOURCE_SHA',
    'TARGET_REPOSITORY',
  ];
  const previousEnvironment = Object.fromEntries(
    names.map((name) => [name, process.env[name]]),
  );
  const previousFetch = globalThis.fetch;

  for (const name of names) {
    delete process.env[name];
  }
  Object.assign(process.env, {
    MARGINLAB_PREVIEW_TOKEN: 'test-token',
    ...overrides,
  });
  globalThis.fetch = async (url, options) =>
    fetchImplementation(url, options);

  try {
    await import(`${scriptUrl.href}?test=${crypto.randomUUID()}`);
  } finally {
    globalThis.fetch = previousFetch;
    for (const name of names) {
      const value = previousEnvironment[name];
      if (value === undefined) {
        delete process.env[name];
      } else {
        process.env[name] = value;
      }
    }
  }
}
