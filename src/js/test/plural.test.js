// The wording helpers for messages about one or several things.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  listText,
  namedList,
  plural,
  pluralWord,
  upperFirst,
} from '@/utils/plural.js';

describe('plural', () => {
  it('uses the singular for exactly one', () => {
    expect(plural(1, 'VM')).toBe('1 VM');
    expect(plural('1', 'CPU')).toBe('1 CPU');
  });

  it('uses the plural for zero, several, and unknown counts', () => {
    expect(plural(0, 'VM')).toBe('0 VMs');
    expect(plural(3, 'file')).toBe('3 files');
    expect(pluralWord(undefined, 'vCPU')).toBe('vCPUs');
  });

  it('takes an irregular plural', () => {
    expect(pluralWord(1, 'it', 'them')).toBe('it');
    expect(pluralWord(4, 'it', 'them')).toBe('them');
  });
});

// prose items without a comma or a word "and", which readList would split on
const item = fc
  .string({ minLength: 1 })
  .filter((s) => !s.includes(',') && !/(^| )and( |$)/.test(s));
const readList = (text) => (text ? text.split(/, and |, | and /) : []);

describe('listText', () => {
  it('joins lists as prose', () => {
    expect(listText([])).toBe('');
    expect(listText(['a'])).toBe('a');
    expect(listText(['a', 'b'])).toBe('a and b');
    expect(listText(['a', 'b', 'c'])).toBe('a, b, and c');
    expect(listText([0, 2])).toBe('0 and 2');
  });

  it('keeps every item, in order', () => {
    fc.assert(
      fc.property(fc.array(item), (items) => {
        expect(readList(listText(items))).toEqual(items);
      }),
    );
  });
});

describe('namedList', () => {
  it('names one VM as "the <name> VM", never "the VMs <name>"', () => {
    expect(namedList(['web'], 'VM')).toBe('the web VM');
    expect(namedList('web', 'VM')).toBe('the web VM');
  });

  it('names several with the plural first', () => {
    expect(namedList(['web', 'db'], 'VM')).toBe('the VMs web and db');
    expect(namedList(['a', 'b', 'c'], 'VM')).toBe('the VMs a, b, and c');
    fc.assert(
      fc.property(fc.array(item, { minLength: 2 }), (names) => {
        const text = namedList(names, 'VM');
        expect(text.startsWith('the VMs ')).toBe(true);
        expect(readList(text.slice('the VMs '.length))).toEqual(names);
      }),
    );
  });

  it('starts a sentence with a capital', () => {
    expect(upperFirst(namedList(['web'], 'VM'))).toBe('The web VM');
    expect(upperFirst('')).toBe('');
  });
});
