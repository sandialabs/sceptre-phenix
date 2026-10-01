import { describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import {
  comboboxKey,
  findUser,
  userLabel,
  userOptions,
} from '@/builder/combobox.js';
import {
  accessLabel,
  addedMessage,
  changeCount,
  conflictMessage,
  deleteMessage,
  describeShares,
  listedAccess,
  matchesMessage,
  mergeShares,
  newestFirst,
  reasonMessage,
  rowChange,
  rowNote,
  rowsOf,
  savedMessage,
  shareLink,
  sharesOf,
  validateAdd,
  validUsername,
} from '@/builder/share.js';

const base = [
  { user: 'bob', access: 'edit' },
  { user: 'carol', access: 'view' },
  { user: 'dave', access: 'view' },
];

describe('share rows', () => {
  test('a stale share starts marked removed, and rows save without it', () => {
    const rows = rowsOf([
      ...base,
      { user: 'erin', access: 'edit', stale: true },
    ]);

    expect(rows.at(-1)).toEqual({
      user: 'erin',
      access: 'edit',
      stale: true,
      removed: true,
    });
    expect(sharesOf(rows)).toEqual(base);
    // Saving removes the stale share: that is a change.
    expect(changeCount([...base, { user: 'erin', access: 'edit' }], rows)).toBe(
      1,
    );
  });

  test('changes count people added, removed and given other access', () => {
    const rows = rowsOf(base);

    expect(changeCount(base, rows)).toBe(0);

    rows[0].access = 'view';
    rows[1].removed = true;
    rows.push({ user: 'frank', access: 'edit', stale: false, removed: false });

    expect(changeCount(base, rows)).toBe(3);
    expect(rows.map((row) => rowChange(base, row))).toEqual([
      'changed',
      'removed',
      '',
      'added',
    ]);

    // Changed back: no longer a change.
    rows[0].access = 'edit';
    expect(changeCount(base, rows)).toBe(2);
  });
});

describe('merging after someone else changed the list', () => {
  test('adds, removals and access changes go onto the latest list', () => {
    const rows = rowsOf(base);

    rows[0].access = 'view'; // bob: edit -> view
    rows[1].removed = true; // carol removed
    rows.push({ user: 'frank', access: 'edit', stale: false, removed: false });

    // Meanwhile, another tab added gina and made dave an editor.
    const server = [
      { user: 'bob', access: 'edit' },
      { user: 'carol', access: 'view' },
      { user: 'dave', access: 'edit' },
      { user: 'gina', access: 'view' },
    ];

    expect(sharesOf(mergeShares(server, base, rows))).toEqual([
      { user: 'bob', access: 'view' },
      { user: 'dave', access: 'edit' },
      { user: 'gina', access: 'view' },
      { user: 'frank', access: 'edit' },
    ]);
  });

  test('both sides adding the same person keeps the access chosen here', () => {
    const rows = [
      ...rowsOf(base),
      { user: 'frank', access: 'edit', stale: false, removed: false },
    ];
    const server = [...base, { user: 'frank', access: 'view' }];
    const merged = mergeShares(server, base, rows);

    expect(merged.filter((row) => row.user === 'frank')).toEqual([
      { user: 'frank', access: 'edit', stale: false, removed: false },
    ]);
  });

  test('someone the other change removed is not added back by an access change', () => {
    const rows = rowsOf(base);

    rows[1].access = 'edit'; // carol: view -> edit here
    const server = [base[0], base[2]]; // carol removed there

    const merged = mergeShares(server, base, rows);

    expect(merged.map((row) => row.user)).toEqual(['bob', 'dave']);
    expect(changeCount(server, merged)).toBe(0);
  });

  test('a removed row stays on screen, marked, so nothing typed is lost', () => {
    const rows = rowsOf(base);

    rows[2].removed = true;
    const merged = mergeShares(base, base, rows);

    expect(merged[2]).toMatchObject({ user: 'dave', removed: true });
    expect(changeCount(base, merged)).toBe(1);
  });
});

describe('adding a person', () => {
  const rows = rowsOf([...base, { user: 'erin', access: 'view', stale: true }]);

  test('names what is wrong, in words', () => {
    expect(validateAdd('  ', rows, 'alice', null).error).toBe(
      'Enter a username.',
    );
    expect(validateAdd('alice', rows, 'alice', null).error).toBe(
      'You own this draft.',
    );
    expect(validateAdd(' bob ', rows, 'alice', null)).toMatchObject({
      user: 'bob',
      error: 'bob already has access.',
      row: { user: 'bob' },
    });
    expect(validateAdd('erin', rows, 'alice', null).error).toMatch(
      /^erin's account was removed\./,
    );
    expect(validateAdd('a/b', rows, 'alice', null).error).toBe(
      'a/b is not a valid username.',
    );
    expect(validateAdd('bobb', rows, 'alice', ['bob', 'bobby']).error).toBe(
      'No user named bobb.',
    );
    // Unknown users are left to the server when the role cannot list them.
    expect(validateAdd('bobb', rows, 'alice', null)).toEqual({
      user: 'bobb',
      error: '',
      row: undefined,
    });
  });

  test('a person marked removed can be added back', () => {
    const marked = rowsOf(base);

    marked[0].removed = true;

    expect(validateAdd('bob', marked, 'alice', null)).toMatchObject({
      error: '',
      row: { user: 'bob' },
    });
  });

  test('a full list takes no one else', () => {
    const full = rowsOf(
      Array.from({ length: 25 }, (_, index) => ({
        user: `u${index}`,
        access: 'view',
      })),
    );

    expect(validateAdd('zed', full, 'alice', null).error).toBe(
      'A draft can be shared with at most 25 people.',
    );
    expect(validateAdd('zed', full, 'alice', null, 30).error).toBe('');
  });

  test('usernames the server refuses are refused here', () => {
    expect(validUsername('bob')).toBe(true);
    expect(validUsername('')).toBe(false);
    expect(validUsername('a\u0007b')).toBe(false);
    expect(validUsername('c1\u009bESC')).toBe(false);
    expect(validUsername('bob\u202e')).toBe(false);
    expect(validUsername('a/b')).toBe(false);
    expect(validUsername('ana.pérez@example.com')).toBe(true);
    expect(validUsername('x'.repeat(257))).toBe(false);
    expect(validUsername('é'.repeat(129))).toBe(false);
  });
});

describe('words', () => {
  test('who a draft is shared with', () => {
    expect(deleteMessage([])).toBe(
      'The draft and its whole history are removed from the server. This cannot be undone.',
    );
    expect(deleteMessage([{ user: 'bob' }, { user: 'carol' }])).toBe(
      'The draft and its whole history are removed from the server. bob and carol will lose access too. This cannot be undone.',
    );
    expect(describeShares([])).toBe('');
    expect(describeShares([{ user: 'bob' }])).toBe('bob');
    expect(describeShares(['bob', 'carol'])).toBe('bob and carol');
    expect(describeShares(['bob', 'carol', 'dave'])).toBe(
      'bob, carol and dave',
    );
    expect(describeShares(['bob', 'carol', 'dave', 'erin', 'frank'])).toBe(
      'bob, carol and 3 others',
    );
    expect(describeShares(['bob', 'carol', 'dave', 'erin'])).toBe(
      'bob, carol and 2 others',
    );
  });

  test("what another user's listed draft lets the user do", () => {
    expect(listedAccess({ access: 'edit' })).toBe('Can edit');
    expect(listedAccess({ access: 'view', readOnly: true })).toBe('Can view');
    expect(listedAccess({ access: 'edit', readOnly: true })).toBe(
      'Can edit (your role allows viewing only)',
    );
  });

  test('who saved the newer version a conflict is with', () => {
    expect(conflictMessage('bob', 'alice')).toBe(
      'bob saved a newer version of this draft, so your changes cannot be written over it.',
    );
    expect(conflictMessage('alice', 'alice')).toBe(
      'You saved a newer version of this draft in another tab or window, so the changes here cannot be written over it.',
    );
    expect(conflictMessage('', 'alice')).toBe(
      'Someone else saved a newer version of this draft, so your changes cannot be written over it.',
    );
  });

  test('drafts are ordered by when they last changed, the latest first', () => {
    const drafts = [
      { id: 'a', updated: '2026-09-27T10:00:43Z' },
      { id: 'b', updated: '2026-09-27T10:00:43.1Z' },
      { id: 'c' },
      { id: 'd', updated: '2026-09-27T11:00:00Z' },
    ];

    expect(newestFirst(drafts).map((draft) => draft.id)).toEqual([
      'd',
      'b',
      'a',
      'c',
    ]);
    // The list it was given is left as it was.
    expect(drafts[0].id).toBe('a');
  });

  test('what a save says', () => {
    expect(savedMessage('Network lab', [])).toBe(
      'Only you can open Network lab now.',
    );
    expect(savedMessage('Network lab', base.slice(0, 2))).toBe(
      'Network lab is now shared with bob (can edit) and carol (can view).',
    );
    expect(
      savedMessage('Lab', [...base, { user: 'erin', access: 'view' }]),
    ).toBe('Lab is now shared with 4 people.');
    expect(accessLabel('edit')).toBe('Can edit');
    expect(accessLabel('nonsense')).toBe('Can view');
  });

  test('why the server refused a person', () => {
    expect(reasonMessage('owner', 'alice')).toBe('You own this draft.');
    expect(reasonMessage('duplicate', 'bob')).toBe('bob is listed twice.');
    expect(reasonMessage('invalid-access', 'bob')).toBe(
      'Choose Can view or Can edit.',
    );
    expect(reasonMessage('invalid-user', 'a/b')).toBe(
      'a/b is not a valid username.',
    );
    expect(reasonMessage('too-many', '')).toBe(
      'A draft can be shared with at most 25 people.',
    );
    expect(reasonMessage('unknown-user', 'bobb')).toBe(
      'No user named bobb. People must have signed in to phēnix at least once.',
    );
    expect(reasonMessage('account-removed', 'erin')).toBe(
      "erin's account was removed. Remove them and save, then add them again if they have a new account.",
    );
    expect(reasonMessage('something-new', 'bob')).toBe(
      'bob could not be added.',
    );
  });

  test('what the dialog says as it is edited', () => {
    expect(addedMessage('bob', 'edit')).toBe(
      'bob added, can edit. Save to apply.',
    );
    expect(matchesMessage(0)).toBe('No users match.');
    expect(matchesMessage(1)).toBe('1 user matches.');
    expect(matchesMessage(12)).toBe('12 users match.');

    const rows = rowsOf([
      ...base,
      { user: 'erin', access: 'view', stale: true },
    ]);

    rows[0].access = 'view';
    rows[1].removed = true;
    rows.push({ user: 'frank', access: 'view', stale: false, removed: false });
    rows.push({ user: 'gina', access: 'edit', stale: false, removed: true });

    expect(rows.map((row) => rowNote(base, row))).toEqual([
      'Changed',
      'Removed when you save',
      '',
      'Account removed',
      'New',
      'Not added',
    ]);
  });

  test('the link opens the draft in Builder v2', () => {
    const router = {
      resolve: vi.fn(({ query }) => ({
        href: `/builder-v2?draft=${encodeURIComponent(query.draft)}`,
      })),
    };

    expect(shareLink(router, 'alice', 'd1', 'https://phenix.example')).toBe(
      'https://phenix.example/builder-v2?draft=alice%2Fd1',
    );
    expect(router.resolve).toHaveBeenCalledWith({
      name: 'builder-v2',
      query: { draft: 'alice/d1' },
    });
  });
});

describe('the user combobox', () => {
  const users = [
    { username: 'alice', name: 'Alice Owner' },
    { username: 'bob', name: 'Robert Lee' },
    { username: 'bobby', name: '' },
    { username: 'carol', name: 'Carol Bobbin' },
    ...Array.from({ length: 10 }, (_, index) => ({
      username: `user${index}`,
      name: '',
    })),
  ];

  test('lists matches by username, then name, leaving out the listed', () => {
    const names = (query, options) =>
      userOptions(users, query, options).map((user) => user.username);

    expect(names('bob', { exclude: ['alice'] })).toEqual([
      'bob',
      'bobby',
      'carol',
    ]);
    expect(names('lee')).toEqual(['bob']);
    expect(names('bob', { exclude: ['bob'] })).not.toContain('bob');
    // With nothing typed, everyone, by username.
    expect(names('')).toHaveLength(users.length);
    expect(names('', { exclude: ['alice'] }).slice(0, 3)).toEqual([
      'bob',
      'bobby',
      'carol',
    ]);
    expect(names('user', { limit: 3 })).toEqual(['user0', 'user1', 'user2']);
  });

  test('names each user "Name (username)", and finds them by either', () => {
    expect(userLabel(users[1])).toBe('Robert Lee (bob)');
    expect(userLabel(users[2])).toBe('bobby');
    expect(findUser(users, 'Robert Lee (bob)')).toEqual(users[1]);
    expect(findUser(users, ' bob ')).toEqual(users[1]);
    expect(findUser(users, 'bobby')).toEqual(users[2]);
    expect(findUser(users, 'Robert Lee')).toBeNull();
    expect(findUser(null, 'bob')).toBeNull();
  });

  test('keys open, move through, take and close the list', () => {
    const key = (name, state, extra = {}) =>
      comboboxKey({ key: name, ...extra }, state);
    const open = { expanded: true, active: 1, count: 3 };
    const closed = { expanded: false, active: -1, count: 3 };

    expect(key('ArrowDown', closed)).toEqual({
      action: 'move',
      index: 0,
      prevent: true,
    });
    expect(key('ArrowDown', open).index).toBe(2);
    expect(key('ArrowDown', { ...open, active: 2 }).index).toBe(0);
    expect(key('ArrowUp', closed).index).toBe(2);
    expect(key('ArrowUp', { ...open, active: 0 }).index).toBe(2);
    expect(key('ArrowDown', closed, { altKey: true }).action).toBe('open');
    expect(key('Enter', open)).toEqual({
      action: 'choose',
      index: 1,
      prevent: true,
    });
    expect(key('Enter', closed).action).toBe('add');
    expect(key('Escape', open).action).toBe('close');
    // A closed list leaves Escape to the dialog.
    expect(key('Escape', closed)).toBeNull();
    expect(key('Tab', open)).toEqual({ action: 'close', prevent: false });
    expect(key('ArrowDown', { ...closed, count: 0 })).toBeNull();
    expect(key('ArrowDown', open, { ctrlKey: true })).toBeNull();
    expect(key('a', open)).toBeNull();
  });
});
