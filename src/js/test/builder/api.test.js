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
  experimentError,
  experimentPath,
  EXPORT_TOPOLOGY_PATH,
  fileHandle,
  fileTopology,
  GENERATE_PATH,
  LEGACY_PATH,
  MAX_REQUEST_BYTES,
  MAX_SOURCE_FILE_BYTES,
  publishIntent,
  publishPath,
  preconditionETag,
  readEnvelope,
  readIssues,
  readPublishResult,
  readETag,
  readShares,
  SCHEMA_PATH,
  serverCode,
  serverReason,
  serverRefusal,
  serverSentence,
  shareCandidatesPath,
  shareErrors,
  sharesPath,
  snapshotPath,
  sourceFileName,
  SOURCES_PATH,
  TooLargeError,
  topologyDocumentPath,
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
    expect(DRAFTS_PATH).toBe('builder/drafts');
    expect(SOURCES_PATH).toBe('builder/sources');
    expect(GENERATE_PATH).toBe('builder/generate');
    expect(LEGACY_PATH).toBe('builder/legacy');
    expect(EXPORT_TOPOLOGY_PATH).toBe('builder/export/topology');
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
    expect(sharesPath('alice', 'd1')).toBe('builder/drafts/alice/d1/shares');
    expect(shareCandidatesPath('alice', 'd1')).toBe(
      'builder/drafts/alice/d1/shares/candidates',
    );
    expect(documentPath('doc 1')).toBe('builder/documents/doc%201');
    expect(topologyDocumentPath('core')).toBe(
      'builder/topologies/core/document',
    );
  });

  test('owner and id are URL encoded', () => {
    expect(draftPath('a/b', 'c d')).toBe('builder/drafts/a%2Fb/c%20d');
    expect(topologyDocumentPath('lab@site 1')).toBe(
      'builder/topologies/lab%40site%201/document',
    );
  });

  // A topology read from its Builder file has no document id: its row is
  // named by a handle no id can be.
  test('a file handle names its topology, and a document id names none', () => {
    expect(fileHandle('core')).toBe('file/core');
    expect(fileTopology('file/core')).toBe('core');
    expect(fileTopology(fileHandle('a.b-c_d@e'))).toBe('a.b-c_d@e');
    expect(fileTopology('f'.repeat(64))).toBe('');
    expect(fileTopology('')).toBe('');
    expect(fileTopology(undefined)).toBe('');
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
        'post builder/drafts': httpError(401),
        'get builder/sources': httpError(403),
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
        rejected({ message: 'the config file is not valid JSON or YAML' }),
      ),
    ).toBe('The config file is not valid JSON or YAML.');
    // Without a cause, the message is used, but never an id.
    expect(
      serverReason(
        rejected({
          message:
            'unable to save builder draft e11aa62f-3289-4051-a661-bc2fa63429ba',
        }),
      ),
    ).toBe('Unable to save builder draft.');
    // A draft is named by its owner and its id: the owner goes with the id.
    expect(
      serverReason(
        rejected({
          message: 'draft alice/e11aa62f-3289-4051-a661-bc2fa63429ba not found',
        }),
      ),
    ).toBe('Draft not found.');
    expect(
      serverReason(
        rejected({
          message:
            'deleting draft a.b@c/e11aa62f-3289-4051-a661-bc2fa63429ba not allowed for alice',
        }),
      ),
    ).toBe('Deleting draft not allowed for alice.');
    // An id inside a longer path takes nothing before it along.
    expect(
      serverReason(
        rejected({
          message:
            'file /phenix/topologies/e11aa62f-3289-4051-a661-bc2fa63429ba/lab.json is missing',
        }),
      ),
    ).toBe('File /phenix/topologies//lab.json is missing.');
    expect(serverReason(new Error('network'))).toBe('');
    expect(errorMessage('invalid', rejected({}))).toMatch(/invalid/i);
  });

  // A publish refused with 422 says which interfaces to fix: in the message
  // (publishProjectionRefusal in builder_publish.go), or, where the
  // message only names the topology, in the cause.
  // Why a Builder file cannot be used is written to be shown: the path it
  // names is kept whole, where serverReason() would take an id out of it.
  test('a sentence written to be shown is shown word for word', () => {
    const refused = (status, message) =>
      Object.assign(new Error('refused'), {
        response: { status, data: { message, cause: '' } },
      });
    const path =
      '/phenix/topologies/0f8fad5b-d9cb-469f-a165-70867728950e/lab.builder.json';
    const missing = refused(
      404,
      `Builder file ${path} does not exist on this phenix server.`,
    );

    expect(serverSentence(missing)).toBe(
      `Builder file ${path} does not exist on this phenix server.`,
    );
    expect(serverReason(missing)).not.toContain('0f8fad5b');
    expect(
      serverSentence(
        refused(404, 'builder document of topology core not found'),
      ),
    ).toBe('Builder document of topology core not found.');
    expect(serverSentence(refused(500, ''))).toBe('');
    expect(serverSentence(new Error('offline'))).toBe('');
    expect(
      serverSentence({ response: { status: 502, data: '<html>Bad gateway' } }),
    ).toBe('');
  });

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

  test('a refused precondition gives the draft’s current etag, for the request to be sent again', () => {
    const refused = (headers, status = 412) => ({
      response: { status, headers },
    });

    expect(preconditionETag(refused({ etag: '"7"' }))).toBe('"7"');
    expect(preconditionETag(refused({ ETag: ' "8" ' }))).toBe('"8"');
    expect(preconditionETag(refused(new Map([['etag', '"9"']])))).toBe('"9"');
    // No header, or no tag in it: the caller reads the draft instead.
    expect(preconditionETag(refused({}))).toBeNull();
    expect(preconditionETag(refused(undefined))).toBeNull();
    expect(preconditionETag(refused({ etag: '' }))).toBeNull();
    expect(preconditionETag(refused({ etag: '*' }))).toBeNull();
    // A tag a compressing proxy rewrote is not the draft's.
    expect(preconditionETag(refused({ etag: 'W/"7"' }))).toBeNull();
    expect(preconditionETag(refused({ etag: '"7-gzip"' }))).toBeNull();
    // Only a refused precondition carries it.
    expect(preconditionETag(refused({ etag: '"7"' }, 409))).toBeNull();
    expect(preconditionETag(new Error('Network Error'))).toBeNull();
    expect(preconditionETag(null)).toBeNull();
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
        document: { metadata: { id: 'doc' } },
        history: [{ id: 's1' }],
      },
      headers: { etag: '"7"' },
    });

    expect(envelope.draft.owner).toBe('alice');
    expect(envelope.document.metadata.id).toBe('doc');
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
      () => api.convertLegacy({ content: '<'.repeat(MAX_DOCUMENT_BYTES + 1) }),
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
    expect(
      errorMessage(
        'too-large',
        await api
          .generate({ content: 'y'.repeat(MAX_DOCUMENT_BYTES + 1) })
          .catch((failure) => failure),
      ),
    ).toBe('The config file is larger than the 5 MiB the server accepts.');
    expect(
      errorMessage(
        'too-large',
        await api
          .convertLegacy({
            content: '<'.repeat(MAX_DOCUMENT_BYTES + 1),
            name: 'plant',
          })
          .catch((failure) => failure),
      ),
    ).toBe('The legacy diagram is larger than the 5 MiB the server accepts.');
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

  test('deleting a published diagram deletes its document path', async () => {
    const http = fakeHttp();

    expect(await createBuilderApi(http).deleteDocument('doc 1')).toBe(true);
    expect(http.calls).toEqual([
      expect.objectContaining({
        method: 'delete',
        url: 'builder/documents/doc%201',
      }),
    ]);
  });

  // The answer is the draft, as a save's is: its ETag is read from the body.
  test('deleting a snapshot sends If-Match and reads the draft it answers with', async () => {
    const http = fakeHttp({
      'delete builder/drafts/alice/d1/snapshots/s%202': {
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
      'get builder/drafts/al%20ice/d1/shares': {
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
      'put builder/drafts/al%20ice/d1/shares': {
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
    const candidates = 'get builder/drafts/alice/d1/shares/candidates';
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

  test('a publish intent keeps the experiment and its scenario, by name alone', () => {
    expect(
      publishIntent({
        mode: 'topology-experiment',
        topology: { name: ' core ', action: 'update' },
        scenario: {
          name: ' sc ',
          action: 'update',
          expectedDigest: `sha256:${'a'.repeat(64)}`,
        },
        experiment: { name: 'exp', action: 'create' },
      }),
    ).toEqual({
      mode: 'topology-experiment',
      topology: { name: 'core', action: 'update' },
      scenario: { name: 'sc' },
      experiment: { name: 'exp', action: 'create' },
    });

    // No scenario is no scenario key: the experiment then has none.
    expect(
      publishIntent({
        mode: 'topology-experiment',
        topology: { name: 'core', action: 'create' },
        scenario: { name: '' },
        experiment: { name: 'exp', action: 'create' },
      }),
    ).not.toHaveProperty('scenario');
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
    // A topology alone names no scenario: the server refuses one there.
    expect(
      publishIntent({
        mode: 'topology',
        topology: { name: 'core', action: 'create' },
        scenario: { name: 'sc' },
      }).scenario,
    ).toBeUndefined();
  });

  test('a partial publish is reported as partial, not as success', () => {
    const failure = {
      code: 'publish.stage.failed',
      severity: 'error',
      message: 'experiment publication failed',
    };
    const result = readPublishResult({
      data: {
        stages: [
          { name: 'topology', status: 'created' },
          { name: 'experiment', status: 'failed', error: 'name taken' },
        ],
        warnings: [
          {
            code: 'publish.broadcast.failed',
            severity: 'warning',
            message: 'check the image',
          },
        ],
        errors: [failure],
      },
    });

    expect(result.status).toBe('partial');
    expect(result.partial).toBe(true);
    expect(result.ok).toBe(false);
    expect(result.failed).toHaveLength(1);
    expect(result.failed[0].message).toBe('name taken');
    expect(result.warnings).toEqual(['check the image']);
    expect(result.warningIssues).toEqual([
      {
        code: 'publish.broadcast.failed',
        severity: 'warning',
        message: 'check the image',
      },
    ]);
    expect(result.errors).toEqual(['experiment publication failed']);
    expect(result.errorIssues).toEqual([failure]);
  });

  // Issues each carry their code; a plain message of a server of an earlier
  // release is an issue of no code, and an entry with no message none.
  test('issues are read with their codes and severities', () => {
    expect(
      readIssues(
        [
          'plain words',
          { code: 'node.hostname.duplicate', message: 'twice', nodeId: 'n1' },
          { code: 'publish.broadcast.failed', severity: 'odd', message: 'x' },
          { code: 'no.message.here' },
          '',
          7,
        ],
        'warning',
      ),
    ).toEqual([
      { code: '', severity: 'warning', message: 'plain words' },
      {
        code: 'node.hostname.duplicate',
        severity: 'warning',
        message: 'twice',
        nodeId: 'n1',
      },
      { code: 'publish.broadcast.failed', severity: 'warning', message: 'x' },
    ]);
    expect(readIssues(undefined)).toEqual([]);
  });

  test('a refusal is read with its code, its reason and its scenario', () => {
    const refused = (data) => ({ response: { status: 422, data } });

    expect(
      serverRefusal(
        refused({
          code: 'publish.scenario.missing',
          message: 'scenario ntp does not exist',
          cause: '',
          metadata: { scenario: 'ntp' },
        }),
      ),
    ).toEqual({
      code: 'publish.scenario.missing',
      reason: 'Scenario ntp does not exist.',
      scenario: 'ntp',
    });
    expect(serverCode(refused({ message: 'x' }))).toBe('');
    expect(serverRefusal(undefined)).toEqual({
      code: '',
      reason: '',
      scenario: '',
    });
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
      'get builder/drafts': {
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
      'get builder/drafts': {
        data: { drafts: [], shared: [], damaged: [damaged] },
        headers: {},
      },
    });

    await expect(
      createBuilderApi(withDamaged).listDrafts(),
    ).resolves.toMatchObject({ damaged: [{ ...damaged, damaged: true }] });
  });

  test('a topology read from its Builder file is listed under a handle', async () => {
    const stored = {
      source: 'store',
      id: 'a'.repeat(64),
      digest: `sha256:${'b'.repeat(64)}`,
      size: 512,
      target: 'core',
      kind: 'Topology',
      config: 'Topology/core',
      draftId: 'd1',
      createdAt: '2026-10-01T15:04:05Z',
      createdBy: 'alice',
    };
    const file = {
      source: 'file',
      target: 'plant',
      kind: 'Topology',
      config: 'Topology/plant',
      path: '/phenix/topologies/plant/plant.builder.json',
    };
    const http = fakeHttp({
      [DOCUMENTS_PATH]: { data: { documents: [stored, file] } },
    });
    const documents = await createBuilderApi(http).listDocuments();

    // A stored row is as the server sent it; a file row gains only its id.
    expect(documents).toEqual([stored, { ...file, id: 'file/plant' }]);
    expect(fileTopology(documents[1].id)).toBe('plant');

    await expect(
      createBuilderApi(
        fakeHttp({ [DOCUMENTS_PATH]: { data: {} } }),
      ).listDocuments(),
    ).resolves.toEqual([]);
  });

  test('the document a topology references is read by its name', async () => {
    const { doc } = sampleDocument();
    const row = {
      source: 'file',
      target: 'plant',
      kind: 'Topology',
      config: 'Topology/plant',
      path: '/phenix/topologies/plant/plant.builder.yaml',
      digest: `sha256:${'c'.repeat(64)}`,
      size: 2048,
      document: doc,
    };
    const http = fakeHttp({
      'builder/topologies/plant/document': {
        data: { ...row, topologyDiffers: true },
      },
      'builder/topologies/core/document': {
        data: { ...row, source: 'store', id: 'p1', target: 'core', path: '' },
      },
    });
    const api = createBuilderApi(http);

    await expect(api.getTopologyDocument('plant')).resolves.toEqual({
      ...row,
      id: 'file/plant',
      topologyDiffers: true,
    });
    expect(http.calls[0]).toMatchObject({
      method: 'get',
      url: 'builder/topologies/plant/document',
    });

    // A stored document keeps its id, and a row that does not say the
    // topology differs does not differ.
    const stored = await api.getTopologyDocument('core');

    expect(stored.id).toBe('p1');
    expect(stored.topologyDiffers).toBe(false);
  });

  test('a draft records the name of the uploaded file only when the server would', () => {
    expect(sourceFileName('plant.builder.json')).toBe('plant.builder.json');
    expect(sourceFileName('Übung 1 (final).yaml')).toBe('Übung 1 (final).yaml');
    expect(sourceFileName('a'.repeat(MAX_SOURCE_FILE_BYTES))).toBe(
      'a'.repeat(MAX_SOURCE_FILE_BYTES),
    );
    // What ValidateSourceFile refuses is left out, not sent.
    expect(sourceFileName('a'.repeat(MAX_SOURCE_FILE_BYTES + 1))).toBe('');
    // Bytes, not characters: 86 three-byte characters are 258 bytes.
    expect(sourceFileName('€'.repeat(86))).toBe('');
    expect(sourceFileName('dir/plant.json')).toBe('');
    expect(sourceFileName('dir\\plant.json')).toBe('');
    expect(sourceFileName('.')).toBe('');
    expect(sourceFileName('..')).toBe('');
    expect(sourceFileName('plant\n.json')).toBe('');
    expect(sourceFileName('plant\u007f.json')).toBe('');
    expect(sourceFileName('')).toBe('');
    expect(sourceFileName(undefined)).toBe('');
    // A name that only starts with dots is a file name.
    expect(sourceFileName('...json')).toBe('...json');
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

  test('generate sends each import choice only when it is made', async () => {
    const http = fakeHttp({
      'post builder/generate': {
        data: { document: { id: 'x' }, warnings: [] },
        headers: {},
      },
    });
    const api = createBuilderApi(http);
    const stored = { kind: 'Topology', name: 'site' };
    const sent = () => http.calls.at(-1).body;

    // A choice that was not offered, or not made, is left out.
    await api.generate({ ...stored, copy: false, newName: '', includes: '' });
    expect(sent()).toEqual({ source: 'Topology/site' });

    await api.generate({ ...stored, includes: 'keep' });
    expect(sent()).toEqual({ source: 'Topology/site', includes: 'keep' });

    await api.generate({ ...stored, copy: true, newName: 'site-copy' });
    expect(sent()).toEqual({
      source: 'Topology/site',
      copy: true,
      name: 'site-copy',
    });

    await api.generate({
      ...stored,
      includes: 'combine',
      newName: 'site-combined',
    });
    expect(sent()).toEqual({
      source: 'Topology/site',
      includes: 'combine',
      name: 'site-combined',
    });

    // A config file takes the same choices, and names no stored source.
    await api.generate({
      content: 'kind: Topology',
      includes: 'combine',
      newName: 'file-combined',
    });
    expect(sent()).toEqual({
      content: 'kind: Topology',
      includes: 'combine',
      name: 'file-combined',
    });
  });

  test('convertLegacy posts the file text and the name to builder/legacy', async () => {
    const content = '<mxGraphModel><root/></mxGraphModel>';
    const http = fakeHttp({
      'post builder/legacy': {
        data: {
          document: { id: 'x' },
          warnings: ['The diagram has no nodes.'],
        },
        headers: {},
      },
    });

    // A diagram alone comes back without a source.
    await expect(
      createBuilderApi(http).convertLegacy({ content, name: 'plant' }),
    ).resolves.toEqual({
      document: { id: 'x' },
      warnings: ['The diagram has no nodes.'],
      source: null,
    });
    expect(http.calls).toHaveLength(1);
    expect(http.calls[0].method).toBe('post');
    expect(http.calls[0].url).toBe('builder/legacy');
    expect(http.calls[0].body).toEqual({ content, name: 'plant' });
  });

  test('convertLegacy leaves out an empty name and reads the source of a Topology config', async () => {
    const source = {
      kind: 'Topology',
      name: 'plant',
      fullName: 'Topology/plant',
      stored: false,
      builder: 'builder-xml',
    };
    const http = fakeHttp({
      'post builder/legacy': {
        data: { document: { id: 'x' }, warnings: [], source },
        headers: {},
      },
    });
    const content = 'kind: Topology\n';

    await expect(
      createBuilderApi(http).convertLegacy({ content }),
    ).resolves.toEqual({ document: { id: 'x' }, warnings: [], source });
    // The request is strict: it carries the two fields the route reads.
    expect(http.calls[0].body).toEqual({ content });

    await createBuilderApi(http).convertLegacy({ content, name: '' });
    expect(http.calls[1].body).toEqual({ content });
  });

  test('exportTopology sends the document and reads the YAML and what blocks publishing', async () => {
    const { doc } = sampleDocument();
    const yaml = 'apiVersion: phenix.sandia.gov/v1\nkind: Topology\n';
    const warning = {
      code: 'interface.name.unmatched',
      severity: 'warning',
      message: 'dropped a connection',
      path: 'nodes[1].device.interfaces[0].name',
      nodeId: 'n1',
    };
    const blocker = {
      code: 'interface.vlan.missing',
      severity: 'error',
      message: 'interface "eth0" of device "alpha" has no VLAN',
    };
    const http = fakeHttp({
      'post builder/export/topology': {
        data: {
          name: 'Sample',
          yaml,
          warnings: [warning],
          publishBlockers: [blocker],
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
      warningIssues: [warning],
      publishBlockerIssues: [blocker],
    });
    expect(http.calls[0].body).toEqual({ document: doc, name: 'Sample' });

    // Without a name, the server names it.
    await api.exportTopology(doc, '');
    expect(http.calls[1].body).toEqual({ document: doc });

    const odd = createBuilderApi(
      fakeHttp({
        'post builder/export/topology': { data: {}, headers: {} },
      }),
    );

    await expect(odd.exportTopology(doc)).rejects.toThrow(
      'The server sent an unexpected topology.',
    );
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

  test('a stored config is read whole, its annotations with it', async () => {
    const config = {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'ntp scn', annotations: { topology: 'plant' } },
      spec: { apps: [] },
    };
    const http = fakeHttp({
      'get configs/Scenario/ntp%20scn': { data: config, headers: {} },
      'get configs/Scenario/other': { data: { spec: {} }, headers: {} },
    });
    const api = createBuilderApi(http);

    await expect(api.getConfig('Scenario', 'ntp scn')).resolves.toBe(config);
    expect(http.calls[0].config).toEqual({
      headers: { Accept: 'application/json' },
    });
    await expect(api.getConfig('Scenario', 'other')).rejects.toThrow(
      'The server sent an unexpected Scenario config.',
    );
  });

  // As the Configs page stores a config: JSON text, typed as JSON.
  test('a config is created with POST configs and replaced with PUT', async () => {
    const config = {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'ntp scn' },
      spec: { apps: [] },
    };
    const http = fakeHttp({
      'post configs': { data: {}, headers: {} },
      'put configs/Scenario/ntp%20scn': { data: {}, headers: {} },
    });
    const api = createBuilderApi(http);

    await api.createConfig(config);
    await api.updateConfig(config);

    expect(http.calls).toEqual([
      expect.objectContaining({
        method: 'post',
        url: 'configs',
        body: JSON.stringify(config),
        config: { headers: { 'Content-Type': 'application/json' } },
      }),
      expect.objectContaining({
        method: 'put',
        url: 'configs/Scenario/ntp%20scn',
        body: JSON.stringify(config),
        config: { headers: { 'Content-Type': 'application/json' } },
      }),
    ]);
  });

  test('a disk listing of another shape is refused', async () => {
    const http = fakeHttp({ 'get disks': { data: 'forbidden', headers: {} } });

    await expect(createBuilderApi(http).listDisks()).rejects.toThrow(
      /unexpected disk listing/,
    );
  });
});

describe('the experiment a diagram was published with', () => {
  test('is read from GET experiments/NAME, for whether it is running', async () => {
    const http = fakeHttp({
      'get experiments/lab-exp': {
        data: { name: 'lab-exp', running: true, vms: [] },
        headers: {},
      },
      'get experiments/a%2Fb%20c': { data: { name: 'a/b c' }, headers: {} },
      'get experiments/gone': httpError(404),
    });
    const api = createBuilderApi(http);

    await expect(api.experimentState('lab-exp')).resolves.toEqual({
      running: true,
    });
    // A stopped experiment's answer may leave the field out.
    await expect(api.experimentState('a/b c')).resolves.toEqual({
      running: false,
    });
    expect(http.calls.map((call) => `${call.method} ${call.url}`)).toEqual([
      'get experiments/lab-exp',
      'get experiments/a%2Fb%20c',
    ]);
    expect(experimentPath('a/b c')).toBe('experiments/a%2Fb%20c');
    // One that is gone is a failure: nothing is opened.
    await expect(api.experimentState('gone')).rejects.toThrow('status 404');
  });

  test('says why it could not be opened', () => {
    expect(experimentError('lab-exp', httpError(404))).toBe(
      'Could not open experiment lab-exp. It may have been deleted or renamed.',
    );
    expect(experimentError('lab-exp', httpError(403))).toBe(
      'Could not open experiment lab-exp. Your role does not allow it.',
    );
    expect(experimentError('lab-exp', httpError(401))).toBe(
      'Could not open experiment lab-exp. Your session has ended. Sign in again to continue.',
    );
    expect(experimentError('lab-exp', new Error('Network Error'))).toBe(
      'Could not open experiment lab-exp. The server could not be reached. Check the connection and try again.',
    );
    // Whatever else the server answers, its own words are not repeated.
    for (const status of [400, 409, 500]) {
      expect(experimentError('lab-exp', httpError(status))).toBe(
        'Could not open experiment lab-exp. It may have been deleted or renamed.',
      );
    }
  });
});
