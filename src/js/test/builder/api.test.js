import { readFileSync } from 'node:fs';

import { describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import {
  classifyError,
  configPath,
  createBuilderApi,
  cursorPath,
  DISKS_PATH,
  documentPath,
  DOCUMENTS_PATH,
  DRAFTS_PATH,
  draftPath,
  errorMessage,
  EXPORT_TOPOLOGY_PATH,
  GENERATE_PATH,
  MAX_REQUEST_BYTES,
  publishIntent,
  publishPath,
  readEnvelope,
  readPublishResult,
  readETag,
  readShares,
  SCHEMA_PATH,
  serverReason,
  shareCandidatesPath,
  shareErrors,
  sharesPath,
  snapshotPath,
  SOURCES_PATH,
  TooLargeError,
  watchSession,
} from '@/builder/api.js';
import { MAX_DOCUMENT_BYTES } from '@/builder/limits.js';

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
    put: handler('put'),
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
    expect(DRAFTS_PATH).toBe('builder-v2/drafts');
    expect(SOURCES_PATH).toBe('builder-v2/sources');
    expect(GENERATE_PATH).toBe('builder-v2/generate');
    expect(EXPORT_TOPOLOGY_PATH).toBe('builder-v2/export/topology');
    expect(DOCUMENTS_PATH).toBe('builder-v2/documents');
    expect(SCHEMA_PATH).toBe('schemas/builder-v2/v1');
    expect(draftPath('alice', 'd1')).toBe('builder-v2/drafts/alice/d1');
    expect(snapshotPath('alice', 'd1')).toBe(
      'builder-v2/drafts/alice/d1/snapshots',
    );
    expect(snapshotPath('alice', 'd1', 's2')).toBe(
      'builder-v2/drafts/alice/d1/snapshots/s2',
    );
    expect(cursorPath('alice', 'd1')).toBe('builder-v2/drafts/alice/d1/cursor');
    expect(publishPath('alice', 'd1')).toBe(
      'builder-v2/drafts/alice/d1/publish',
    );
    expect(sharesPath('alice', 'd1')).toBe('builder-v2/drafts/alice/d1/shares');
    expect(shareCandidatesPath('alice', 'd1')).toBe(
      'builder-v2/drafts/alice/d1/shares/candidates',
    );
    expect(documentPath('doc 1')).toBe('builder-v2/documents/doc%201');
  });

  test('owner and id are URL encoded', () => {
    expect(draftPath('a/b', 'c d')).toBe('builder-v2/drafts/a%2Fb/c%20d');
  });
});

// The main UI loads the API client to send saves left unsent, from a chunk
// it shares with the Builder that the server does not compress. What the
// client imports from src/builder goes into that chunk, so it must not reach
// the document decoder or validator, which only the Builder uses.
describe('imports', () => {
  test('the client does not reach the document decoder or validator', () => {
    const builder = new URL('../../src/builder/', import.meta.url);
    const seen = new Set();
    const visit = (url) => {
      if (seen.has(url.href)) {
        return;
      }
      seen.add(url.href);
      const source = readFileSync(url, 'utf8');
      for (const [, from] of source.matchAll(
        /^import\b[^;]*?\bfrom '(\.{1,2}\/[^']+)';/gm,
      )) {
        visit(new URL(from, url));
      }
    };
    visit(new URL('api.js', builder));
    const files = [...seen].map((href) => href.slice(builder.href.length));

    expect(files).toContain('limits.js');
    expect(files).not.toContain('decode.js');
    expect(files).not.toContain('validate.js');
  });
});

describe('error classification', () => {
  test('names each state the UI must show', () => {
    expect(classifyError(httpError(409))).toBe('conflict');
    expect(classifyError(httpError(412))).toBe('conflict');
    expect(classifyError(httpError(428))).toBe('conflict');
    expect(classifyError(httpError(403))).toBe('forbidden');
    // An ended session is not a refusal of this user.
    expect(classifyError(httpError(401))).toBe('unauthenticated');
    expect(errorMessage('unauthenticated', httpError(401))).toBe(
      'Your session has ended. Sign in again to continue.',
    );
    expect(classifyError(httpError(404))).toBe('missing');
    expect(classifyError(httpError(413))).toBe('too-large');
    expect(classifyError(httpError(400))).toBe('invalid');
    expect(classifyError(httpError(422))).toBe('invalid');
    expect(classifyError(httpError(500))).toBe('error');
    expect(classifyError(new Error('network'))).toBe('offline');
  });

  // The Builder then offers to sign in again (see signin.js).
  test('says when the server refuses the session, and fails as the client does', async () => {
    const ended = vi.fn();
    const http = watchSession(
      fakeHttp({
        'post builder-v2/drafts': httpError(401),
        'get builder-v2/sources': httpError(403),
      }),
      ended,
    );
    const api = createBuilderApi(http);

    await expect(api.createDraft({ document: {} })).rejects.toMatchObject({
      response: { status: 401 },
    });
    expect(ended).toHaveBeenCalledOnce();
    await expect(api.getSources()).rejects.toMatchObject({
      response: { status: 403 },
    });
    await expect(api.listDrafts()).resolves.toMatchObject({ mine: [] });
    expect(ended).toHaveBeenCalledOnce();
  });

  test('messages explain what happened', () => {
    expect(errorMessage('conflict')).toMatch(/changed on the server/i);
    // Nothing here knows whether work was kept locally, so it never says so.
    expect(errorMessage('offline')).not.toMatch(/kept on this device/i);
    expect(errorMessage('offline')).toMatch(/could not be reached/i);
    // The server's reason is shown as a sentence.
    expect(
      errorMessage('forbidden', {
        response: { data: { error: 'no write access' } },
      }),
    ).toBe('No write access.');
  });

  test('a generic server message gives way to the cause, without paths or ids', () => {
    const rejected = (data) => ({ response: { status: 422, data } });

    expect(
      serverReason(
        rejected({
          message:
            'unable to save builder draft e11aa62f-3289-4051-a661-bc2fa63429ba',
          cause:
            'builder: invalid request: document: is not a valid builder document: invalid builder document: nodes[1].device.hostname: duplicate hostname "node-2" (also nodes[0])',
        }),
      ),
    ).toBe('Duplicate hostname "node-2".');
    expect(
      serverReason(
        rejected({ cause: 'hostname a is bad; hostname b is bad; c is bad' }),
      ),
    ).toBe('Hostname a is bad (and 2 more problems).');
    // A message that says what is wrong is used, as a sentence.
    expect(
      serverReason(
        rejected({
          message: 'Scenario configs cannot be opened in the builder',
          cause: 'unsupported config kind for builder document: "Scenario"',
        }),
      ),
    ).toBe('Scenario configs cannot be opened in the builder.');
    expect(
      serverReason(
        rejected({ message: 'the uploaded config is not valid JSON or YAML' }),
      ),
    ).toBe('The uploaded config is not valid JSON or YAML.');
    // Without a cause, the message is used, but never an id.
    expect(
      serverReason(
        rejected({
          message:
            'unable to save builder draft e11aa62f-3289-4051-a661-bc2fa63429ba',
        }),
      ),
    ).toBe('Unable to save builder draft.');
    expect(serverReason(new Error('network'))).toBe('');
    expect(errorMessage('invalid', rejected({}))).toMatch(/invalid/i);
  });

  // A publish refused with 422 says which interfaces to fix: in the message
  // (publishProjectionRefusal in builder_v2_publish.go), or, where the
  // message only names the topology, in the cause.
  test('a refused publish says what to fix', () => {
    const rejected = (data) => ({ response: { status: 422, data } });
    const vlan =
      'interface "eth1" of device "server" has no VLAN: connect it to a network, or type a VLAN for it';

    expect(
      errorMessage(
        'invalid',
        rejected({
          message: `topology lab cannot be published: ${vlan}`,
          cause: `validating topology projection: ${vlan}`,
        }),
      ),
    ).toBe(`Topology lab cannot be published: ${vlan}.`);
    expect(
      serverReason(
        rejected({
          message: 'builder document cannot be published as topology lab',
          cause: `validating topology projection: ${vlan}; interface #2 of device "client" has no VLAN: connect it to a network, or type a VLAN for it`,
        }),
      ),
    ).toBe(`I${vlan.slice(1)} (and 1 more problem).`);
    // A schema error repeats itself for each kind of node it could be.
    const schema =
      'Error at "/nodes/1/network/interfaces/0": Error at "/mtu": value must be an integer';

    expect(
      serverReason(
        rejected({
          message: 'builder document cannot be published as topology lab',
          cause: `validating topology projection: config validation failed: ${schema} | ${schema} | Error at "/nodes/1/external": property "external" is missing`,
        }),
      ),
    ).toBe(`${schema} (and 1 more problem).`);
    expect(
      serverReason(
        rejected({
          message: 'builder document cannot be projected',
          cause:
            'invalid builder document: nodes[1].device.hostname: duplicate hostname "node-2" (also nodes[0])',
        }),
      ),
    ).toBe('Duplicate hostname "node-2".');
  });
});

describe('envelopes', () => {
  test('etags are read from header bags and Maps', () => {
    expect(readETag({ headers: { etag: '"3"' } })).toBe('"3"');
    expect(readETag({ headers: new Map([['etag', '"4"']]) })).toBe('"4"');
    expect(readETag({})).toBeNull();
  });

  // A compressing proxy rewrites the header, which the server's If-Match
  // then refuses; the body keeps the server's own tag.
  test('etags are read from the body before a header a proxy rewrote', () => {
    const draft = { id: 'd1', owner: 'alice', etag: '"3"' };

    expect(readETag({ data: draft, headers: { etag: 'W/"3"' } })).toBe('"3"');
    expect(
      readEnvelope({ data: draft, headers: { etag: '"3-gzip"' } }).etag,
    ).toBe('"3"');
    // A publish result carries it in its draft.
    expect(
      readETag({
        data: { status: 'succeeded', draft: { ...draft, etag: '"5"' } },
        headers: new Map([['etag', 'W/"5"']]),
      }),
    ).toBe('"5"');
    // A body without one falls back to the header.
    expect(readETag({ data: { snapshot: {} }, headers: { etag: '"6"' } })).toBe(
      '"6"',
    );
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
    expect(envelope.history).toEqual([{ id: 's1' }]);
    expect(envelope.cursor).toBe(2);
    expect(envelope.etag).toBe('"7"');

    // A save answers with the draft alone: its history is unknown, not
    // empty.
    const saved = readEnvelope({
      data: { id: 'd1', owner: 'alice', cursor: 1, snapshotId: 's2' },
      headers: { etag: '"8"' },
    });

    expect(saved.history).toBeNull();
    expect(saved.draft.snapshotId).toBe('s2');
  });
});

describe('client', () => {
  test('a snapshot is appended with If-Match, never a whole draft PUT', async () => {
    const { doc } = sampleDocument();
    const http = fakeHttp({
      'post builder-v2/drafts/alice/d1/snapshots': {
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
    // The only PUT is the share list's.
    expect(http.calls.some((call) => call.method === 'put')).toBe(false);
  });

  // The server refuses a body over its limit before reading all of it, and
  // Firefox then reports a reset connection, which read as offline. Such an
  // upload is refused here, before it is sent, and says why.
  test('an upload the server would refuse for its size is not sent', async () => {
    const http = fakeHttp();
    const api = createBuilderApi(http);
    const big = { name: 'big', notes: 'x'.repeat(MAX_DOCUMENT_BYTES) };
    // Two bytes per character as UTF-8: over the limit in bytes only.
    const wide = { name: 'wide', notes: 'é'.repeat(MAX_DOCUMENT_BYTES / 2) };

    for (const request of [
      () => api.appendSnapshot('alice', 'd1', { document: big }, '"1"'),
      () => api.createDraft({ title: 'wide', document: wide }),
      () => api.generate({ content: 'y'.repeat(MAX_DOCUMENT_BYTES + 1) }),
      () => api.exportTopology(big, 'big'),
    ]) {
      const error = await request().catch((failure) => failure);

      expect(error).toBeInstanceOf(TooLargeError);
      expect(classifyError(error)).toBe('too-large');
    }

    expect(http.calls).toEqual([]);
    expect(
      errorMessage(
        'too-large',
        await api
          .appendSnapshot('alice', 'd1', { document: big }, '"1"')
          .catch((failure) => failure),
      ),
    ).toBe('This diagram is larger than the 5 MiB the server accepts.');
    expect(MAX_REQUEST_BYTES).toBe(MAX_DOCUMENT_BYTES + 1024 * 1024);

    // Just under the limit, it is sent.
    const fits = { notes: 'x'.repeat(MAX_DOCUMENT_BYTES - 64) };
    await api.appendSnapshot('alice', 'd1', { document: fits }, '"1"');
    expect(http.calls).toHaveLength(1);
  });

  test('the cursor moves with PATCH and If-Match', async () => {
    const http = fakeHttp();
    const api = createBuilderApi(http);

    await api.moveCursor('alice', 'd1', { index: 4 }, '"9"');

    expect(http.calls[0]).toMatchObject({
      method: 'patch',
      url: 'builder-v2/drafts/alice/d1/cursor',
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

  test('deleting a published diagram deletes its document path', async () => {
    const http = fakeHttp();

    expect(await createBuilderApi(http).deleteDocument('doc 1')).toBe(true);
    expect(http.calls).toEqual([
      expect.objectContaining({
        method: 'delete',
        url: 'builder-v2/documents/doc%201',
      }),
    ]);
  });

  // The answer is the draft, as a save's is: its ETag is read from the body.
  test('deleting a snapshot sends If-Match and reads the draft it answers with', async () => {
    const http = fakeHttp({
      'delete builder-v2/drafts/alice/d1/snapshots/s%202': {
        data: { id: 'd1', cursor: 1, snapshots: 2, etag: '"8"' },
        headers: { etag: 'W/"8"' },
      },
    });
    const envelope = await createBuilderApi(http).deleteSnapshot(
      'alice',
      'd1',
      's 2',
      '"7"',
    );

    expect(http.calls[0].method).toBe('delete');
    expect(http.calls[0].body.headers['If-Match']).toBe('"7"');
    expect(envelope).toMatchObject({
      draft: { id: 'd1', snapshots: 2 },
      history: null,
      cursor: 1,
      etag: '"8"',
    });
  });

  // The share list has a tag of its own, read from the body before the
  // header; an update answers with the draft, whose ETag is in its body and
  // never the share list's header.
  test('share lists are read and replaced with their own tag', async () => {
    const http = fakeHttp({
      'get builder-v2/drafts/al%20ice/d1/shares': {
        data: {
          shares: [
            { user: 'bob', access: 'edit', grantedAt: 't', stale: false },
            { user: 'carol', access: 'view', stale: true },
            { access: 'edit' },
          ],
          sharesEtag: '"shares-3"',
          maxShares: 25,
        },
        headers: { etag: 'W/"shares-3"' },
      },
      'put builder-v2/drafts/al%20ice/d1/shares': {
        data: {
          shares: [{ user: 'bob', access: 'view' }],
          sharesEtag: '"shares-4"',
          maxShares: 25,
          draft: { id: 'd1', owner: 'al ice', etag: '"57"', shares: [] },
        },
        headers: { etag: '"shares-4"' },
      },
    });
    const api = createBuilderApi(http);

    await expect(api.getShares('al ice', 'd1')).resolves.toEqual({
      shares: [
        { user: 'bob', access: 'edit', stale: false, grantedAt: 't' },
        { user: 'carol', access: 'view', stale: true, grantedAt: '' },
      ],
      sharesEtag: '"shares-3"',
      maxShares: 25,
    });
    expect(readShares({ data: {}, headers: { etag: '"shares-0"' } })).toEqual({
      shares: [],
      sharesEtag: '"shares-0"',
      maxShares: 25,
    });

    const saved = await api.updateShares(
      'al ice',
      'd1',
      [{ user: 'bob', access: 'view', stale: false, removed: false }],
      '"shares-3"',
    );

    expect(http.calls[1].method).toBe('put');
    expect(http.calls[1].body).toEqual({
      shares: [{ user: 'bob', access: 'view' }],
    });
    expect(http.calls[1].config.headers['If-Match']).toBe('"shares-3"');
    expect(saved.sharesEtag).toBe('"shares-4"');
    expect(saved.draft.id).toBe('d1');
    expect(saved.etag).toBe('"57"');
  });

  test('a refused share list names each person and why', () => {
    const refused = Object.assign(new Error('status 422'), {
      response: {
        status: 422,
        data: {
          message: 'Some people could not be added.',
          cause: '',
          errors: [
            { user: 'bobb', reason: 'unknown-user' },
            { user: '', reason: 'too-many' },
            { user: 'x' },
          ],
        },
      },
    });

    expect(shareErrors(refused)).toEqual([
      { user: 'bobb', reason: 'unknown-user' },
      { user: '', reason: 'too-many' },
    ]);
    expect(serverReason(refused)).toBe('Some people could not be added.');
    expect(shareErrors(httpError(412))).toEqual([]);
    expect(classifyError(httpError(412))).toBe('conflict');
  });

  test('the users a draft may be shared with are listed by username and name', async () => {
    const candidates = 'get builder-v2/drafts/alice/d1/shares/candidates';
    const http = fakeHttp({
      [candidates]: {
        data: {
          users: [
            { username: 'bob', name: 'Bob Lee ' },
            { username: 'carol', name: '' },
            { username: 'dave' },
            { name: 'No username' },
          ],
        },
      },
    });

    await expect(
      createBuilderApi(http).listShareCandidates('alice', 'd1'),
    ).resolves.toEqual([
      { username: 'bob', name: 'Bob Lee' },
      { username: 'carol', name: '' },
      { username: 'dave', name: '' },
    ]);
    await expect(
      createBuilderApi(
        fakeHttp({ [candidates]: { data: {} } }),
      ).listShareCandidates('alice', 'd1'),
    ).rejects.toThrow(TypeError);
  });

  test('publishing sends only the intent, never the document', async () => {
    const http = fakeHttp({
      'post builder-v2/drafts/alice/d1/publish': {
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
      'post builder-v2/drafts/alice/d1/publish': error,
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

  // Drafts other users shared with the caller are "shared"; those the
  // caller sees through their role alone are "others".
  test('drafts are grouped into mine, shared, others and published, and the damaged are marked', async () => {
    const shared = { id: 'b', owner: 'bob', via: 'share', access: 'edit' };
    const role = { id: 'c', owner: 'carol', via: 'role', access: 'view' };
    const http = fakeHttp({
      'get builder-v2/drafts': {
        data: { drafts: [{ id: 'a' }], shared: [shared, role] },
        headers: {},
      },
    });

    await expect(createBuilderApi(http).listDrafts()).resolves.toEqual({
      mine: [{ id: 'a' }],
      shared: [shared],
      others: [role],
      published: [],
      damaged: [],
    });

    const damaged = { id: 'c', owner: 'alice', etag: '"7"', canDelete: true };
    const withDamaged = fakeHttp({
      'get builder-v2/drafts': {
        data: { drafts: [], shared: [], damaged: [damaged] },
        headers: {},
      },
    });

    await expect(
      createBuilderApi(withDamaged).listDrafts(),
    ).resolves.toMatchObject({ damaged: [{ ...damaged, damaged: true }] });
  });

  test('generate returns the document and its warnings', async () => {
    const http = fakeHttp({
      'post builder-v2/generate': {
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
      'post builder-v2/generate': {
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

  test('a topology export sends the document and reads the YAML and what blocks publishing', async () => {
    const { doc } = sampleDocument();
    const yaml = 'apiVersion: phenix.sandia.gov/v1\nkind: Topology\n';
    const http = fakeHttp({
      'post builder-v2/export/topology': {
        data: {
          name: 'Sample',
          yaml,
          warnings: ['dropped a connection'],
          publishBlockers: ['interface "eth0" of device "alpha" has no VLAN'],
        },
        headers: {},
      },
    });
    const api = createBuilderApi(http);

    await expect(api.exportTopology(doc, 'Sample')).resolves.toEqual({
      name: 'Sample',
      yaml,
      warnings: ['dropped a connection'],
      publishBlockers: ['interface "eth0" of device "alpha" has no VLAN'],
    });
    expect(http.calls[0].body).toEqual({ document: doc, name: 'Sample' });

    // Without a name, the server names it.
    await api.exportTopology(doc, '');
    expect(http.calls[1].body).toEqual({ document: doc });

    const odd = createBuilderApi(
      fakeHttp({
        'post builder-v2/export/topology': { data: {}, headers: {} },
      }),
    );

    await expect(odd.exportTopology(doc)).rejects.toThrow(
      'The server sent an unexpected topology.',
    );
  });

  test('sources always report every catalog', async () => {
    const http = fakeHttp({
      'get builder-v2/sources': { data: { topologies: ['a'] }, headers: {} },
    });

    await expect(createBuilderApi(http).getSources()).resolves.toEqual({
      images: [],
      topologies: ['a'],
      scenarios: [],
      experiments: [],
    });
  });

  test('disk images are listed by file name, from GET disks', async () => {
    const http = fakeHttp({
      'get disks': {
        data: {
          disks: [
            { kind: 'VM', name: 'ubuntu.qc2', fullPath: '/p/ubuntu.qc2' },
            { kind: 'Container', name: 'box_rootfs.tgz' },
            { kind: 'ISO', name: 'ubuntu.qc2' },
            { kind: 'Unknown', name: '' },
          ],
        },
        headers: {},
      },
    });

    await expect(createBuilderApi(http).listDisks()).resolves.toEqual([
      'ubuntu.qc2',
      'box_rootfs.tgz',
    ]);
    expect(http.calls).toEqual([
      expect.objectContaining({ method: 'get', url: DISKS_PATH }),
    ]);
    expect(DISKS_PATH).toBe('disks');
  });

  test('a stored scenario is read from its config, as JSON', async () => {
    const spec = { apps: [{ name: 'ntp' }] };
    const http = fakeHttp({
      'get configs/Scenario/ntp%20scn': { data: { spec }, headers: {} },
      'get configs/Scenario/other': { data: 'nope', headers: {} },
    });

    await expect(createBuilderApi(http).getScenario('ntp scn')).resolves.toBe(
      spec,
    );
    expect(http.calls[0].config).toEqual({
      headers: { Accept: 'application/json' },
    });
    expect(configPath('Scenario', 'a/b')).toBe('configs/Scenario/a%2Fb');
    await expect(createBuilderApi(http).getScenario('other')).rejects.toThrow(
      /unexpected scenario/,
    );
  });

  test('a disk listing of another shape is refused', async () => {
    const http = fakeHttp({ 'get disks': { data: 'forbidden', headers: {} } });

    await expect(createBuilderApi(http).listDisks()).rejects.toThrow(
      /unexpected disk listing/,
    );
  });
});
