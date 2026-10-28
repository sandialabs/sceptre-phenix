// The Node Editor's field search (fieldSearch.js): which parts of the form a
// query shows. The dialog is left to builder-inspector.spec.js.

import { describe, expect, test } from 'vitest';

import {
  partMatches,
  searchFields,
  searchPath,
  searchResultText,
  searchWords,
} from '@/builder/fieldSearch.js';

describe('the words of a query', () => {
  test('are in lowercase, with paths in the dotted form', () => {
    expect(searchWords('  Hardware  OS ')).toEqual(['hardware', 'os']);
    expect(searchWords('network.interfaces[0].VLAN')).toEqual([
      'network.interfaces.0.vlan',
    ]);
    expect(searchWords('spec.general.hostname')).toEqual(['general.hostname']);
    expect(searchWords('.hardware.')).toEqual(['hardware']);
    expect(searchWords('   ')).toEqual([]);
    expect(searchWords(undefined)).toEqual([]);
  });
});

describe('the path a part is found by', () => {
  test('is its path in the node spec, or the data path of a field of the Builder', () => {
    expect(searchPath('spec.network.interfaces.0.vlan')).toBe(
      'network.interfaces.0.vlan',
    );
    expect(searchPath('iconKey')).toBe('iconKey');
    expect(searchPath('#checks')).toBe('');
    expect(searchPath('spec#more-settings')).toBe('');
    expect(searchPath(undefined)).toBe('');
  });
});

describe('a part matches', () => {
  const vlan = { label: 'VLAN', path: 'network.interfaces.0.vlan' };
  const memory = { label: 'Memory', path: 'hardware.memory' };

  test('by its label or its path, ignoring case', () => {
    expect(partMatches(['vla'], vlan)).toBe(true);
    expect(partMatches(['mem'], memory)).toBe(true);
    expect(partMatches(['hardware'], memory)).toBe(true);
    expect(partMatches(['hardware.memory'], memory)).toBe(true);
    expect(partMatches(['hardware.mem'], memory)).toBe(true);
    expect(partMatches(['network.interfaces.0.vlan'], vlan)).toBe(true);
    expect(partMatches(['interfaces.1.vlan'], vlan)).toBe(false);
    expect(partMatches(['disk'], memory)).toBe(false);
  });

  test('by the parts of a path in order', () => {
    expect(partMatches(['interfaces.vlan'], vlan)).toBe(true);
    expect(partMatches(['network.vlan'], vlan)).toBe(true);
    expect(partMatches(['vlan.interfaces'], vlan)).toBe(false);
    expect(partMatches(['interfaces.0.vlan'], vlan)).toBe(true);
    expect(
      partMatches(['interfaces.1.vlan'], {
        label: 'VLAN',
        path: 'network.interfaces.10.vlan',
      }),
    ).toBe(false);
  });

  test('by a number in a path as a whole list index', () => {
    const tenth = { label: 'VLAN', path: 'network.interfaces.10.vlan' };

    expect(partMatches(['interfaces.1'], tenth)).toBe(false);
    expect(partMatches(['1'], tenth)).toBe(false);
    expect(partMatches(['1.vlan'], tenth)).toBe(false);
    expect(partMatches(['interfaces.10'], tenth)).toBe(true);
    expect(partMatches(['10'], tenth)).toBe(true);
    expect(partMatches(['faces.10.vl'], tenth)).toBe(true);
    expect(partMatches(searchWords('interfaces[1]'), tenth)).toBe(false);
    expect(partMatches(searchWords('interfaces[10]'), tenth)).toBe(true);
  });

  test('when each word matches', () => {
    expect(partMatches(['interface', 'vlan'], vlan)).toBe(true);
    expect(partMatches(['interface', 'memory'], vlan)).toBe(false);
    expect(partMatches([], vlan)).toBe(false);
  });

  test('without a path, by its label alone', () => {
    expect(partMatches(['connection'], { label: 'Connection points' })).toBe(
      true,
    );
    expect(partMatches(['checks'], { label: 'Checks', path: '' })).toBe(true);
  });
});

describe('what a search shows', () => {
  // A form in document order: the parent of each part is the index of the
  // part that holds it.
  const parts = [
    { label: 'Hostname', path: 'hostname', parent: -1, field: true },
    { label: 'General', path: 'general', parent: -1 },
    { label: 'Snapshot', path: 'general.snapshot', parent: 1, field: true },
    { label: 'Network', path: 'network', parent: -1 },
    { label: 'Interfaces', path: 'network.interfaces', parent: 3 },
    {
      label: 'Interface 1: eth0',
      path: 'network.interfaces.0',
      parent: 4,
    },
    {
      label: 'Address',
      path: 'network.interfaces.0.address',
      parent: 5,
      field: true,
    },
    {
      label: 'VLAN',
      path: 'network.interfaces.0.vlan',
      parent: 5,
      field: true,
    },
    {
      label: 'Interface 2: eth1',
      path: 'network.interfaces.1',
      parent: 4,
    },
    {
      label: 'VLAN',
      path: 'network.interfaces.1.vlan',
      parent: 8,
      field: true,
    },
    { label: 'Connection points', path: '', parent: -1 },
  ];

  test('a field, with the parts that hold it, and hides the rest', () => {
    const { states, count } = searchFields(parts, 'interfaces[1].vlan');

    expect(states).toEqual([
      'hidden',
      'hidden',
      'hidden',
      'holds',
      'holds',
      'hidden',
      'hidden',
      'hidden',
      'holds',
      'match',
      'hidden',
    ]);
    expect(count).toBe(1);
  });

  test('every field a partial path matches', () => {
    const { states, count } = searchFields(parts, 'interfaces.vlan');

    expect(states.filter((state) => state === 'match')).toHaveLength(2);
    expect(count).toBe(2);
  });

  test('all that a section or an entry holds, when its label matches', () => {
    const { states, count } = searchFields(parts, 'eth0');

    expect(states.slice(3, 8)).toEqual([
      'holds',
      'holds',
      'match',
      'inside',
      'inside',
    ]);
    expect(states[8]).toBe('hidden');
    expect(count).toBe(2);
  });

  test('a part with no field, which counts as one', () => {
    const { states, count } = searchFields(parts, 'connection');

    expect(states[10]).toBe('match');
    expect(count).toBe(1);
  });

  test('nothing, for a query that matches nothing or no query', () => {
    expect(searchFields(parts, 'zzz').count).toBe(0);
    expect(searchFields(parts, '').states.every((s) => s === 'hidden')).toBe(
      true,
    );
  });

  test('says how many fields match', () => {
    expect(searchResultText(0)).toBe('No fields match.');
    expect(searchResultText(1)).toBe('1 field matches.');
    expect(searchResultText(3)).toBe('3 fields match.');
  });
});
