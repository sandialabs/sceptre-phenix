import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import { iconKeyForSpec } from '@/builder/catalog.js';

// The server derives a generated device's icon from the same table
// (TestIconKeyForSpecMatchesFrontEnd in types/builder), so a node gets the
// same icon whether it was made here or generated there.
describe('icon keys shared with the server', () => {
  const table = JSON.parse(
    readFileSync(
      new URL(
        '../../../go/types/builder/testdata/icon-keys.json',
        import.meta.url,
      ),
    ),
  );

  test.each(table.cases.map((entry) => [JSON.stringify(entry.spec), entry]))(
    '%s',
    (_, entry) => {
      expect(iconKeyForSpec(entry.spec)).toBe(entry.iconKey);
    },
  );
});
