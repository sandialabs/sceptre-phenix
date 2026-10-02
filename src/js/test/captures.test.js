import { describe, expect, test } from 'vitest';
import {
  applyStopCaptureUpdate,
  captureFilename,
  isInterfaceCapturing,
  hasMultipleCaptures,
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

describe('hasMultipleCaptures', () => {
  test('returns false when zero or one captures are running', () => {
    expect(hasMultipleCaptures([])).toBe(false);
    expect(hasMultipleCaptures(null)).toBe(false);
    expect(hasMultipleCaptures(undefined)).toBe(false);
    expect(hasMultipleCaptures([{ interface: 0 }])).toBe(false);
  });

  test('returns true when more than one capture is running', () => {
    expect(hasMultipleCaptures([{ interface: 0 }, { interface: 1 }])).toBe(
      true,
    );
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
