// Which disk actions a role may take on a disk in its current state, and why
// not, as the Disks page's details window and rows show them.
import fc from 'fast-check';
import { describe, expect, it, vi } from 'vitest';

const { modules } = await vi.hoisted(() => import('./helpers/mocks.js'));
// the disk list's loader reaches the store
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);

import {
  DISK_ACTION_RULES,
  diskActionState,
  diskDisabledReason,
  diskRunningText,
} from '@/utils/diskActions.js';
import { diskListArb } from './helpers/arbitraries.js';
import { disk } from './helpers/fixtures.js';

const base = disk('/phenix/images/base.qc2', 'base.qc2', { kind: 'VM' });
const snap = disk('/phenix/images/snap.qc2', 'snap.qc2', {
  kind: 'VM',
  backingImages: ['/phenix/images/base.qc2'],
});
const disks = [base, snap];
const all = () => true;
const reason = (action, d, can = all) =>
  diskDisabledReason(action, d, { disks, can });

describe('diskDisabledReason', () => {
  it('allows every action on an idle VM disk the role may act on', () => {
    for (const action of Object.keys(DISK_ACTION_RULES)) {
      expect(reason(action, snap)).toBe(null);
    }
  });

  it.each([
    [
      'a read-only disk outside the files directory',
      { ...snap, readOnly: true, outsideFilesDir: true },
      'delete',
      'the disk is outside the standard images directory',
    ],
    [
      'a read-only disk the list leaves out',
      { ...snap, readOnly: true },
      'rename',
      'the disk is at a path the disk list leaves out',
    ],
    [
      'a disk a running experiment uses',
      { ...snap, inUse: true, experiments: [{ name: 'a', running: true }] },
      'delete',
      'the disk is in use by a',
    ],
    [
      'a disk that is no VM disk',
      { ...snap, kind: 'Kernel' },
      'snapshot',
      'only VM disks can be snapshotted',
    ],
    [
      'a disk with no backing image',
      { ...snap, backingImages: [] },
      'commit',
      'the disk has no backing image to commit into',
    ],
    [
      'a backing image phēnix does not act on',
      { ...snap, backingImages: ['/elsewhere/base.qc2'] },
      'commit',
      null,
    ],
  ])('rules out an action on %s', (_, d, action, want) => {
    expect(reason(action, d)).toBe(want);
  });

  it('rules out the actions minimega runs on a path it cannot take', () => {
    const spaced = disk('/phenix/images/my disk.qc2', 'my disk.qc2', {
      backingImages: ['/phenix/images/base.qc2'],
    });
    const reasons = Object.fromEntries(
      Object.keys(DISK_ACTION_RULES).map((a) => [a, reason(a, spaced)]),
    );
    const unusable = "minimega cannot use the disk's path";
    expect(reasons).toEqual({
      snapshot: unusable,
      commit: unusable,
      rebase: unusable,
      clone: null,
      resize: unusable,
      download: null,
      rename: null,
      delete: null,
    });
    // in a folder's name too
    for (const rel of ['a$b.qc2', 'x,y/a.qc2']) {
      expect(reason('resize', disk(`/phenix/images/${rel}`, rel))).toBe(
        unusable,
      );
    }
  });

  it('refuses to commit into a read-only backing image, found by its full path', () => {
    const commit = (...list) =>
      diskDisabledReason('commit', snap, { disks: [...list, snap], can: all });

    expect(commit({ ...base, readOnly: true, outsideFilesDir: true })).toBe(
      'its backing image is outside the standard images directory',
    );
    expect(commit({ ...base, readOnly: true })).toBe(
      'its backing image is at a path the disk list leaves out',
    );
    // a read-only disk of the same name elsewhere is not the backing image
    expect(commit(disk('/data/vms/base.qc2'), base)).toBe(null);
  });

  it.each([
    [
      'snapshot',
      [
        ['create', undefined],
        ['get', 'snap.qc2'],
      ],
    ],
    [
      'commit',
      [
        ['update', 'snap.qc2'],
        ['update', 'base.qc2'],
      ],
    ],
    [
      'clone',
      [
        ['create', undefined],
        ['get', 'snap.qc2'],
      ],
    ],
    ['rebase', [['update', 'snap.qc2']]],
    ['resize', [['update', 'snap.qc2']]],
    ['download', [['get', 'snap.qc2']]],
    ['rename', [['update', 'snap.qc2']]],
    ['delete', [['delete', 'snap.qc2']]],
  ])('needs %s permissions %j', (action, needed) => {
    const key = (verb, name) => `${verb}/${name}`;
    const neededKeys = new Set(needed.map(([v, n]) => key(v, n)));

    expect(reason(action, snap, (v, n) => neededKeys.has(key(v, n)))).toBe(
      null,
    );
    for (const [v, n] of needed) {
      expect(reason(action, snap, (vv, nn) => key(vv, nn) !== key(v, n))).toBe(
        'you do not have permission',
      );
    }
  });

  // a disk list, one of its disks in any state, and a role
  const cases = diskListArb()
    .filter((list) => list.length > 0)
    .chain((list) =>
      fc.record({
        list: fc.constant(list),
        disk: fc.record({
          listed: fc.constantFrom(...list),
          readOnly: fc.boolean(),
          inUse: fc.boolean(),
          kind: fc.constantFrom('VM', 'ISO', 'Kernel'),
          experiments: fc.array(
            fc.record({ name: fc.string(), running: fc.boolean() }),
            { maxLength: 3 },
          ),
        }),
        action: fc.constantFrom(...Object.keys(DISK_ACTION_RULES)),
        can: fc.func(fc.boolean()),
      }),
    );

  it("refuses for want of permission only what the disk's state allows", () => {
    const NO = 'you do not have permission';
    fc.assert(
      fc.property(
        cases,
        ({ list, disk: { listed, ...state }, action, can }) => {
          const d = { ...listed, ...state };
          const always = diskDisabledReason(action, d, {
            disks: list,
            can: all,
          });
          // the answers the role gave to every permission asked of it
          const asked = [];
          const got = diskDisabledReason(action, d, {
            disks: list,
            can: (...args) => (asked.push(can(...args)), asked.at(-1)),
          });
          const none = () => false;

          if (d.readOnly) {
            expect(got).toMatch(/^the disk is (outside|at a path)/);
          }
          if (always === null) {
            expect(got).toBe(asked.every(Boolean) ? null : NO);
            expect(
              diskDisabledReason(action, d, { disks: list, can: none }),
            ).toBe(NO);
          } else {
            expect(got).toBe(always);
          }

          const label = action[0].toUpperCase() + action.slice(1);
          const offered = (role) =>
            diskActionState({ name: action, label }, d, {
              disks: list,
              can: role,
            });
          expect(offered(all)).toMatchObject({
            permitted: true,
            disabled: always !== null,
            reason: always && `Cannot ${action}: ${always}`,
          });
          expect(offered(none)).toMatchObject({
            permitted: always !== null,
            disabled: true,
            reason: `Cannot ${action}: ${always ?? NO}`,
          });
        },
      ),
    );
  });
});

describe('diskActionState', () => {
  const clone = { name: 'clone', label: 'Clone', icon: 'copy' };

  it('offers an action it may take', () => {
    expect(diskActionState(clone, snap, { disks, can: all })).toMatchObject({
      permitted: true,
      disabled: false,
      reason: null,
    });
  });

  it('marks an action the role may not take as not permitted', () => {
    expect(
      diskActionState(clone, snap, { disks, can: () => false }),
    ).toMatchObject({
      permitted: false,
      disabled: true,
      reason: 'Cannot clone: you do not have permission',
    });
  });

  it("disables an action the disk's state rules out", () => {
    const rename = { name: 'rename', label: 'Rename' };
    const used = { ...snap, inUse: true };
    expect(diskActionState(rename, used, { disks, can: all })).toMatchObject({
      permitted: true,
      disabled: true,
      reason: 'Cannot rename: the disk is in use by a running experiment',
    });
  });
});

describe('diskRunningText', () => {
  it('names the running experiments using a disk', () => {
    expect(
      diskRunningText({
        experiments: [
          { name: 'a', running: true },
          { name: 'b', running: false },
          { name: 'c', running: true },
        ],
      }),
    ).toBe('a, c');
    expect(diskRunningText({})).toBe('a running experiment');
  });
});
