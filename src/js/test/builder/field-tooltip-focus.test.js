// @vitest-environment happy-dom
// The field tooltip (fieldTooltip.js) and focus: a focus that focusQuietly
// gives, such as the Node Editor's first field as it opens, shows no
// tooltip. A later focus does.

import { afterEach, describe, expect, test } from 'vitest';
import { effectScope } from 'vue';

import {
  focusQuietly,
  useFieldTooltip,
} from '@/components/builder/inspector/fieldTooltip.js';

let scope;

afterEach(() => {
  scope?.stop();
  document.body.replaceChildren();
});

// A label and its text field. The field shows focus as the keyboard does.
function fieldWithTooltip() {
  scope = effectScope();

  const tooltip = scope.run(() =>
    useFieldTooltip(() => ({ path: 'general.hostname', text: 'The name.' })),
  );
  const label = document.createElement('label');
  const input = document.createElement('input');
  const other = document.createElement('button');
  const matches = input.matches.bind(input);

  input.matches = (selector) =>
    selector === ':focus-visible' ? true : matches(selector);
  input.addEventListener('focusin', (event) => tooltip.onFocusIn(event, label));
  document.body.append(label, input, other);

  return { tooltip, input, other };
}

describe('the field tooltip on focus', () => {
  test('a focus that focusQuietly gives shows no tooltip', () => {
    const { tooltip, input } = fieldWithTooltip();

    focusQuietly(input);

    expect(document.activeElement).toBe(input);
    expect(tooltip.tip.value).toBeNull();
  });

  test('a later focus of the same field shows it', () => {
    const { tooltip, input, other } = fieldWithTooltip();

    focusQuietly(input);
    other.focus();
    input.focus();

    expect(tooltip.tip.value?.text).toEqual({
      path: 'general.hostname',
      text: 'The name.',
    });
  });

  test('no element is no focus', () => {
    expect(() => focusQuietly(null)).not.toThrow();
  });
});
