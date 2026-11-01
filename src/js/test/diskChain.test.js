// @vitest-environment node
// How pages name and offer disk-list images: the VM details' backing chain,
// the disk and CD-ROM pickers' options, and the warnings beside a VM's disk.
import { readFileSync } from 'node:fs';

import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import {
  backingChain,
  baseName,
  chainProblem,
  diskDetailsRoute,
  diskLabel,
  diskList,
  diskOptions,
  diskReasons,
  diskWarnings,
  filesDir,
  findDisk,
  isoDisks,
  minimegaSafe,
} from '@/utils/diskChain.js';
import { RESOURCES } from '@/utils/freshness.js';
import {
  cachePage,
  cachedPage,
  clearPageCache,
  fetchIntoCache,
} from '@/utils/pageCache.js';
import { pageFetchers } from '@/utils/pageData.js';
import { diskListArb, pathSegment } from './helpers/arbitraries.js';
import { disk } from './helpers/fixtures.js';

// web_snap.qc2 is backed by the jammy folder's hardened image, backed by one
// outside the files directory; another jammy.qc2 is at the directory's top.
const disks = [
  disk('/phenix/images/web_snap.qc2', 'web_snap.qc2', {
    backingImages: [
      '/phenix/images/jammy/jammy_hardened.qc2',
      '/data/vms/jammy.qc2',
    ],
  }),
  disk('/phenix/images/jammy/jammy_hardened.qc2', 'jammy/jammy_hardened.qc2', {
    backingImages: ['/data/vms/jammy.qc2'],
  }),
  disk('/data/vms/jammy.qc2'),
  disk('/phenix/images/jammy.qc2', 'jammy.qc2'),
  disk('/phenix/images/virtio.iso', 'virtio.iso', { kind: 'ISO' }),
  disk('/phenix/images/isos/tools.iso', 'isos/tools.iso', { kind: 'ISO' }),
  disk('/data/isos/drivers.iso', '', { kind: 'ISO' }),
];

beforeEach(() => resetMocks({ store: {} }));

afterEach(() => {
  clearPageCache();
  vi.useRealTimers();
});

describe('baseName', () => {
  const noSlash = fc.string({ unit: 'binary' }).filter((s) => !s.includes('/'));

  it('names a disk image by its file name', () => {
    expect(baseName('/phenix/images/win10.qc2')).toBe('win10.qc2');
    // any folders, absolute or relative, even empty ones
    fc.assert(
      fc.property(fc.array(noSlash), noSlash, (folders, name) => {
        expect(baseName([...folders, name].join('/'))).toBe(name);
      }),
    );
  });
});

describe('diskLabel', () => {
  it('names a disk by its path within the files directory, else its full path', () => {
    expect(diskLabel(disks[1])).toBe('jammy/jammy_hardened.qc2');
    expect(diskLabel(disks[2])).toBe('/data/vms/jammy.qc2');
    expect(diskLabel(null)).toBe('');
  });
});

describe('diskDetailsRoute', () => {
  it('names the Disks page with the card of an image open', () => {
    expect(diskDetailsRoute('/phenix/images/kali.qc2')).toEqual({
      name: 'disks',
      query: { disk: '/phenix/images/kali.qc2' },
    });
  });
});

describe('backingChain', () => {
  it("lists a disk's backing images by label, nearest first", () => {
    expect(backingChain(disks, '/phenix/images/web_snap.qc2')).toEqual([
      {
        label: 'jammy/jammy_hardened.qc2',
        fullPath: '/phenix/images/jammy/jammy_hardened.qc2',
      },
      { label: '/data/vms/jammy.qc2', fullPath: '/data/vms/jammy.qc2' },
    ]);
  });

  it('names a backing image the list does not hold by its full path', () => {
    const chained = [
      disk('/phenix/images/a.qc2', 'a.qc2', {
        backingImages: ['/phenix/images/gone.qc2'],
      }),
    ];
    expect(backingChain(chained, '/phenix/images/a.qc2')).toEqual([
      { label: '/phenix/images/gone.qc2', fullPath: '' },
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

  it("names each backing image by a tail of its path, whatever the list's order", () => {
    const lists = diskListArb().chain((list) =>
      fc.tuple(
        fc.constant(list),
        fc.shuffledSubarray(list, { minLength: list.length }),
      ),
    );
    fc.assert(
      fc.property(lists, ([list, shuffled]) => {
        for (const { fullPath, backingImages } of list) {
          const chain = backingChain(list, fullPath);
          expect(chain).toHaveLength(backingImages.length);
          chain.forEach(({ label, fullPath }, i) => {
            const image = backingImages[i];
            expect(label === image || image.endsWith(`/${label}`)).toBe(true);
            expect(fullPath).toBe(findDisk(list, image) ? image : '');
          });
          expect(backingChain(shuffled, fullPath)).toEqual(chain);
        }
      }),
    );
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
      disk('/phenix/images/win/base.qc2', 'win/base.qc2'),
      disk('/phenix/images/base.qc2', 'base.qc2'),
      disk('/data/base.qc2'),
    ]);
    expect(inside.map(({ value, label }) => [label, value])).toEqual([
      ['base.qc2', '/phenix/images/base.qc2'],
      ['win/base.qc2', '/phenix/images/win/base.qc2'],
    ]);
    expect(outside.map((option) => option.value)).toEqual(['/data/base.qc2']);
  });

  it('offers a disk minimega cannot use disabled, saying why', () => {
    const { inside } = diskOptions([
      disk('/phenix/images/my disk.qc2', 'my disk.qc2'),
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

  it('offers every disk once, in its group, sorted, disabled when unsafe', () => {
    fc.assert(
      fc.property(diskListArb(), (list) => {
        const { inside, outside } = diskOptions(list);
        const values = [...inside, ...outside].map((option) => option.value);
        expect(values.toSorted()).toEqual(
          list.map((d) => d.fullPath).toSorted(),
        );
        for (const group of [inside, outside]) {
          const labels = group.map((o) => diskLabel(findDisk(list, o.value)));
          labels.slice(1).forEach((label, i) => {
            expect(labels[i].localeCompare(label)).toBeLessThanOrEqual(0);
          });
          for (const option of group) {
            const listed = findDisk(list, option.value);
            expect(option.outside).toBe(listed.outsideFilesDir);
            expect(option.disabled).toBe(!minimegaSafe(option.value));
            expect(option.label.startsWith(diskLabel(listed))).toBe(true);
          }
        }
      }),
    );
  });
});

describe('minimegaSafe', () => {
  // The cases the server's rule is tested against too (see names_test.go):
  // every code point either rule refuses, and example paths.
  const cases = JSON.parse(
    readFileSync(
      new URL(
        '../../go/api/disk/testdata/minimega_names.json',
        import.meta.url,
      ),
      'utf8',
    ),
  );
  const codePoint = (name) => parseInt(name.replace(/^U\+/, ''), 16);
  const refusedCodes = new Set(
    cases.refused.flatMap(({ from, to = from }) => {
      const first = codePoint(from);
      return Array.from(
        { length: codePoint(to) - first + 1 },
        (_, i) => first + i,
      );
    }),
  );
  const refused = (char) => refusedCodes.has(char.codePointAt(0));

  it('refuses the code points the shared cases refuse, and only those', () => {
    expect(refusedCodes).toContain('#'.codePointAt(0));
    const wrong = [];
    for (let code = 0; code <= 0x10ffff && wrong.length < 20; code++) {
      const path = `/phenix/images/a${String.fromCodePoint(code)}b.qc2`;
      if (minimegaSafe(path) === refusedCodes.has(code)) {
        wrong.push(`U+${code.toString(16).toUpperCase().padStart(4, '0')}`);
      }
    }
    expect(wrong).toEqual([]);
  });

  it.each(cases.paths.accepted.map(({ path }) => [path]))(
    'takes %j, which minimega reads literally',
    (path) => {
      expect(minimegaSafe(path)).toBe(true);
    },
  );

  it.each(cases.paths.refused.map(({ path }) => [path]))(
    'refuses %j',
    (path) => {
      expect(minimegaSafe(path)).toBe(false);
    },
  );

  it('refuses a whole path of any code points when one is refused', () => {
    // astral code points and lone surrogates included
    fc.assert(
      fc.property(fc.string({ unit: 'binary' }), (path) => {
        expect(minimegaSafe(path)).toBe(![...path].some(refused));
      }),
    );
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

  it('is the directory of any list holding a disk inside it', () => {
    const dirs = fc
      .array(pathSegment, { minLength: 1, maxLength: 3 })
      .map((names) => `/${names.join('/')}`);
    const lists = dirs.chain((dir) =>
      fc.tuple(fc.constant(dir), diskListArb(dir)),
    );
    fc.assert(
      fc.property(lists, ([dir, list]) => {
        const inside = list.some((d) => !d.outsideFilesDir);
        expect(filesDir(list)).toBe(inside ? dir : '');
      }),
    );
  });
});

describe('chainProblem', () => {
  it('names what breaks a broken chain', () => {
    const broken = disk('/phenix/images/a.qc2', 'a.qc2', {
      brokenChain: true,
      chainProblem: 'backing file /phenix/images/base.qc2 is missing',
    });
    expect(chainProblem(broken)).toBe(
      'backing file /phenix/images/base.qc2 is missing',
    );
  });

  it('gives a general sentence when the server gives none', () => {
    const broken = disk('/phenix/images/a.qc2', 'a.qc2', { brokenChain: true });
    expect(chainProblem(broken)).toBe('a file in its backing chain is missing');
  });

  it('is empty for a whole chain or no disk', () => {
    const whole = disk('/phenix/images/a.qc2', 'a.qc2', {
      chainProblem: 'left over',
    });
    expect(chainProblem(whole)).toBe('');
    expect(chainProblem(null)).toBe('');
  });
});

describe('diskWarnings', () => {
  it('warns of a disk outside the files directory', () => {
    expect(diskWarnings(disks, '/data/vms/jammy.qc2')).toEqual(['outside']);
    expect(diskWarnings(disks, '/phenix/images/jammy.qc2')).toEqual([]);
  });

  it('warns of a disk a known list does not hold', () => {
    expect(diskWarnings(disks, '/phenix/images/gone.qc2')).toEqual([
      'unlisted',
    ]);
    expect(diskWarnings([], '/phenix/images/gone.qc2')).toEqual(['unlisted']);
  });

  it('gives a broken chain first, with the other reasons', () => {
    const list = [
      disk('/data/vms/over.qc2', '', { brokenChain: true }),
      disk('/phenix/images/over.qc2', 'over.qc2', { brokenChain: true }),
    ];
    expect(diskWarnings(list, '/data/vms/over.qc2')).toEqual([
      'broken',
      'outside',
    ]);
    expect(diskWarnings(list, '/phenix/images/over.qc2')).toEqual(['broken']);
  });

  it('says nothing while the list is unknown, or of no disk', () => {
    expect(diskWarnings(null, '/phenix/images/gone.qc2')).toEqual([]);
    expect(diskWarnings(disks, '')).toEqual([]);
  });

  it('gives distinct known reasons, broken exactly for a broken listed disk', () => {
    const lists = diskListArb().chain((list) =>
      fc.tuple(
        fc.constant(list),
        fc.array(fc.boolean(), {
          minLength: list.length,
          maxLength: list.length,
        }),
        fc.oneof(
          fc.constant('/phenix/images/gone.qc2'),
          ...(list.length
            ? [fc.constantFrom(...list.map((d) => d.fullPath))]
            : []),
        ),
      ),
    );
    fc.assert(
      fc.property(lists, ([list, broken, path]) => {
        const marked = list.map((d, i) => ({ ...d, brokenChain: broken[i] }));
        const reasons = diskWarnings(marked, path);
        const found = findDisk(marked, path);

        expect(new Set(reasons).size).toBe(reasons.length);
        for (const reason of reasons) {
          expect(['unlisted', 'broken', 'outside']).toContain(reason);
        }
        expect(reasons.includes('broken')).toBe(!!found?.brokenChain);
        expect(reasons.includes('unlisted')).toBe(!found);
        if (found) expect(reasons).toEqual(diskReasons(found));
      }),
    );
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
        vi.advanceTimersByTime(RESOURCES.disks.reuseMs + 1);
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
