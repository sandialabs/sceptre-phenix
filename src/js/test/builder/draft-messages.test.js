// What the drafts page and the editor say about a draft (draftMessages.js).

import { describe, expect, test } from 'vitest';

import {
  conflictMessage,
  deleteMessage,
  newestFirst,
} from '@/builder/draftMessages.js';

describe('draft messages', () => {
  test('what deleting a draft does', () => {
    expect(deleteMessage()).toBe(
      'The Builder removes the draft and its whole history from the server. You cannot undo this.',
    );
  });

  test('who saved the newer version a conflict is with', () => {
    expect(conflictMessage('bob', 'alice')).toBe(
      'bob saved a newer version of this draft. Your changes cannot replace it.',
    );
    expect(conflictMessage('alice', 'alice')).toBe(
      'You saved a newer version of this draft in another tab or window. The changes here cannot replace it.',
    );
    expect(conflictMessage('', 'alice')).toBe(
      'Someone else saved a newer version of this draft. Your changes cannot replace it.',
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
    expect(newestFirst(null)).toEqual([]);
  });
});
