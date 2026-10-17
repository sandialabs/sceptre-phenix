// Rules of key specs that hold for every spec and key press. Each call names
// its platform, so nothing reads or sets the page's platform.

import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  eventToKey,
  matchesKey,
  normalizeKey,
  parseKey,
} from '@/builder/keymap.js';

import { keyPress } from './arbitraries.js';

const modifier = fc.oneof(
  fc.constantFrom(
    'Mod',
    'ctrl',
    'Control',
    'cmd',
    'Command',
    'meta',
    'alt',
    'Option',
    'shift',
  ),
  fc.string({ maxLength: 3 }),
);
const keyName = fc.oneof(
  fc.constantFrom(
    'a',
    'Z',
    '5',
    '+',
    '-',
    '?',
    'Space',
    'Esc',
    'Return',
    'pgdn',
    'F12',
    'Up',
  ),
  fc.string({ maxLength: 3 }),
);
const spec = fc
  .tuple(fc.array(modifier, { maxLength: 4 }), keyName)
  .map(([modifiers, key]) => [...modifiers, key].join('+'));

describe('key specs', () => {
  test('normalizing a spec twice changes nothing', () => {
    fc.assert(
      fc.property(spec, (text) => {
        const once = normalizeKey(text);

        expect(normalizeKey(once)).toBe(once);
      }),
      { numRuns: 500 },
    );
  });

  test('a canonical spec parses back to itself', () => {
    fc.assert(
      fc.property(spec, (text) => {
        const parts = parseKey(text);

        if (parts) {
          expect(parseKey(parts.spec)?.spec).toBe(parts.spec);
        }
      }),
      { numRuns: 500 },
    );
  });
});

describe('recording a key press', () => {
  test('Shift with a numpad digit records nothing, as no spec matches it', () => {
    const press = { code: 'Numpad5', key: '5', shiftKey: true };

    expect(eventToKey(press, 'other')).toBe('');
    expect(eventToKey({ ...press, shiftKey: false }, 'other')).toBe('5');
  });

  test('a recorded key press matches the spec recorded for it', () => {
    fc.assert(
      fc.property(keyPress, ({ event, platform }) => {
        const recorded = eventToKey(event, platform);

        if (recorded) {
          expect(matchesKey(event, recorded, platform)).toBe(true);
        }
      }),
      { numRuns: 500 },
    );
  });
});
