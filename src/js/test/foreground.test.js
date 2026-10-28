// Whether background refreshes may ask the server: while the page's browser
// tab is showing, focused or not.
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { inForeground } from '@/utils/foreground.js';

afterEach(() => vi.restoreAllMocks());

describe('inForeground', () => {
  it.each([
    ['allows a shown, focused tab', false, true, true],
    ['allows a shown tab whose window lacks focus', false, false, true],
    ['holds back a hidden tab', true, false, false],
  ])('%s', (_, hidden, focused, want) => {
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
    vi.spyOn(document, 'hasFocus').mockReturnValue(focused);
    expect(inForeground()).toBe(want);
  });

  it('follows whether the tab is hidden alone', () => {
    fc.assert(
      fc.property(fc.boolean(), fc.boolean(), (hidden, focused) => {
        vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
        vi.spyOn(document, 'hasFocus').mockReturnValue(focused);
        expect(inForeground()).toBe(!hidden);
      }),
    );
  });
});
