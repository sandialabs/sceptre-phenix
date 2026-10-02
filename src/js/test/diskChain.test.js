// How pages name and offer the images in the disk list the Disks page shows:
// the VM details' backing image chain, the disk and CD-ROM pickers' options
// and the warnings beside a VM's disk.
import { readFileSync } from 'node:fs';

import { afterEach, describe, expect, it, vi } from 'vitest';

const axios = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => true }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));

import {
  backingChain,
  diskLabel,
  diskList,
  DISK_LIST_REUSE_MS,
  diskOptions,
  diskWarning,
  filesDir,
  isoDisks,
  minimegaSafe,
} from '@/utils/diskChain.js';
import {
  cachePage,
  cachedPage,
  clearPageCache,
  fetchIntoCache,
} from '@/utils/pageCache.js';
import { pageFetchers } from '@/utils/pageData.js';

// A disk as GET disks lists it: inside the files directory /phenix/images
// when given its path within it, else outside it.
const disk = (kind, fullPath, relativePath = '', backingImages = []) => ({
  kind,
  name: fullPath.replace(/^.*\//, ''),
  fullPath,
  relativePath,
  outsideFilesDir: !relativePath,
  readOnly: !relativePath,
  backingImages,
});

// web_snap.qc2 is backed by the hardened image in the jammy folder, which is
// backed by an image outside the files directory; another jammy.qc2 is at
// the top of the files directory
const disks = [
  disk('VM', '/phenix/images/web_snap.qc2', 'web_snap.qc2', [
    '/phenix/images/jammy/jammy_hardened.qc2',
    '/data/vms/jammy.qc2',
  ]),
  disk(
    'VM',
    '/phenix/images/jammy/jammy_hardened.qc2',
    'jammy/jammy_hardened.qc2',
    ['/data/vms/jammy.qc2'],
  ),
  disk('VM', '/data/vms/jammy.qc2'),
  disk('VM', '/phenix/images/jammy.qc2', 'jammy.qc2'),
  disk('ISO', '/phenix/images/virtio.iso', 'virtio.iso'),
  disk('ISO', '/phenix/images/isos/tools.iso', 'isos/tools.iso'),
  disk('ISO', '/data/isos/drivers.iso'),
];

afterEach(() => {
  clearPageCache();
  axios.get.mockReset();
  vi.useRealTimers();
});

describe('diskLabel', () => {
  it('names a disk by its path within the files directory, else its full path', () => {
    expect(diskLabel(disks[1])).toBe('jammy/jammy_hardened.qc2');
    expect(diskLabel(disks[2])).toBe('/data/vms/jammy.qc2');
    expect(diskLabel(null)).toBe('');
  });
});

describe('backingChain', () => {
  it("lists a disk's backing images by label, nearest first", () => {
    expect(backingChain(disks, '/phenix/images/web_snap.qc2')).toEqual([
      'jammy/jammy_hardened.qc2',
      '/data/vms/jammy.qc2',
    ]);
  });

  it('names a backing image the list does not hold by its full path', () => {
    const chained = [
      disk('VM', '/phenix/images/a.qc2', 'a.qc2', ['/phenix/images/gone.qc2']),
    ];
    expect(backingChain(chained, '/phenix/images/a.qc2')).toEqual([
      '/phenix/images/gone.qc2',
    ]);
  });

  it('finds a disk by its full path alone, never by a same-named file', () => {
    // /phenix/images/jammy.qc2 has no backing image; the other jammy.qc2 does
    // not stand in for it, nor does a file name
    expect(backingChain(disks, '/phenix/images/jammy.qc2')).toEqual([]);
    expect(backingChain(disks, 'web_snap.qc2')).toEqual([]);
  });

  it('is empty for a base image, an unlisted disk, or no disk', () => {
    expect(backingChain(disks, '/data/vms/jammy.qc2')).toEqual([]);
    expect(backingChain(disks, '/elsewhere/other.qc2')).toEqual([]);
    expect(backingChain(disks, '')).toEqual([]);
    expect(backingChain(null, '/phenix/images/web_snap.qc2')).toEqual([]);
  });
});

describe('diskOptions', () => {
  const labels = (options) => options.map((option) => option.label);

  it('offers disks by label, sorted, those outside the files directory apart', () => {
    const { inside, outside } = diskOptions(disks);
    expect(labels(inside)).toEqual([
      'isos/tools.iso',
      'jammy.qc2',
      'jammy/jammy_hardened.qc2',
      'virtio.iso',
      'web_snap.qc2',
    ]);
    expect(outside).toEqual([
      {
        value: '/data/isos/drivers.iso',
        label: '/data/isos/drivers.iso',
        disabled: false,
        outside: true,
      },
      {
        value: '/data/vms/jammy.qc2',
        label: '/data/vms/jammy.qc2',
        disabled: false,
        outside: true,
      },
    ]);
  });

  it('keeps same-named disks in different folders apart, valued by full path', () => {
    const { inside, outside } = diskOptions([
      disk('VM', '/phenix/images/win/base.qc2', 'win/base.qc2'),
      disk('VM', '/phenix/images/base.qc2', 'base.qc2'),
      disk('VM', '/data/base.qc2'),
    ]);
    expect(inside.map(({ value, label }) => [label, value])).toEqual([
      ['base.qc2', '/phenix/images/base.qc2'],
      ['win/base.qc2', '/phenix/images/win/base.qc2'],
    ]);
    expect(outside.map((option) => option.value)).toEqual(['/data/base.qc2']);
  });

  it('offers a disk minimega cannot use disabled, saying why', () => {
    const { inside } = diskOptions([
      disk('VM', '/phenix/images/my disk.qc2', 'my disk.qc2'),
    ]);
    expect(inside).toEqual([
      {
        value: '/phenix/images/my disk.qc2',
        label: 'my disk.qc2 (minimega cannot use this name)',
        disabled: true,
        outside: false,
      },
    ]);
  });

  it('is empty without disks', () => {
    expect(diskOptions(null)).toEqual({ inside: [], outside: [] });
  });
});

describe('minimegaSafe', () => {
  // The server's rule: minimegaSafe in src/go/api/disk/paths.go refuses "$"
  // and "," besides what file.CommandSafe in src/go/util/file/delete.go
  // refuses: the characters of its globMeta, control characters
  // (unicode.IsControl) and white space (unicode.IsSpace).
  const goSource = (path) =>
    readFileSync(new URL(`../../go/${path}`, import.meta.url), 'utf8');
  const fileSource = goSource('util/file/delete.go');
  const globMeta = JSON.parse(
    fileSource.match(/const globMeta = ("[^\n]*")/)[1],
  );
  const control = (code) => code < 0x20 || (code >= 0x7f && code <= 0x9f);
  // Go's unicode.IsSpace, from its documentation and White_Space table
  const space = (code) =>
    (code >= 0x09 && code <= 0x0d) ||
    [0x20, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000].includes(
      code,
    ) ||
    (code >= 0x2000 && code <= 0x200a);

  it('mirrors the rule the server applies', () => {
    const commandSafe = fileSource.match(
      /func CommandSafe\(p string\) bool \{([\s\S]*?)\n\}/,
    )[1];
    expect(commandSafe).toContain('strings.ContainsAny(p, globMeta)');
    expect(commandSafe).toContain('unicode.IsControl(r)');
    expect(commandSafe).toContain('unicode.IsSpace(r)');
    expect(goSource('api/disk/paths.go')).toContain(
      'file.CommandSafe(p) && !strings.ContainsAny(p, "$,")',
    );
  });

  it('refuses the paths the server refuses, and only those', () => {
    expect(globMeta).toContain('#');
    for (let code = 0; code <= 0x30ff; code++) {
      const char = String.fromCharCode(code);
      const refused =
        globMeta.includes(char) ||
        '$,'.includes(char) ||
        control(code) ||
        space(code);
      expect(
        minimegaSafe(`/phenix/images/a${char}b.qc2`),
        `U+${code.toString(16).padStart(4, '0')}`,
      ).toBe(!refused);
    }
    // not white space to Go, though JavaScript's \s matches it
    expect(minimegaSafe('/phenix/images/a\ufeffb.qc2')).toBe(true);
  });

  it('takes the paths minimega reads literally', () => {
    for (const path of [
      '/phenix/images/a+b/x_y.v2.qcow2',
      '/phenix/images/ü/x-1.qc2',
      '/data/vms/%20&=@!~.img',
    ]) {
      expect(minimegaSafe(path)).toBe(true);
    }
  });
});

describe('filesDir', () => {
  it('is the files directory a disk inside it shows', () => {
    expect(filesDir(disks)).toBe('/phenix/images');
  });

  it('is unknown from disks outside it alone, or none', () => {
    expect(filesDir([disks[2]])).toBe('');
    expect(filesDir(null)).toBe('');
  });
});

describe('diskWarning', () => {
  it('warns of a disk outside the files directory', () => {
    expect(diskWarning(disks, '/data/vms/jammy.qc2')).toBe('outside');
    expect(diskWarning(disks, '/phenix/images/jammy.qc2')).toBe(null);
  });

  it('warns of a disk a known list does not hold', () => {
    expect(diskWarning(disks, '/phenix/images/gone.qc2')).toBe('unlisted');
    expect(diskWarning([], '/phenix/images/gone.qc2')).toBe('unlisted');
  });

  it('says nothing while the list is unknown, or of no disk', () => {
    expect(diskWarning(null, '/phenix/images/gone.qc2')).toBe(null);
    expect(diskWarning(disks, '')).toBe(null);
  });
});

describe('isoDisks', () => {
  it('keeps the ISO images', () => {
    expect(isoDisks(disks).map((iso) => iso.fullPath)).toEqual([
      '/phenix/images/virtio.iso',
      '/phenix/images/isos/tools.iso',
      '/data/isos/drivers.iso',
    ]);
  });

  it('is empty when there are none', () => {
    expect(isoDisks([])).toEqual([]);
    expect(isoDisks(undefined)).toEqual([]);
  });
});

describe('diskList', () => {
  it("reuses the Disks page's recent list", async () => {
    cachePage('disks', disks);
    expect(await diskList()).toBe(disks);
    expect(axios.get).not.toHaveBeenCalled();
  });

  it.each([
    ['missing', () => {}],
    [
      'stale',
      () => {
        cachePage('disks', []);
        vi.advanceTimersByTime(DISK_LIST_REUSE_MS + 1);
      },
    ],
  ])('loads the list into the shared cache when it is %s', async (_, setup) => {
    vi.useFakeTimers();
    setup();
    axios.get.mockResolvedValue({ data: { disks } });

    expect(await diskList()).toEqual(disks);
    expect(axios.get).toHaveBeenCalledWith('disks', expect.anything());
    expect(cachedPage('disks').data).toEqual(disks);
  });

  it("waits on the Disks page's load rather than asking again", async () => {
    cachePage('disks', []);
    const answer = Promise.withResolvers();
    axios.get.mockReturnValue(answer.promise);

    const page = fetchIntoCache('disks', pageFetchers.disks);
    const joined = diskList();
    answer.resolve({ data: { disks } });
    expect(await joined).toEqual(disks);
    expect(axios.get).toHaveBeenCalledTimes(1);
    page.release();
  });
});
