import { describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import {
  classifyError,
  createBuilderApi,
  cursorPath,
  documentPath,
  DOCUMENTS_PATH,
  DRAFTS_PATH,
  draftPath,
  errorMessage,
  GENERATE_PATH,
  publishIntent,
  publishPath,
  readEnvelope,
  readPublishResult,
  readETag,
  SCHEMA_PATH,
  snapshotPath,
  SOURCES_PATH,
} from '@/builder/api.js';

import { sampleDocument } from './fixtures.js';

function fakeHttp(responses = {}) {
  const calls = [];
  const handler =
    (method) =>
    (url, ...rest) => {
      calls.push({ method, url, body: rest[0], config: rest[1] ?? rest[0] });

      const response = responses[`${method} ${url}`] ?? responses[url];

      if (response instanceof Error) {
        return Promise.reject(response);
      }

      return Promise.resolve(response ?? { data: {}, headers: {} });
    };

  return {
    calls,
    get: handler('get'),
    post: handler('post'),
    patch: handler('patch'),
    delete: handler('delete'),
  };
}

function httpError(status) {
  return Object.assign(new Error(`status ${status}`), {
    response: { status, data: {} },
  });
}

describe('routes', () => {
  test('match the documented backend contract', () => {
    expect(DRAFTS_PATH).toBe('builder/drafts');
    expect(SOURCES_PATH).toBe('builder/sources');
    expect(GENERATE_PATH).toBe('builder/generate');
    expect(DOCUMENTS_PATH).toBe('builder/documents');
    expect(SCHEMA_PATH).toBe('schemas/builder/v1');
    expect(draftPath('alice', 'd1')).toBe('builder/drafts/alice/d1');
    expect(snapshotPath('alice', 'd1')).toBe(
      'builder/drafts/alice/d1/snapshots',
    );
    expect(snapshotPath('alice', 'd1', 's2')).toBe(
      'builder/drafts/alice/d1/snapshots/s2',
    );
    expect(cursorPath('alice', 'd1')).toBe('builder/drafts/alice/d1/cursor');
    expect(publishPath('alice', 'd1')).toBe('builder/drafts/alice/d1/publish');
    expect(documentPath('doc 1')).toBe('builder/documents/doc%201');
  });

  test('owner and id are URL encoded', () => {
    expect(draftPath('a/b', 'c d')).toBe('builder/drafts/a%2Fb/c%20d');
  });
});

describe('error classification', () => {
  test('names each state the UI must show', () => {
    expect(classifyError(httpError(409))).toBe('conflict');
    expect(classifyError(httpError(412))).toBe('conflict');
    expect(classifyError(httpError(428))).toBe('conflict');
    expect(classifyError(httpError(403))).toBe('forbidden');
    expect(classifyError(httpError(404))).toBe('missing');
    expect(classifyError(httpError(413))).toBe('too-large');
    expect(classifyError(httpError(500))).toBe('error');
    expect(classifyError(new Error('network'))).toBe('offline');
  });

  test('messages explain what happened', () => {
    expect(errorMessage('conflict')).toMatch(/changed on the server/i);
    expect(errorMessage('offline')).toMatch(/kept on this device/i);
    expect(
      errorMessage('forbidden', {
        response: { data: { error: 'no write access' } },
      }),
    ).toBe('no write access');
  });
});

describe('envelopes', () => {
  test('etags are read from header bags and Maps', () => {
    expect(readETag({ headers: { etag: '"3"' } })).toBe('"3"');
    expect(readETag({ headers: new Map([['etag', '"4"']]) })).toBe('"4"');
    expect(readETag({})).toBeNull();
  });

  test('an envelope carries metadata, document, history and cursor', () => {
    const envelope = readEnvelope({
      data: {
        draft: { id: 'd1', owner: 'alice', cursor: 2 },
        document: { id: 'doc' },
        history: [{ id: 's1' }],
      },
      headers: { etag: '"7"' },
    });

    expect(envelope.draft.owner).toBe('alice');
    expect(envelope.document.id).toBe('doc');
    expect(envelope.cursor).toBe(2);
    expect(envelope.etag).toBe('"7"');
  });
});

describe('client', () => {
  test('a snapshot is appended with If-Match, never a whole draft PUT', async () => {
    const { doc } = sampleDocument();
    const http = fakeHttp({
      'post builder/drafts/alice/d1/snapshots': {
        data: { draft: { id: 'd1' }, document: doc },
        headers: { etag: '"2"' },
      },
    });
    const api = createBuilderApi(http);

    const envelope = await api.appendSnapshot(
      'alice',
      'd1',
      { document: doc, summary: 'Added a device' },
      '"1"',
    );

    expect(http.calls).toHaveLength(1);
    expect(http.calls[0].method).toBe('post');
    expect(http.calls[0].body.document).toEqual(doc);
    expect(http.calls[0].body.summary).toBe('Added a device');
    expect(http.calls[0].config.headers['If-Match']).toBe('"1"');
    expect(envelope.etag).toBe('"2"');
    expect(http.put).toBeUndefined();
  });

  test('the cursor moves with PATCH and If-Match', async () => {
    const http = fakeHttp();
    const api = createBuilderApi(http);

    await api.moveCursor('alice', 'd1', { index: 4 }, '"9"');

    expect(http.calls[0]).toMatchObject({
      method: 'patch',
      url: 'builder/drafts/alice/d1/cursor',
      body: { index: 4 },
    });
    expect(http.calls[0].config.headers['If-Match']).toBe('"9"');
  });

  test('deleting a draft sends If-Match', async () => {
    const http = fakeHttp();
    const api = createBuilderApi(http);

    await api.deleteDraft('alice', 'd1', '"3"');

    expect(http.calls[0].method).toBe('delete');
    expect(http.calls[0].body.headers['If-Match']).toBe('"3"');
  });

  test('publishing sends only the intent, never the document', async () => {
    const http = fakeHttp({
      'post builder/drafts/alice/d1/publish': {
        data: { stages: [{ name: 'topology', status: 'created' }] },
        headers: { etag: '"5"' },
      },
    });
    const api = createBuilderApi(http);
    const { doc } = sampleDocument();
    const result = await api.publish(
      'alice',
      'd1',
      {
        mode: 'topology',
        topology: { name: 'core', action: 'create' },
        document: doc,
      },
      '"4"',
    );

    expect(http.calls[0].config.headers['If-Match']).toBe('"4"');
    expect(http.calls[0].body).toEqual({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });
    expect(http.calls[0].body.document).toBeUndefined();
    expect(result.etag).toBe('"5"');
    expect(result.result.ok).toBe(true);
  });

  test('a non-success partial response remains inspectable', async () => {
    const error = Object.assign(new Error('partial publication'), {
      response: {
        status: 500,
        data: {
          status: 'partial',
          stages: [
            { name: 'document', status: 'created' },
            { name: 'topology', status: 'failed' },
          ],
        },
        headers: { etag: '"4"' },
      },
    });
    const http = fakeHttp({
      'post builder/drafts/alice/d1/publish': error,
    });

    const response = await createBuilderApi(http).publish(
      'alice',
      'd1',
      {
        mode: 'topology',
        topology: { name: 'core', action: 'create' },
      },
      '"4"',
    );

    expect(response.result.partial).toBe(true);
    expect(response.result.failed).toHaveLength(1);
    expect(response.etag).toBe('"4"');
  });

  test('a publish intent keeps the scenario and experiment targets', () => {
    expect(
      publishIntent({
        mode: 'topology-experiment',
        topology: { name: ' core ', action: 'update' },
        scenario: {
          name: 'sc',
          action: 'update',
          expectedDigest: `sha256:${'a'.repeat(64)}`,
        },
        experiment: { name: 'exp', action: 'create' },
      }),
    ).toEqual({
      mode: 'topology-experiment',
      topology: { name: 'core', action: 'update' },
      scenario: {
        name: 'sc',
        action: 'update',
        expectedDigest: `sha256:${'a'.repeat(64)}`,
      },
      experiment: { name: 'exp', action: 'create' },
    });
  });

  test('a publish intent refuses missing or unusable targets', () => {
    expect(() => publishIntent({ mode: 'topology' })).toThrow(/topology name/i);
    expect(() =>
      publishIntent({ topology: { name: 'core', action: 'delete' } }),
    ).toThrow(/topology name/i);
    expect(() =>
      publishIntent({
        mode: 'topology-experiment',
        topology: { name: 'core', action: 'create' },
      }),
    ).toThrow(/experiment name/i);
    expect(
      publishIntent({
        mode: 'topology',
        topology: { name: 'core', action: 'create' },
        scenario: { name: 'sc', action: 'launch' },
      }).scenario,
    ).toBeUndefined();
  });

  test('a partial publish is reported as partial, not as success', () => {
    const result = readPublishResult({
      data: {
        stages: [
          { name: 'topology', status: 'created' },
          { name: 'experiment', status: 'failed', error: 'name taken' },
        ],
        warnings: ['check the image'],
      },
    });

    expect(result.status).toBe('partial');
    expect(result.partial).toBe(true);
    expect(result.ok).toBe(false);
    expect(result.failed).toHaveLength(1);
    expect(result.failed[0].message).toBe('name taken');
    expect(result.warnings).toEqual(['check the image']);
  });

  test('a publish with no stage results is a success', () => {
    const result = readPublishResult({ data: { topology: { name: 'core' } } });

    expect(result.status).toBe('succeeded');
    expect(result.ok).toBe(true);
    expect(result.topology).toEqual({ name: 'core' });
  });

  test('drafts are grouped into mine, shared and published', async () => {
    const http = fakeHttp({
      'get builder/drafts': {
        data: { drafts: [{ id: 'a' }], shared: [{ id: 'b' }] },
        headers: {},
      },
    });

    await expect(createBuilderApi(http).listDrafts()).resolves.toEqual({
      mine: [{ id: 'a' }],
      shared: [{ id: 'b' }],
      published: [],
    });
  });

  test('generate returns the document and its warnings', async () => {
    const http = fakeHttp({
      'post builder/generate': {
        data: { document: { id: 'x' }, warnings: ['dropped a field'] },
        headers: {},
      },
    });

    await expect(
      createBuilderApi(http).generate({ kind: 'topology', name: 'core' }),
    ).resolves.toEqual({
      document: { id: 'x' },
      warnings: ['dropped a field'],
      source: null,
    });
    expect(http.calls[0].body).toEqual({ source: 'topology/core' });
  });

  test('generate sends uploaded config content without a source token', async () => {
    const http = fakeHttp({
      'post builder/generate': {
        data: {
          document: { id: 'x' },
          warnings: [],
          source: { stored: false },
        },
        headers: {},
      },
    });
    const content = 'apiVersion: phenix.sandia.gov/v1\nkind: Topology\n';

    await createBuilderApi(http).generate({ content });

    expect(http.calls[0].body).toEqual({ content });
  });

  test('sources always report every catalog', async () => {
    const http = fakeHttp({
      'get builder/sources': { data: { topologies: ['a'] }, headers: {} },
    });

    await expect(createBuilderApi(http).getSources()).resolves.toEqual({
      images: [],
      topologies: ['a'],
      scenarios: [],
      experiments: [],
    });
  });
});
