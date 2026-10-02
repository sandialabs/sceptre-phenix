import { afterEach, describe, expect, test, vi } from 'vitest';

import { fitHeaderLabels, followHeaderLabels } from '@/builder/headerLabels.js';

// An editor header whose counts are laid out by `layout`: for each step of
// data-labels, where the counts are and how tall the header is. Records
// each step laid out.
function fakeHeader(layout) {
  const tried = [];
  const header = {
    dataset: {},
    get offsetHeight() {
      return layout[header.dataset.labels].height ?? 37;
    },
    querySelector: (selector) =>
      selector === ':scope > .builder-counts' ? counts : null,
  };
  const counts = {
    getBoundingClientRect() {
      const { left, top = 8, width = 300 } = layout[header.dataset.labels];

      tried.push(header.dataset.labels);

      return { left, top, right: left + width, bottom: top + 30 };
    },
  };

  return { header, tried };
}

describe('fitHeaderLabels', () => {
  test('shows every label when they leave the counts where they are with none', () => {
    const { header, tried } = fakeHeader({
      none: { left: 610 },
      some: { left: 610 },
      all: { left: 610.2 },
    });

    expect(fitHeaderLabels(header)).toBe('all');
    expect(header.dataset.labels).toBe('all');
    expect(tried).toEqual(['none', 'all']);
  });

  test('takes a step when the labels would move the counts', () => {
    const { header } = fakeHeader({
      none: { left: 610 },
      some: { left: 610 },
      all: { left: 525 },
    });

    expect(fitHeaderLabels(header)).toBe('some');
    expect(header.dataset.labels).toBe('some');
  });

  test('shows none when even the fewest would move, squeeze or wrap them', () => {
    for (const some of [
      { left: 590 },
      { left: 610, width: 280 },
      { left: 610, height: 81 },
    ]) {
      const { header, tried } = fakeHeader({
        none: { left: 610 },
        some,
        all: { left: 500 },
      });

      expect(fitHeaderLabels(header)).toBe('none');
      expect(header.dataset.labels).toBe('none');
      expect(tried).toEqual(['none', 'all', 'some']);
    }
  });

  test('leaves a header without counts alone', () => {
    const header = { dataset: {}, querySelector: () => null };

    expect(fitHeaderLabels(header)).toBe('');
    expect(header.dataset).toEqual({});
    expect(fitHeaderLabels(null)).toBe('');
  });
});

describe('followHeaderLabels', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  // Observers that record what they observe, and run their callback when
  // told to.
  function stubObservers() {
    const made = {};

    for (const name of ['ResizeObserver', 'MutationObserver']) {
      vi.stubGlobal(
        name,
        class {
          constructor(callback) {
            made[name] = this;
            this.callback = callback;
            this.observed = [];
            this.disconnect = vi.fn();
          }

          observe(target, options) {
            this.observed.push({ target, options });
          }
        },
      );
    }

    return made;
  }

  test('fits the labels when the page resizes and when the header’s text changes, until stopped', () => {
    const made = stubObservers();
    const { header } = fakeHeader({
      none: { left: 610 },
      some: { left: 610 },
      all: { left: 525 },
    });
    const page = {};

    const stop = followHeaderLabels(header, page);

    expect(made.ResizeObserver.observed).toEqual([
      { target: page, options: undefined },
    ]);
    expect(made.MutationObserver.observed).toEqual([
      {
        target: header,
        options: { childList: true, characterData: true, subtree: true },
      },
    ]);
    // Not before the page's size is first reported.
    expect(header.dataset.labels).toBeUndefined();

    made.ResizeObserver.callback();
    expect(header.dataset.labels).toBe('some');
    header.dataset.labels = 'all';
    made.MutationObserver.callback();
    expect(header.dataset.labels).toBe('some');

    stop();
    expect(made.ResizeObserver.disconnect).toHaveBeenCalledOnce();
    expect(made.MutationObserver.disconnect).toHaveBeenCalledOnce();
  });

  test('does nothing without the observers', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const header = { dataset: {} };

    const stop = followHeaderLabels(header, {});
    stop();

    expect(header.dataset).toEqual({});
  });
});
