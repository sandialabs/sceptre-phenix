// escapeHTML guards user, file and server text in the HTML Buefy renders; the
// Files tab's delete confirmation and errorNotif.test.js show it at work.
import fc from 'fast-check';
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

// HTML-ish text: markup, entities and quotes mixed with any characters but
// NUL and CR, which a browser's HTML parser drops or turns into LF
const text = fc
  .array(
    fc.oneof(
      fc.constantFrom('<', '>', '&', '"', "'", '&amp;', '&lt;', '<b>', '</p>'),
      fc.string({ unit: 'binary' }),
    ),
  )
  .map((parts) => parts.join('').replace(/[\0\r]/g, ''));

it('reads back as the same text, in an element or a quoted attribute', () => {
  const box = document.createElement('div');
  fc.assert(
    fc.property(text, (s) => {
      box.innerHTML = `<p title="${escapeHTML(s)}">${escapeHTML(s)}</p>`;
      expect(box.children).toHaveLength(1);
      expect(box.firstChild.textContent).toBe(s);
      expect(box.firstChild.getAttribute('title')).toBe(s);
    }),
  );
});
