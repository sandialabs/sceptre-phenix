import fc from 'fast-check';
import { describe, expect, test } from 'vitest';
import {
  applyStopCaptureUpdate,
  captureFilename,
  isInterfaceCapturing,
} from '@/utils/captures.js';

describe('applyStopCaptureUpdate', () => {
  const captures = [
    { vm: 'test-vm', interface: 0, filename: 'iface0.pcap' },
    { vm: 'test-vm', interface: 1, filename: 'iface1.pcap' },
  ];

  // interface 0 is falsy, but still names an interface
  test.each([0, 1])('stopping interface %i leaves the other running', (n) => {
    expect(applyStopCaptureUpdate(captures, { interface: n })).toEqual(
      captures.filter((c) => c.interface != n),
    );
  });

  // a VM's captures, on a few interfaces, now and then two on one
  const capturesArb = fc.array(
    fc.record({ interface: fc.nat(4), filename: fc.string() }),
    { maxLength: 8 },
  );
  const isSubsequence = (sub, list) =>
    list.reduce((i, c) => i + (c === sub[i]), 0) === sub.length;

  test('stops only that interface, keeping the rest in order', () => {
    fc.assert(
      fc.property(capturesArb, fc.nat(5), (captures, n) => {
        const left = applyStopCaptureUpdate(captures, { interface: n });
        expect(isInterfaceCapturing(left, n)).toBe(false);
        expect(isSubsequence(left, captures)).toBe(true);
        expect(captures.length - left.length).toBe(
          captures.filter((c) => c.interface === n).length,
        );
        for (let m = 0; m <= 5; m++) {
          if (m !== n) {
            expect(isInterfaceCapturing(left, m)).toBe(
              isInterfaceCapturing(captures, m),
            );
          }
        }
        expect(applyStopCaptureUpdate(left, { interface: n })).toEqual(left);
      }),
    );
  });

  test('clears all captures when no result is provided (stop-all)', () => {
    expect(applyStopCaptureUpdate(captures, undefined)).toEqual([]);
  });

  test('clears all captures when result has no interface field', () => {
    expect(applyStopCaptureUpdate(captures, {})).toEqual([]);
  });

  test('handles an empty/nil captures list without throwing', () => {
    expect(applyStopCaptureUpdate(null, { interface: 0 })).toEqual([]);
    expect(applyStopCaptureUpdate(undefined, { interface: 0 })).toEqual([]);
    expect(applyStopCaptureUpdate([], { interface: 0 })).toEqual([]);
  });
});

describe('isInterfaceCapturing', () => {
  const captures = [
    { vm: 'test-vm', interface: 0, filename: 'iface0.pcap' },
    { vm: 'test-vm', interface: 1, filename: 'iface1.pcap' },
  ];

  test.each([0, 1])(
    'returns true for interface %i, which is capturing',
    (n) => {
      expect(isInterfaceCapturing(captures, n)).toBe(true);
    },
  );

  test('returns false when the interface has no running capture', () => {
    expect(isInterfaceCapturing(captures, 2)).toBe(false);
  });

  test('returns false for an empty/nil captures list', () => {
    expect(isInterfaceCapturing([], 0)).toBe(false);
    expect(isInterfaceCapturing(null, 0)).toBe(false);
    expect(isInterfaceCapturing(undefined, 0)).toBe(false);
  });
});

describe('captureFilename', () => {
  test('names the file after the VM, interface, and time', () => {
    expect(captureFilename('web', 0, '2026-09-28_0914')).toBe(
      'web_0_2026-09-28_0914.pcap',
    );
    expect(captureFilename('web', 2, '2026-09-28_0914')).toBe(
      'web_2_2026-09-28_0914.pcap',
    );
  });
});
