// @vitest-environment node
// The header on a desktop-wide screen: with every page link and download it
// is wider than most screens, so it wraps onto a second row rather than push
// its right end (refresh status, jobs, Logout) off-screen.
import { expect, it } from 'vitest';

import { declarations } from './helpers/css.js';

// Bulma's navbar breakpoint, $desktop: where the burger menu gives way
const DESKTOP = 'min-width: 1024px';

it.each(['.navbar-menu', '.navbar-start', '.navbar-end'])(
  'lets %s wrap within the screen',
  (selector) => {
    expect(declarations(selector, DESKTOP)).toMatchObject({
      'flex-wrap': 'wrap',
      'flex-shrink': '1',
      'min-width': '0',
    });
  },
);

it('spaces the header links more tightly than Bulma', () => {
  for (const selector of [
    '.navbar-start > .navbar-item',
    '.navbar-end > .navbar-item',
  ]) {
    expect(declarations(selector, DESKTOP)).toMatchObject({
      'padding-inline': '0.4rem',
    });
  }
});
