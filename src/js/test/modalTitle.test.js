// @vitest-environment node
// A dialog's title wraps inside its head's padding; Bulma's own rule stops it
// shrinking, so a long user or VM name would reach the dialog's edge.
import { expect, it } from 'vitest';

import { declarations } from './helpers/css.js';

it('lets a long dialog title shrink and wrap', () => {
  expect(declarations('.modal-card-title')).toMatchObject({
    'flex-shrink': '1',
    'min-width': '0',
    'overflow-wrap': 'anywhere',
  });
});
