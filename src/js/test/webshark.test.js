// WebShark's helpers: which files it opens, its URLs, the Wireshark command,
// and who may use it (the pages that open captures have their own tests).
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);

import {
  canUseWebShark,
  capabilityURL,
  isCaptureFile,
  mintCaptureStream,
  openFailure,
  openInWebShark,
  queryTarget,
  captureStreamCLI,
  webSharkInstalled,
  webSharkURL,
  wiresharkCommand,
} from '@/utils/webshark.js';
import { allowOnly } from './helpers/permissions.js';

beforeEach(() => resetMocks());

// The words a POSIX shell reads from a command line of single-quoted words,
// backslash escapes and plain words; throws on anything else unquoted.
function shellWords(line) {
  const words = [];
  let word = null;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (c === "'") {
      const end = line.indexOf("'", i + 1);
      if (end < 0) throw new Error(`unterminated quote in ${line}`);
      word = (word ?? '') + line.slice(i + 1, end);
      i = end;
    } else if (c === '\\') {
      word = (word ?? '') + line[++i];
    } else if (c === ' ') {
      if (word !== null) words.push(word);
      word = null;
    } else if (/[\w./:|-]/.test(c)) {
      word = (word ?? '') + c;
    } else {
      throw new Error(`unquoted ${JSON.stringify(c)} in ${line}`);
    }
  }
  return word === null ? words : [...words, word];
}

const anyText = fc.string({ unit: 'binary' });

describe('capture files', () => {
  it.each([
    'a.pcap',
    'dir/b.PCAPNG',
    'c.cap',
    'd.pcap.gz',
    'e.PcapNg.GZ',
    'f.cap.gz',
  ])('%s is one', (name) => {
    expect(isCaptureFile(name)).toBe(true);
  });

  it.each(['a.txt', 'pcap', 'a.pcap.bak', 'a.gz', 'a.pcapx', '', undefined])(
    '%s is not',
    (name) => {
      expect(isCaptureFile(name)).toBe(false);
    },
  );
});

describe('webSharkURL', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('is under the base path, with the capture encoded', () => {
    expect(webSharkURL('exp1/a b&c.pcap')).toBe(
      '/webshark/embed?capture=exp1%2Fa%20b%26c.pcap&embed=1',
    );
    expect(webSharkURL('x.pcap', { embed: false })).toBe(
      '/webshark/embed?capture=x.pcap',
    );
  });

  it('stays under the base path of a UI behind a proxy', () => {
    vi.stubEnv('BASE_URL', '/igor/range-1/phenix/');
    expect(webSharkURL('x.pcap')).toBe(
      '/igor/range-1/phenix/webshark/embed?capture=x.pcap&embed=1',
    );
    expect(webSharkURL('x.pcap', { embed: false })).toBe(
      '/igor/range-1/phenix/webshark/embed?capture=x.pcap',
    );
  });

  it('reads back as any capture name', () => {
    fc.assert(
      fc.property(fc.string({ unit: 'grapheme' }), fc.boolean(), (c, embed) => {
        const url = new URL(webSharkURL(c, { embed }), 'http://h');
        expect(url.pathname).toBe('/webshark/embed');
        expect([...url.searchParams]).toEqual([
          ['capture', c],
          ...(embed ? [['embed', '1']] : []),
        ]);
      }),
    );
  });
});

describe('queryTarget', () => {
  it('names a saved file', () => {
    const { exp, target } = queryTarget({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(exp).toBe('exp1');
    expect(target).toMatchObject({
      key: 'file:caps/a.pcap',
      live: false,
      body: { path: 'caps/a.pcap' },
      query: { file: 'caps/a.pcap' },
    });
  });

  it('names a running capture by VM and interface index', () => {
    const { target } = queryTarget({ exp: 'exp1', vm: 'web', iface: '2' });
    expect(target).toMatchObject({
      key: 'live:2:web',
      live: true,
      label: 'web · IF2',
      body: { vm: 'web', interface: 2 },
      query: { vm: 'web', iface: '2' },
    });
  });

  it('names nothing without an experiment or with a bad interface', () => {
    expect(queryTarget({ file: 'a.pcap' })).toEqual({
      exp: null,
      target: null,
    });
    expect(queryTarget({ exp: 'e', vm: 'web', iface: 'x' }).target).toBe(null);
    expect(queryTarget({ exp: 'e', vm: 'web' }).target).toBe(null);
  });

  it('takes the first of a repeated parameter', () => {
    const { exp, target } = queryTarget({
      exp: ['e1', 'e2'],
      file: ['a.pcap'],
    });
    expect(exp).toBe('e1');
    expect(target.path).toBe('a.pcap');
  });

  it('names the same target again from the query the target gives', () => {
    const name = fc.string({ minLength: 1 });
    // the router gives a repeated parameter as an array
    const param = (arb) =>
      fc.oneof(arb, fc.array(arb, { minLength: 1, maxLength: 2 }));
    const fileQuery = fc.record({ exp: param(name), file: param(name) });
    const liveQuery = fc.record({
      exp: param(name),
      vm: param(name),
      iface: param(fc.stringMatching(/^\d{1,6}$/)),
    });
    const value = fc.oneof(
      fc.constant(null),
      fc.string(),
      fc.nat().map(String),
      fc.array(fc.option(fc.string()), { maxLength: 2 }),
    );
    const anyQuery = fc.record(
      { exp: value, file: value, vm: value, iface: value },
      { requiredKeys: [] },
    );
    const roundTrips = (q) => {
      const { exp, target } = queryTarget(q);
      expect(queryTarget({ exp, ...target.query })).toEqual({ exp, target });
    };

    fc.assert(
      fc.property(fileQuery, (q) => {
        expect(queryTarget(q).target.live).toBe(false);
        roundTrips(q);
      }),
    );
    fc.assert(
      fc.property(liveQuery, (q) => {
        const { target } = queryTarget(q);
        expect(target.live).toBe(true);
        expect(target.query.iface).toBe(String(target.iface));
        roundTrips(q);
      }),
    );
    fc.assert(
      fc.property(anyQuery, (q) => {
        if (queryTarget(q).target) roundTrips(q);
      }),
    );
  });
});

describe('openInWebShark', () => {
  it('posts the target to the experiment', async () => {
    axios.post.mockResolvedValue({ data: { capture: 'c', live: false } });
    const { target } = queryTarget({ exp: 'a b', file: 'x.pcap' });
    expect(await openInWebShark('a b', target)).toEqual({
      capture: 'c',
      live: false,
    });
    expect(axios.post).toHaveBeenCalledWith('experiments/a%20b/webshark', {
      path: 'x.pcap',
    });
  });

  it('says why a capture could not be opened', () => {
    const status = (s) => ({ response: { status: s } });
    expect(openFailure(status(404))).toMatch(/its capture stopped/);
    expect(openFailure(status(501))).toMatch(/not installed/);
    expect(openFailure(status(403))).toMatch(/permission/);
    expect(openFailure(new Error('network'))).toMatch(/could not open/);
  });
});

describe('the Wireshark command', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('streams the capture with a stream token, under the base path', () => {
    vi.stubEnv('BASE_URL', '/phenix/');
    const url = capabilityURL(
      'webshark/streams/pcs_abc',
      'https://phenix.example',
    );
    expect(url).toBe('https://phenix.example/phenix/webshark/streams/pcs_abc');
    // the token is the only credential: no session token header
    expect(wiresharkCommand(url)).toBe(
      "curl -fsSN --max-redirs 0 -L 'https://phenix.example/phenix/webshark/streams/pcs_abc'" +
        ' | wireshark -k -i -',
    );
  });

  it('quotes what the shell would otherwise read', () => {
    expect(wiresharkCommand("http://h/it's")).toBe(
      "curl -fsSN --max-redirs 0 -L 'http://h/it'\\''s' | wireshark -k -i -",
    );
  });

  it('streams any URL as the one word curl is given', () => {
    fc.assert(
      fc.property(anyText, (url) => {
        expect(shellWords(wiresharkCommand(url))).toEqual([
          'curl',
          '-fsSN',
          '--max-redirs',
          '0',
          '-L',
          url,
          '|',
          'wireshark',
          '-k',
          '-i',
          '-',
        ]);
      }),
    );
  });

  it("asks the server for a stream token of the VM interface's capture", async () => {
    const token = {
      token: 'pcs_abc',
      urls: { stream: 'webshark/streams/pcs_abc' },
    };
    axios.post.mockResolvedValue({ data: token });
    await expect(mintCaptureStream('exp 1', 'web', 2)).resolves.toEqual(token);
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp%201/captureTokens',
      { vm: 'web', interface: 2, operations: ['stream'] },
    );
  });

  it('names the same capture for the CLI', () => {
    expect(captureStreamCLI("it's", 'web', 2)).toBe(
      "phenix vm capture stream 'it'\\''s' 'web' 2 | wireshark -k -i -",
    );
    fc.assert(
      fc.property(anyText, anyText, fc.nat(), (exp, vm, iface) => {
        expect(shellWords(captureStreamCLI(exp, vm, iface))).toEqual([
          'phenix',
          'vm',
          'capture',
          'stream',
          exp,
          vm,
          String(iface),
          '|',
          'wireshark',
          '-k',
          '-i',
          '-',
        ]);
      }),
    );
  });
});

describe('who may use WebShark', () => {
  it('needs the server to have it installed', () => {
    expect(webSharkInstalled(['tunneler-download', 'webshark'])).toBe(true);
    expect(webSharkInstalled(['tunneler-download'])).toBe(false);
    expect(webSharkInstalled(null)).toBe(false);
  });

  it('needs to get experiment files or list VM captures', () => {
    rbac.allowed = allowOnly(['experiments/files', 'get']);
    expect(canUseWebShark()).toBe(true);
    rbac.allowed = allowOnly(['vms/captures', 'list']);
    expect(canUseWebShark()).toBe(true);
    rbac.allowed = allowOnly(['experiments/files', 'list']);
    expect(canUseWebShark()).toBe(false);
  });
});
