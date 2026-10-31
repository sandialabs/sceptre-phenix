import { afterEach, describe, expect, test, vi } from 'vitest';

import {
  fitText,
  fittedHeight,
  MAX_LINES,
} from '@/components/builder/inspector/fitText.js';

// Text boxes two lines tall that grow to fit their text, up to MAX_LINES
// lines, and keep a height the user dragged them to.
describe('fittedHeight', () => {
  test('fits the text, up to the most, and keeps a dragged height', () => {
    expect(fittedHeight({ content: 50, most: 200 })).toBe(50);
    expect(fittedHeight({ content: 500, most: 200 })).toBe(200);
    expect(fittedHeight({ content: 50, most: 200, dragged: 120 })).toBe(120);
    expect(fittedHeight({ content: 150, most: 200, dragged: 120 })).toBe(150);
    expect(fittedHeight({ content: 500, most: 200, dragged: 300 })).toBe(300);
  });
});

// What fitText reads of a <textarea>: lines of 20 pixels, a padding of 4
// and a border of 1 at the top and the bottom, and its scroll parent.
function box({ content, drawn = true }) {
  const parent = { scrollTop: 40, parentElement: null };
  const element = {
    isConnected: true,
    parentElement: parent,
    style: { height: '' },
    // Made auto tall, the box shortens the page, which scrolls back.
    get scrollHeight() {
      if (this.style.height === 'auto') {
        parent.scrollTop = 0;
      }

      return content - 2;
    },
    getClientRects: () => (drawn ? [{}] : []),
    getBoundingClientRect() {
      return { height: Number.parseFloat(this.style.height) || 0 };
    },
  };

  return { element, parent };
}

describe('fitText', () => {
  afterEach(() => vi.unstubAllGlobals());

  function stubStyle() {
    vi.stubGlobal('getComputedStyle', () => ({
      lineHeight: '20px',
      fontSize: '13px',
      paddingTop: '4px',
      paddingBottom: '4px',
      borderTopWidth: '1px',
      borderBottomWidth: '1px',
    }));
  }

  test('gives a box the height of its text, and keeps the page where it was scrolled', () => {
    stubStyle();
    const { element, parent } = box({ content: 50 });

    fitText(element);
    expect(element.style.height).toBe('50px');
    expect(parent.scrollTop).toBe(40);
  });

  test('stops at MAX_LINES lines, then the box scrolls', () => {
    stubStyle();
    const { element } = box({ content: 1000 });

    fitText(element);
    expect(element.style.height).toBe(`${20 * MAX_LINES + 10}px`);
  });

  test('keeps a height the user dragged the box to', () => {
    stubStyle();
    const { element } = box({ content: 50 });

    fitText(element);
    element.style.height = '120px';
    fitText(element);
    expect(element.style.height).toBe('120px');
  });

  test('leaves a box that is not drawn as it is', () => {
    stubStyle();
    const { element } = box({ content: 50, drawn: false });

    fitText(element);
    expect(element.style.height).toBe('');
  });
});
