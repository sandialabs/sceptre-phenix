// WebShark's helpers: which files it opens, its URLs, the Wireshark command,
// and who may use it. The pages that open captures in it have their own
// tests.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const rbac = vi.hoisted(() => ({ allowed: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => rbac.allowed(...args),
}));

const axios = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));

import {
  canUseWebShark,
  captureStreamURL,
  isCaptureFile,
  openFailure,
  openInWebShark,
  queryTarget,
  captureStreamCLI,
  webSharkInstalled,
  webSharkURL,
  wiresharkCommand,
} from '@/utils/webshark.js';

beforeEach(() => {
  rbac.allowed = () => true;
  axios.post.mockReset();
});

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
    expect(openFailure(status(404))).toMatch(/no longer being captured/);
    expect(openFailure(status(501))).toMatch(/not installed/);
    expect(openFailure(status(403))).toMatch(/permission/);
    expect(openFailure(new Error('network'))).toMatch(/could not open/);
  });
});

describe('the Wireshark command', () => {
  it("streams the capture from the UI's API", () => {
    const url = captureStreamURL('exp 1', 'web', 1, 'https://phenix.example');
    expect(url).toBe(
      'https://phenix.example/api/v1/experiments/exp%201/vms/web/captures/1/stream',
    );
    expect(wiresharkCommand(url, 'jwt.token')).toBe(
      "curl -fsSN --max-redirs 0 -L -H 'X-Phenix-Auth-Token: Bearer jwt.token' " +
        "'https://phenix.example/api/v1/experiments/exp%201/vms/web/captures/1/stream'" +
        ' | wireshark -k -i -',
    );
  });

  it('quotes what the shell would otherwise read', () => {
    const url = captureStreamURL("it's", "vm'1", 0, 'http://h');
    expect(wiresharkCommand(url, null)).toBe(
      "curl -fsSN --max-redirs 0 -L 'http://h/api/v1/experiments/it'\\''s/vms/vm'\\''1/captures/0/stream'" +
        ' | wireshark -k -i -',
    );
  });

  it('names the same capture for the CLI', () => {
    expect(captureStreamCLI("it's", 'web', 2)).toBe(
      "phenix vm capture stream 'it'\\''s' 'web' 2 | wireshark -k -i -",
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
    const only = (resource, verb) => (r, v) => r === resource && v === verb;
    rbac.allowed = only('experiments/files', 'get');
    expect(canUseWebShark()).toBe(true);
    rbac.allowed = only('vms/captures', 'list');
    expect(canUseWebShark()).toBe(true);
    rbac.allowed = only('experiments/files', 'list');
    expect(canUseWebShark()).toBe(false);
  });
});
