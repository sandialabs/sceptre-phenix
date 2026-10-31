import { describe, expect, test } from 'vitest';

import {
  count,
  createAnnouncer,
  describeCombined,
  describeImport,
  describeNames,
  describeRemoval,
  listOf,
  unsaidPart,
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

  test('up to three things are named, and of more the first two and how many others', () => {
    expect(describeNames([])).toBe('');
    expect(describeNames(undefined)).toBe('');
    expect(describeNames(['Lab'])).toBe('Lab');
    expect(describeNames(['Lab', 'Core'])).toBe('Lab and Core');
    expect(describeNames(['Lab', 'Core', 'Edge'])).toBe('Lab, Core and Edge');
    expect(describeNames(['Lab', 'Core', 'Edge', 'Site'])).toBe(
      'Lab, Core and 2 others',
    );
    expect(describeNames(['a', 'b', 'c', 'd', 'e', 'f', 'g'])).toBe(
      'a, b and 5 others',
    );
    // What has no name is left out.
    expect(describeNames(['Lab', '', undefined, 'Core'])).toBe('Lab and Core');
  });

  test('an import says how many warnings it gave', () => {
    expect(describeImport()).toBe('Imported diagram.');
    expect(describeImport([])).toBe('Imported diagram.');
    expect(describeImport(['a'])).toBe('Imported the diagram, with 1 warning.');
    expect(describeImport(['a', 'b'])).toBe(
      'Imported the diagram, with 2 warnings.',
    );
    expect(describeImport(['a'], {})).toBe(
      'Imported the diagram, with 1 warning.',
    );
    expect(describeImport([], { legacy: false })).toBe('Imported diagram.');
  });

  test('the conversion of a legacy diagram says so, with its warnings', () => {
    expect(describeImport([], { legacy: true })).toBe(
      'Converted the legacy diagram.',
    );
    expect(describeImport(undefined, { legacy: true })).toBe(
      'Converted the legacy diagram.',
    );
    expect(describeImport(['a'], { legacy: true })).toBe(
      'Converted the legacy diagram, with 1 warning.',
    );
    expect(describeImport(['a', 'b', 'c'], { legacy: true })).toBe(
      'Converted the legacy diagram, with 3 warnings.',
    );
  });

  test('a copy and a combined import name the topology read and the new one', () => {
    const names = { source: 'site', name: 'site-copy' };

    expect(describeImport([], { mode: 'copy', ...names })).toBe(
      'Imported a copy of topology site as site-copy.',
    );
    expect(describeImport(['a', 'b'], { mode: 'copy', ...names })).toBe(
      'Imported a copy of topology site as site-copy, with 2 warnings.',
    );
    expect(
      describeImport([], {
        mode: 'combine',
        source: 'site',
        name: 'site-combined',
      }),
    ).toBe(
      'Combined topology site and its included topologies as site-combined.',
    );
    expect(
      describeImport(['a'], {
        mode: 'combine',
        source: 'site',
        name: 'site-combined',
      }),
    ).toBe(
      'Combined topology site and its included topologies as site-combined, with 1 warning.',
    );
    // A legacy topology imported as a copy is announced as the copy it is.
    expect(describeImport([], { mode: 'copy', legacy: true, ...names })).toBe(
      'Imported a copy of topology site as site-copy.',
    );
    // A plain import says nothing of names, whatever it is given.
    expect(describeImport([], { mode: 'import', ...names })).toBe(
      'Imported diagram.',
    );
    expect(describeImport([], { mode: 'import', legacy: true })).toBe(
      'Converted the legacy diagram.',
    );
  });

  test('combining in an open draft names both drafts and what is still included', () => {
    expect(describeCombined(3, 'water-combined', 'water')).toBe(
      'Combined 3 included nodes into new draft water-combined. Draft water is unchanged.',
    );
    expect(describeCombined(1, 'water-combined', 'water', [])).toBe(
      'Combined 1 included node into new draft water-combined. Draft water is unchanged.',
    );
    expect(describeCombined(2, 'w-combined', 'w', ['site-b'])).toBe(
      'Combined 2 included nodes into new draft w-combined. Draft w is unchanged. ' +
        'It still includes site-b, whose nodes are not in the diagram.',
    );
    expect(describeCombined(2, 'w-combined', 'w', ['a', 'b', 'c'])).toMatch(
      / It still includes a, b and c, whose nodes are not in the diagram\.$/,
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
    // A device end names its interface.
    web.device.interfaces = [{ id: 'h1', name: 'eth1' }];
    edge.sourceHandleId = 'h1';
    expect(describeRemoval(before, { nodes: [web, db], edges: [] })).toBe(
      'Deleted the connection between web-01 (eth1) and db-01',
    );
    expect(describeRemoval(before, { nodes: [], edges: [] })).toBe(
      'Deleted web-01, db-01 and 1 connection',
    );

    // A network that went with its last switch is named too.
    const sw = { id: 'n3', kind: 'switch', label: 'EXP' };
    const withNetwork = {
      nodes: [web, sw],
      edges: [{ id: 'e2', sourceNodeId: 'n1', targetNodeId: 'n3' }],
      networks: [{ id: 'x', name: 'EXP' }],
    };

    expect(
      describeRemoval(withNetwork, { nodes: [web], edges: [], networks: [] }),
    ).toBe('Deleted EXP, 1 connection and network EXP');

    // More than 3 networks are counted, as nodes are.
    const names = ['A', 'B', 'C', 'D'];
    const many = {
      nodes: names.map((name) => ({ id: name, kind: 'switch', label: name })),
      edges: [],
      networks: names.map((name) => ({ id: name, name })),
    };

    expect(describeRemoval(many, { nodes: [], edges: [], networks: [] })).toBe(
      'Deleted 4 nodes and 4 networks',
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

    announcer.push('Imported the diagram, with 2 warnings.');
    announcer.push('Draft created.');
    expect(shown).toEqual(['Imported the diagram, with 2 warnings.']);

    timers.tick();
    expect(shown).toEqual([
      'Imported the diagram, with 2 warnings.',
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

  test('a stale message gives its place in a burst to one that holds', () => {
    const shown = [];
    const timers = manualTimers();
    const heard = new Set();
    let modal = true;
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      blocked: () => modal,
      ...timers,
    });
    const push = (text) =>
      announcer.push(text, { stale: () => heard.has(text) });

    // While a dialog is open, it says a, b and c itself, but not kept.
    push('kept');
    for (const text of ['a', 'b', 'c']) {
      push(text);
      heard.add(text);
    }
    modal = false;
    while (timers.pending.length) {
      timers.tick();
    }

    expect(shown).toEqual(['kept']);
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

describe('messages a dialog said', () => {
  test('nothing is left of an announcement the dialog said at or after it', () => {
    const message = { text: 'Renamed alpha to alpha-2', seq: 4 };

    expect(unsaidPart(message, [])).toBe('Renamed alpha to alpha-2');
    // The dialog's sentence ends in a period. The announcement's need not.
    expect(
      unsaidPart(message, [{ text: 'Renamed alpha to alpha-2.', seq: 4 }]),
    ).toBe('');
    expect(
      unsaidPart(message, [
        { text: 'Moved alpha.', seq: 6 },
        { text: ' Renamed alpha to alpha-2 ', seq: 5 },
      ]),
    ).toBe('');
  });

  test('nothing is left when the dialog said more after the same sentence', () => {
    const message = {
      text: 'Saved the layers to layer set Plant, version 3',
      seq: 4,
    };

    expect(
      unsaidPart(message, [
        {
          text: 'Saved the layers to layer set Plant, version 3. Icon ops was not added.',
          seq: 4,
        },
      ]),
    ).toBe('');
    // A longer name is another sentence.
    expect(
      unsaidPart(message, [
        { text: 'Saved the layers to layer set Plant, version 30.', seq: 4 },
      ]),
    ).toBe(message.text);
  });

  test('only the remainder is left when the dialog said the first sentence', () => {
    const message = {
      text: 'Renamed alpha to alpha-2. Not saved: this draft changed on the server.',
      seq: 4,
    };

    expect(
      unsaidPart(message, [{ text: 'Renamed alpha to alpha-2.', seq: 4 }]),
    ).toBe('Not saved: this draft changed on the server.');
    expect(
      unsaidPart(message, [
        { text: 'Renamed alpha.', seq: 4 },
        { text: 'Renamed alpha to alpha-2.', seq: 5 },
      ]),
    ).toBe('Not saved: this draft changed on the server.');
  });

  test('all is left of another message, or of the same one said before it', () => {
    const message = { text: 'Renamed alpha to alpha-2', seq: 4 };

    expect(
      unsaidPart(message, [{ text: 'Renamed alpha to alpha-3.', seq: 4 }]),
    ).toBe(message.text);
    // An earlier fix said the same, but this announcement came after it.
    expect(
      unsaidPart(message, [{ text: 'Renamed alpha to alpha-2.', seq: 3 }]),
    ).toBe(message.text);
    // A sentence that only starts the same is not a first sentence.
    expect(
      unsaidPart(message, [{ text: 'Renamed alpha to alpha', seq: 4 }]),
    ).toBe(message.text);
  });
});

describe('the rest of a message', () => {
  test('is what shows, and a message with no rest is dropped', () => {
    const shown = [];
    const timers = manualTimers();
    let blocked = true;
    const said = { first: false, second: false };
    const announcer = createAnnouncer({
      show: (text) => shown.push(text),
      blocked: () => blocked,
      ...timers,
    });

    announcer.push('Renamed alpha. Not saved.', {
      rest: () => (said.first ? 'Not saved.' : 'Renamed alpha. Not saved.'),
    });
    announcer.push('Moved alpha', {
      rest: () => (said.second ? '' : 'Moved alpha'),
    });
    said.first = true;
    said.second = true;
    blocked = false;
    timers.tick();

    expect(shown).toEqual(['Not saved.']);
  });
});
