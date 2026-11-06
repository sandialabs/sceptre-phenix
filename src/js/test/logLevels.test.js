// The Logs page's level badges: the short uppercase text and color of each
// level, and an unknown level shown by its own name.
import fc from 'fast-check';
import { expect, it } from 'vitest';

import { levelBadge } from '@/utils/logLevels.js';

it.each([
  ['DEBUG', 'DBG', 'level-debug'],
  ['INFO', 'INFO', 'level-info'],
  ['WARN', 'WRN', 'level-warn'],
  ['WARNING', 'WRN', 'level-warn'],
  ['ERROR', 'ERR', 'level-error'],
  ['error', 'ERR', 'level-error'],
  ['FATAL', 'FATAL', 'level-other'],
  ['trace', 'TRACE', 'level-other'],
  ['', 'UNKNOWN', 'level-other'],
  [undefined, 'UNKNOWN', 'level-other'],
  ['constructor', 'CONSTRUCTOR', 'level-other'],
])('shows %j as %s', (level, label, cls) => {
  expect(levelBadge(level)).toEqual({ label, cls });
});

const KNOWN = ['DEBUG', 'INFO', 'WARN', 'WARNING', 'ERROR'];

it('shows any level uppercased, a known one shortened whatever its case', () => {
  fc.assert(
    fc.property(fc.string(), (level) => {
      const { label, cls } = levelBadge(level);
      const name = level.trim().toUpperCase();
      expect(label).toBe(label.toUpperCase());
      if (KNOWN.includes(name)) {
        expect(cls).not.toBe('level-other');
        expect(label.length).toBeLessThanOrEqual(4);
      } else {
        expect({ label, cls }).toEqual({
          label: name || 'UNKNOWN',
          cls: 'level-other',
        });
      }
    }),
  );
});
