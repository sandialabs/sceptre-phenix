// The paste pre-check of paste-as-keys mode (T35). noVNC sends a code point
// above U+00FF as "?" and U+0080–U+00FF as one Latin-1 byte, and the shim types
// only printable ASCII, Tab and Enter.
import fc from 'fast-check';
import { describe, expect, it, vi } from 'vitest';

import {
  BIDIRECTIONAL_HINT_CHARS,
  PASTE_MS_PER_CHAR,
  PASTE_MS_PER_SHIFTED_CHAR,
  charLabel,
  checkPaste,
  typingTime,
  usePastePrecheck,
} from '@/vnc/pasteCheck.js';

describe('what a paste types', () => {
  it('types printable ASCII, Tab and Enter', () => {
    const text = 'echo "hi"\tok\r\n~ !';
    expect(checkPaste(text)).toMatchObject({
      typed: [...text].length,
      asQuestion: [],
      skipped: [],
      clean: true,
    });
  });

  it('lists the characters above U+00FF as typed as "?"', () => {
    const result = checkPaste('echo “café” → ok 😀');
    expect(result.asQuestion).toEqual(['“', '”', '→', '😀']);
    expect(result.asQuestionCount).toBe(4);
    expect(result.clean).toBe(false);
  });

  it('lists U+0080–U+00FF as skipped', () => {
    const result = checkPaste('café ÿ\u0080 é');
    expect(result.skipped).toEqual(['é', 'ÿ', '\u0080']);
    expect(result.skippedCount).toBe(4);
    expect(result.typed).toBe(5);
  });

  it('skips the control characters other than Tab and Enter', () => {
    const result = checkPaste('a\u0007b\u007f');
    expect(result.skipped).toEqual(['\u0007', '\u007f']);
    expect(result.typed).toBe(2);
  });

  it('counts each character once by its kind', () => {
    fc.assert(
      fc.property(fc.string({ unit: 'grapheme', maxLength: 40 }), (text) => {
        const r = checkPaste(text);
        expect(r.typed + r.skippedCount).toBe([...text].length);
        expect(r.asQuestionCount).toBeLessThanOrEqual(r.typed);
        expect(new Set(r.asQuestion).size).toBe(r.asQuestion.length);
        expect(new Set(r.skipped).size).toBe(r.skipped.length);
        for (const ch of r.asQuestion)
          expect(ch.codePointAt(0)).toBeGreaterThan(0xff);
        for (const ch of r.skipped)
          expect(ch.codePointAt(0)).toBeLessThanOrEqual(0xff);
        expect(r.clean).toBe(r.asQuestionCount === 0 && r.skippedCount === 0);
      }),
      { numRuns: 200 },
    );
  });

  it('estimates the typing time, and suggests the bidirectional mode for long text', () => {
    expect(checkPaste('').seconds).toBe(0);
    expect(checkPaste('a').seconds).toBe(1);
    const long = 'a'.repeat(BIDIRECTIONAL_HINT_CHARS + 1);
    expect(checkPaste(long).seconds).toBe(
      Math.ceil(((BIDIRECTIONAL_HINT_CHARS + 1) * PASTE_MS_PER_CHAR) / 1000),
    );
    expect(checkPaste(long).suggestBidirectional).toBe(true);
    expect(
      checkPaste('a'.repeat(BIDIRECTIONAL_HINT_CHARS)).suggestBidirectional,
    ).toBe(false);
    expect(BIDIRECTIONAL_HINT_CHARS).toBe(5000);
  });

  it('counts a character that needs Shift, "?" for a character above U+00FF included, at two times the time', () => {
    expect([PASTE_MS_PER_CHAR, PASTE_MS_PER_SHIFTED_CHAR]).toEqual([20, 40]);
    // 50 unshifted characters are 1 s, 50 shifted ones 2 s
    const unshifted = "a1-=[];',./` \t\n".repeat(4).slice(0, 50);
    const shifted = 'A!"#$%&()*+:<>?@^_{|}~Z'.repeat(3).slice(0, 50);
    expect(checkPaste(unshifted).seconds).toBe(1);
    expect(checkPaste(shifted).seconds).toBe(2);
    expect(checkPaste('→'.repeat(50)).seconds).toBe(2);
    expect(checkPaste(unshifted + shifted).seconds).toBe(3);
    // a skipped character takes no time
    expect(checkPaste(unshifted + 'é'.repeat(50)).seconds).toBe(1);
  });

  it.each([
    [0, 'about 0 s'],
    [1, 'about 1 s'],
    [59, 'about 59 s'],
    [60, 'about 1 min'],
    [61, 'about 2 min'],
  ])('says %i s as %s', (seconds, text) => {
    expect(typingTime(seconds)).toBe(text);
  });

  it.each([
    ['é', 'é'],
    ['→', '→'],
    ['\u0007', 'U+0007'],
    ['\u0080', 'U+0080'],
    ['\u00a0', 'U+00A0'],
    ['\u007f', 'U+007F'],
  ])('labels %j as %s', (ch, label) => {
    expect(charLabel(ch)).toBe(label);
  });
});

describe('Type anyway and Cancel', () => {
  it('sends a clean paste at once', () => {
    const send = vi.fn();
    const gate = usePastePrecheck(send);
    expect(gate.paste('ls -la\n')).toBe(true);
    expect(send).toHaveBeenCalledWith('ls -la\n');
    expect(gate.pending.value).toBe(null);
  });

  it('holds a paste with characters it cannot type until Type anyway', () => {
    const send = vi.fn();
    const gate = usePastePrecheck(send);
    expect(gate.paste('café')).toBe(false);
    expect(send).not.toHaveBeenCalled();
    expect(gate.pending.value).toMatchObject({ text: 'café', skipped: ['é'] });

    gate.typeAnyway();
    expect(send).toHaveBeenCalledWith('café');
    expect(gate.pending.value).toBe(null);
  });

  it('drops the held paste on Cancel', () => {
    const send = vi.fn();
    const gate = usePastePrecheck(send);
    gate.paste('→');
    gate.cancel();
    expect(gate.pending.value).toBe(null);
    gate.typeAnyway();
    expect(send).not.toHaveBeenCalled();
  });
});
