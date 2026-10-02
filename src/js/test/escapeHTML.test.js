// escapeHTML serves every page that puts user, file or server text in the
// HTML Buefy renders; the Files tab's delete confirmation and error
// notifications (errorNotif.test.js) show it at work.
import { expect, it } from 'vitest';

import { escapeHTML } from '@/utils/escapeHTML.js';

it('escapes markup and attribute quotes', () => {
  expect(escapeHTML('<b a="1">&</b>')).toBe(
    '&lt;b a=&quot;1&quot;&gt;&amp;&lt;/b&gt;',
  );
  // text that already looks escaped is shown as typed
  expect(escapeHTML('&lt;')).toBe('&amp;lt;');
  expect(escapeHTML(42)).toBe('42');
});
