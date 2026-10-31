// Runs before every test file; files that opt into node get no DOM.
import { afterEach, beforeEach } from 'vitest';

import { memoryStorage } from './helpers/storage.js';

if (typeof document !== 'undefined') {
  const { enableAutoUnmount } = await import('@vue/test-utils');

  // Node's own localStorage shadows happy-dom's, so each test gets an empty one
  beforeEach(() => {
    // happy-dom 21 defines it with a getter only, so redefine rather than assign
    Object.defineProperty(globalThis, 'localStorage', {
      value: memoryStorage(),
      configurable: true,
      writable: true,
    });
  });
  // registered first so it runs last: the Buefy dialogs and toasts on the body
  afterEach(() => {
    document.body.innerHTML = '';
  });
  enableAutoUnmount(afterEach);
}
