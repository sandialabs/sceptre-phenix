// @vitest-environment node
// A default button keeps white text when focused or pressed: Bulma's #363636
// is 2.2:1 on its gray, as when a closed dialog returns focus to its button.
import { expect, it } from 'vitest';

import { declarations } from './helpers/css.js';

it.each(['.button:focus', '.button:active'])(
  'gives %s white text',
  (selector) => {
    expect(declarations(selector)).toMatchObject({ color: 'white' });
  },
);
