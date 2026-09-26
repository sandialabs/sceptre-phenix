import { describe, expect, test } from 'vitest';

import {
  count,
  createAnnouncer,
  describeImport,
  describeRemoval,
  listOf,
} from '@/builder/announce.js';

// A timer queue driven by the test.
function manualTimers() {
  const pending = [];

  return {
    pending,
    setTimer: (fn) => {
      pending.push(fn);

      return pending.length;
    },
    clearTimer: () => {},
    tick() {
      pending.shift()?.();
    },
  };
}

describe('wording', () => {
  test('counts and lists read naturally', () => {
    expect(count(1, 'node')).toBe('1 node');
    expect(count(2, 'node')).toBe('2 nodes');
    expect(count(0, 'connection')).toBe('0 connections');
    expect(listOf(['a'])).toBe('a');
    expect(listOf(['a', 'b'])).toBe('a and b');
    expect(listOf(['a', 'b', 'c'])).toBe('a, b and c');
  });

  test('an import says how many warnings it gave', () => {
    expect(describeImport()).toBe('Imported diagram.');
    expect(describeImport([])).toBe('Imported diagram.');
    expect(describeImport(['a'])).toBe(
      'The diagram was imported with 1 warning.',
    );
    expect(describeImport(['a', 'b'])).toBe(
      'The diagram was imported with 2 warnings.',
    );
  });

  test('a removal names what went', () => {
    const web = { id: 'n1', kind: 'device', device: { hostname: 'web-01' } };
    const db = { id: 'n2', kind: 'device', device: { hostname: 'db-01' } };
    const edge = { id: 'e1', sourceNodeId: 'n1', targetNodeId: 'n2' };
    const before = { nodes: [web, db], edges: [edge] };

    expect(describeRemoval(before, { nodes: [db], edges: [] })).toBe(
      'Deleted web-01 and 1 connection',
    );
    expect(describeRemoval(before, { nodes: [web, db], edges: [] })).toBe(
      'Deleted the connection between web-01 and db-01',
    );
    expect(describeRemoval(before, { nodes: [], edges: [] })).toBe(
      'Deleted web-01, db-01 and 1 connection',
    );
  });
});

describe('live region pacing', () => {
  test('back-to-back messages are each shown, in order', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('The diagram was imported with 2 warnings.');
    announcer.push('Draft created.');
    expect(shown).toEqual(['The diagram was imported with 2 warnings.']);

    timers.tick();
    expect(shown).toEqual([
      'The diagram was imported with 2 warnings.',
      'Draft created.',
    ]);

    // The region is free again once the last hold ends.
    timers.tick();
    announcer.push('Added device');
    expect(shown.at(-1)).toBe('Added device');
  });

  test('a repeat is announced again, but queued only once', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('Nothing to undo.');
    announcer.push('Nothing to undo.');
    announcer.push('Nothing to undo.');
    timers.tick();
    timers.tick();

    expect(shown).toEqual(['Nothing to undo.', 'Nothing to undo.']);
  });

  test('messages that waited are shown together, as sentences', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('Added switch');
    announcer.push('Connected nodes');
    announcer.push('Nothing to undo.');
    timers.tick();

    // None is spoken more than one hold late.
    expect(shown).toEqual([
      'Added switch',
      'Connected nodes. Nothing to undo.',
    ]);
  });

  test('a burst keeps only the latest few messages waiting', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    ['a', 'b', 'c', 'd', 'e'].forEach((message) => announcer.push(message));
    while (timers.pending.length) {
      timers.tick();
    }

    expect(shown).toEqual(['a', 'c. d. e.']);
  });

  test('a newer message in the same slot replaces one still waiting', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('Added device');
    announcer.push('Offline: 1 change kept on this device.', { slot: 'save' });
    announcer.push('Added switch');
    announcer.push('All changes saved.', { slot: 'save' });
    timers.tick();

    expect(shown).toEqual(['Added device', 'Added switch. All changes saved.']);
  });

  test('a burst drops older edits before a save-state message', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('a');
    announcer.push('Offline.', { slot: 'save' });
    ['b', 'c', 'd'].forEach((message) => announcer.push(message));
    timers.tick();

    expect(shown).toEqual(['a', 'Offline. c. d.']);
  });

  test('a message that no longer holds is dropped, not spoken late', () => {
    const shown = [];
    const timers = manualTimers();
    let status = 'offline';
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('Added device');
    announcer.push('Offline: 1 change kept on this device.', {
      slot: 'save',
      stale: () => status !== 'offline',
    });
    status = 'conflict';
    timers.tick();
    timers.tick();

    expect(shown).toEqual(['Added device']);

    // The region is free again for the next message.
    announcer.push('Undid Added device.');
    expect(shown.at(-1)).toBe('Undid Added device.');
  });

  test('messages wait while the region cannot be read', () => {
    const shown = [];
    const timers = manualTimers();
    let modal = true;
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      blocked: () => modal,
      ...timers,
    });

    announcer.push('Snapshot restored');
    timers.tick();
    expect(shown).toEqual([]);

    modal = false;
    timers.tick();
    expect(shown).toEqual(['Snapshot restored']);
  });

  test('dispose drops waiting messages', () => {
    const shown = [];
    const timers = manualTimers();
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      ...timers,
    });

    announcer.push('one');
    announcer.push('two');
    announcer.dispose();
    timers.tick();

    expect(shown).toEqual(['one']);
  });
});
