// Toast durations come from utils/toast.js, and errors are never toasts: they
// open as notifications that stay until dismissed (utils/errorNotif.js).
import { defineComponent, h } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';

import { TOAST_DURATION } from '@/utils/toast.js';
import { mountComponent, toasts } from './helpers/mount.js';

// the index of the ')' that closes a call whose arguments start at i
function closingParen(text, i) {
  const open = { '(': ')', '{': '}', '[': ']' };
  const closers = [')'];
  for (; i < text.length; i++) {
    const [c, top] = [text[i], closers.at(-1)];
    if (top === '`' || top === "'" || top === '"') {
      if (c === '\\') i++;
      else if (c === top) closers.pop();
      else if (top === '`' && c === '$' && text[i + 1] === '{') {
        closers.push('}');
        i++;
      }
    } else if (c === '/' && text[i + 1] === '/') {
      i = text.indexOf('\n', i);
      if (i < 0) break;
    } else if (c === '`' || c === "'" || c === '"') closers.push(c);
    else if (open[c]) closers.push(open[c]);
    else if (c === top) {
      closers.pop();
      if (!closers.length) return i;
    }
  }
  return text.length;
}

// the arguments of each toast.open( ... ) call, as written
const toastCalls = (text) =>
  [...text.matchAll(/toast\.open\(/g)].map((m) => {
    const start = m.index + m[0].length;
    return text.slice(start, closingParen(text, start));
  });

afterEach(() => {
  vi.useRealTimers();
});

it('shows a toast without a duration of its own for TOAST_DURATION', async () => {
  const Page = defineComponent({ render: () => h('div') });
  const wrapper = await mountComponent(Page);
  vi.useFakeTimers();

  wrapper.vm.$buefy.toast.open({ message: 'Saved', type: 'is-success' });
  vi.advanceTimersByTime(TOAST_DURATION - 1);
  expect(toasts()).toEqual([{ text: 'Saved', type: 'is-success' }]);
  // and the fade out after it
  vi.advanceTimersByTime(1 + 150);
  expect(toasts()).toEqual([]);
});

it.each([
  ["toast.open({ message: 'Saved' })", ["{ message: 'Saved' }"]],
  [
    "toast.open({ message: `Lost ${vm.name}`, type: 'is-danger' }); f()",
    ["{ message: `Lost ${vm.name}`, type: 'is-danger' }"],
  ],
  [
    "toast.open({ message: ')}', ...{ duration: 1 } })",
    ["{ message: ')}', ...{ duration: 1 } }"],
  ],
  [
    "toast.open({\n  // don't (\n  message: m(a),\n})",
    ["{\n  // don't (\n  message: m(a),\n}"],
  ],
])('reads the whole call in %j', (text, calls) => {
  expect(toastCalls(text)).toEqual(calls);
});
