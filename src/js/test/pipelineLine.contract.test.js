// The SVG path of each line in a SCORCH pipeline graph
// (components/pipeline/line.js), for each relative position of two nodes.
import { describe, expect, test } from 'vitest';

import { DefaultStyleService } from '@/components/pipeline/line.js';

const draw = new DefaultStyleService(125, 75);
const at = (x, y) => ({ x, y });
// The paths hold line breaks and indents only to make the source easy to read.
const path = (start, end) =>
  draw.drawEdge(start, end).replace(/\s+/g, ' ').trim();

describe('drawEdge', () => {
  test('a node in the next column of the same row gets a straight line', () => {
    expect(path(at(70, 100), at(195, 100))).toBe('M 82,100 L82,100 180,100');
  });

  test('a node two columns to the right gets a line that goes down and up', () => {
    expect(path(at(70, 100), at(320, 100))).toBe(
      'M 80 100 l 20 0 C 172.5,100 162.5,130 195,130' +
        'C 227.5,130 217.5,100 290,100 l 20 0',
    );
  });

  test('a node more columns to the right gets a longer line below the row', () => {
    expect(path(at(70, 100), at(570, 100))).toBe(
      'M 80 100 l 20 0 C 172.5,100 162.5,130 195,130' +
        'l 250 0C 477.5,130 467.5,100 540,100 l 20 0',
    );
  });

  test('a node below in the same column gets a vertical line', () => {
    expect(path(at(70, 100), at(70, 175))).toBe('M 70,114 L70,114 70,161');
  });

  test('a node below and to the right gets a line that turns down, then right', () => {
    expect(path(at(70, 100), at(195, 175))).toBe(
      'M 80 100 l 20 0 c 12 0 12 12 12 12 l 0 51 ' +
        'c 0 12 12 12 12 12 l 55 0',
    );
  });

  test('a node above and to the right gets a line that turns up, then right', () => {
    expect(path(at(70, 175), at(195, 100))).toBe(
      'M 84 175 l 55 0 c 12 0 12 -12 12 -12 l 0 -51 ' +
        'c 0 -12 12 -12 12 -12 l 20 0',
    );
  });
});
