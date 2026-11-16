// The Console page's helpers: its terminal colors, text sizes, reconnect
// waits, and how it reads what the user enters and what the terminal shows.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  afterPrompt,
  bufferText,
  clampFontSize,
  FONT_SIZE,
  LineTracker,
  MINIMEGA_COMMANDS,
  quitCommand,
  RECONNECT_DELAYS,
  reconnectDelay,
  terminalTheme,
  transcriptName,
} from '@/components/console/consoleTools.js';
import { contrast } from './helpers/css.js';

const TEXT_COLORS = ['red', 'green', 'yellow', 'blue', 'magenta', 'cyan'];

describe('the terminal theme', () => {
  it.each([
    ['dark', true],
    ['light', false],
  ])('gives the %s theme text of 7:1 or more', (_, dark) => {
    const theme = terminalTheme(dark);
    expect(contrast(theme.foreground, theme.background)).toBeGreaterThanOrEqual(
      7,
    );
    for (const color of TEXT_COLORS) {
      const bright = 'bright' + color[0].toUpperCase() + color.slice(1);
      expect(contrast(theme[color], theme.background)).toBeGreaterThanOrEqual(
        7,
      );
      expect(contrast(theme[bright], theme.background)).toBeGreaterThanOrEqual(
        7,
      );
    }
    expect(
      contrast(theme.selectionForeground, theme.selectionBackground),
    ).toBeGreaterThanOrEqual(7);
    expect(
      contrast(theme.brightBlack, theme.background),
    ).toBeGreaterThanOrEqual(7);
  });

  it('is dark on a dark surface and light on a light one', () => {
    expect(terminalTheme(true).background).toBe('#2b2b2b');
    expect(terminalTheme(false).background).toBe('#fafafa');
  });
});

describe('the text size', () => {
  it.each([
    [14, 14],
    ['16', 16],
    [9, FONT_SIZE.min],
    [99, FONT_SIZE.max],
    [12.6, 13],
    ['big', FONT_SIZE.default],
    [null, FONT_SIZE.min],
  ])('of %j is %d', (size, want) => {
    expect(clampFontSize(size)).toBe(want);
  });

  it('is always a whole size in range, and kept once clamped', () => {
    fc.assert(
      fc.property(fc.double(), (size) => {
        const clamped = clampFontSize(size);
        expect(Number.isInteger(clamped)).toBe(true);
        expect(clamped).toBeGreaterThanOrEqual(FONT_SIZE.min);
        expect(clamped).toBeLessThanOrEqual(FONT_SIZE.max);
        expect(clampFontSize(clamped)).toBe(clamped);
      }),
      { numRuns: 200 },
    );
  });
});

describe('reconnecting', () => {
  it('waits longer before each try, then gives up', () => {
    expect(RECONNECT_DELAYS.map((_, i) => reconnectDelay(i))).toEqual([
      1, 2, 4, 8, 15,
    ]);
    expect(reconnectDelay(RECONNECT_DELAYS.length)).toBe(null);
  });
});

describe('the Commands menu', () => {
  it('enters only the commands that do not change anything', () => {
    const entered = MINIMEGA_COMMANDS.filter((c) => c.run).map(
      (c) => c.command,
    );
    expect(entered).toEqual([
      'vm info',
      'vm info summary',
      'namespace',
      'ns hosts',
      'host',
      'mesh status',
      'vlans',
      'cc clients',
      'cc commands',
      'version',
      'help',
    ]);
    for (const c of MINIMEGA_COMMANDS.filter((c) => !c.run)) {
      expect(c.command).toMatch(/ $/);
    }
  });
});

describe('quit', () => {
  it.each([
    ['quit', 'confirm'],
    ['  quit  ', 'confirm'],
    ['quit 10', 'now'],
    ['quitting', null],
    ['vm info', null],
    ['disconnect', null],
    ['', null],
    [null, null],
  ])('in %j is %j', (line, want) => {
    expect(quitCommand(line)).toBe(want);
  });
});

describe("minimega's prompt", () => {
  it.each([
    ['minimega:/tmp/minimega/minimega$ vm info', 'vm info'],
    ['minimega:/tmp/minimega/minimega[demo]$ quit', 'quit'],
    ['minimega:/tmp/minimega/minimega$ ', ''],
    ['VM output', null],
  ])('in %j leaves %j', (row, want) => {
    expect(afterPrompt(row)).toBe(want);
  });
});

describe('the line the user types', () => {
  const feed = (...chunks) => {
    const tracker = new LineTracker();
    return chunks.flatMap((chunk) => tracker.feed(chunk));
  };

  it.each([
    ['a line, typed', ['q', 'u', 'i', 't', '\r'], ['quit']],
    ['a line, pasted', ['quit\r'], ['quit']],
    ['two lines', ['vm info\rquit\n'], ['vm info', 'quit']],
    ['a corrected line', ['quiz\x7ft\r'], ['quit']],
    ['a line erased', ['vm info\x15quit\r'], ['quit']],
    ['a line given up', ['vm\x03quit\r'], ['quit']],
    ['a line from history', ['\x1b[A', 'quit\r'], [null]],
    ['a line cleared to its start', ['\x1b[A\x05\x15quit\r'], ['quit']],
    ['a completed line', ['qu\t\r'], [null]],
    ['a line edited with SS3 keys', ['q\x1bOD\r'], [null]],
    ['the line after an unknown one', ['\x1b[A\rquit\r'], [null, 'quit']],
    ['nothing entered', ['quit'], []],
  ])('is %s', (_, chunks, want) => {
    expect(feed(...chunks)).toEqual(want);
  });

  it('is what was typed, in any chunks', () => {
    const printable = fc.string({ unit: fc.constantFrom('a', 'b', ' ', 'q') });
    fc.assert(
      fc.property(
        fc.array(printable, { maxLength: 4 }),
        fc.nat({ max: 5 }),
        (lines, cut) => {
          const typed = lines.map((line) => `${line}\r`).join('');
          const at = Math.min(cut, typed.length);
          expect(feed(typed.slice(0, at), typed.slice(at))).toEqual(lines);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('the terminal text', () => {
  const buffer = (rows) => ({
    length: rows.length,
    getLine: (y) => ({
      isWrapped: rows[y].startsWith('\t'),
      translateToString: () => rows[y].replace(/^\t/, ''),
    }),
  });

  it('joins wrapped rows and drops the blank rows at the end', () => {
    expect(bufferText(buffer(['one', 'two', '\tcontinued', '', '  ']))).toBe(
      'one\ntwocontinued',
    );
  });

  it('is empty for an empty terminal', () => {
    expect(bufferText(buffer([]))).toBe('');
    expect(bufferText({ length: 1, getLine: () => undefined })).toBe('');
  });
});

describe('the transcript name', () => {
  const date = new Date(2026, 10, 4, 9, 5, 7);

  it.each([
    ['', 'minimega-console-2026-11-04-090507.txt'],
    ['demo', 'minimega-console-demo-2026-11-04-090507.txt'],
    ['a b/c', 'minimega-console-a_b_c-2026-11-04-090507.txt'],
  ])('in namespace %j is %s', (namespace, want) => {
    expect(transcriptName(date, namespace)).toBe(want);
  });
});
