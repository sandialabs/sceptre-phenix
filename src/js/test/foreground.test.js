// Whether background refreshes may ask the server: while the page's browser
// tab is showing, focused or not.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { inForeground } from '@/utils/foreground.js';

afterEach(() => vi.restoreAllMocks());

describe('inForeground', () => {
  it.each([
    ['allows a shown, focused tab', false, true, true],
    ['allows a shown tab whose window lacks focus', false, false, true],
    ['holds back a hidden tab', true, false, false],
    ['holds back a hidden tab even if its window has focus', true, true, false],
  ])('%s', (_, hidden, focused, want) => {
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
    vi.spyOn(document, 'hasFocus').mockReturnValue(focused);
    expect(inForeground()).toBe(want);
  });
});
