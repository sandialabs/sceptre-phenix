// The VLAN save with force and the VLAN check as IDs are typed
// (utils/vlanActions.js), with a stand-in dialog and VLAN API.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { checkVlans, role } = vi.hoisted(() => ({
  checkVlans: vi.fn(),
  role: { allowed: () => true },
}));
vi.mock('@/api/vlans.js', () => ({ checkVlans }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => role.allowed(...args),
}));

import {
  CHECK_DELAY_MS,
  createVlanCheck,
  saveWithForce,
} from '@/utils/vlanActions.js';
import { allowAll, allowOnly, denyOnly } from './helpers/permissions.js';

// a dialog that answers each confirm with `answer` and records its options
function fakeBuefy(answer = true) {
  const confirm = vi.fn((opts) =>
    answer ? opts.onConfirm() : opts.onCancel(),
  );
  return { dialog: { confirm } };
}

// a 409 that names these issues
const conflict = (issues) => ({ response: { status: 409, data: { issues } } });

// send(false) fails with err; send(true) resolves 'forced'
const sender = (err) =>
  vi.fn((force) => (force ? Promise.resolve('forced') : Promise.reject(err)));

beforeEach(() => {
  role.allowed = allowAll;
  checkVlans.mockReset();
});

describe('saveWithForce', () => {
  it('returns the first answer when the save does not fail', async () => {
    const send = vi.fn().mockResolvedValue('ok');
    await expect(saveWithForce(fakeBuefy(), 'exp', send)).resolves.toBe('ok');
    expect(send).toHaveBeenCalledExactlyOnceWith(false);
  });

  it('rejects an error that is not a VLAN conflict', async () => {
    const err = { response: { status: 500 } };
    const buefy = fakeBuefy();
    await expect(saveWithForce(buefy, 'exp', sender(err))).rejects.toBe(err);
    expect(buefy.dialog.confirm).not.toHaveBeenCalled();
  });

  // Each row: the issue, a role that may force it, and one that may not.
  const pinned = { kind: 'pin', message: 'VLAN 101 is pinned' };
  it.each([
    [
      'a conflict with an experiment the role may list',
      { ...pinned, experiments: ['exp', 'other'] },
      allowOnly(['vlans', 'list', 'other']),
      allowOnly(['vlans', 'list', 'exp']),
    ],
    [
      'a cluster kind for a role that may list all VLANs',
      { kind: 'unowned', message: 'VLAN 7 has no owner' },
      allowOnly(['vlans', 'list', '*']),
      allowOnly(['vlans', 'list', 'exp']),
    ],
    [
      'a blacklisted pin for a role that may list all VLANs',
      { kind: 'pin-blacklisted', message: 'VLAN 9 is blacklisted' },
      allowAll,
      denyOnly(['vlans', 'list', '*']),
    ],
  ])('asks before it forces %s', async (_, issue, allowed, refused) => {
    const err = conflict([issue]);

    role.allowed = allowed;
    const buefy = fakeBuefy();
    const send = sender(err);
    await expect(saveWithForce(buefy, 'exp', send)).resolves.toBe('forced');
    expect(send.mock.calls).toEqual([[false], [true]]);
    expect(buefy.dialog.confirm.mock.calls[0][0].message).toContain(
      issue.message,
    );

    role.allowed = refused;
    const denied = fakeBuefy();
    await expect(saveWithForce(denied, 'exp', sender(err))).rejects.toBe(err);
    expect(denied.dialog.confirm).not.toHaveBeenCalled();
  });

  it('never forces a conflict with another experiment, for any role', async () => {
    const err = conflict([
      { kind: 'pin', message: 'VLAN 101 is in use by another experiment' },
    ]);
    const buefy = fakeBuefy();
    await expect(saveWithForce(buefy, 'exp', sender(err))).rejects.toBe(err);
    expect(buefy.dialog.confirm).not.toHaveBeenCalled();
  });

  it('resolves null and does not force when the user cancels', async () => {
    const send = sender(conflict([pinned]));
    await expect(saveWithForce(fakeBuefy(false), 'exp', send)).resolves.toBe(
      null,
    );
    expect(send).toHaveBeenCalledExactlyOnceWith(false);
  });
});

describe('createVlanCheck', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  // a promise and the functions that settle it
  function deferred() {
    let resolve, reject;
    const promise = new Promise((res, rej) => {
      resolve = res;
      reject = rej;
    });
    return { promise, resolve, reject };
  }

  const checking = { checking: true, issues: null, failed: false };

  it('applies the issues once typing stops', async () => {
    checkVlans.mockResolvedValue({ data: { issues: [{ kind: 'pin' }] } });
    const apply = vi.fn();
    const run = createVlanCheck(apply);

    run({ experiment: 'exp' });
    expect(apply.mock.calls).toEqual([[checking]]);
    expect(checkVlans).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    expect(checkVlans).toHaveBeenCalledExactlyOnceWith({ experiment: 'exp' });
    expect(apply).toHaveBeenLastCalledWith({
      checking: false,
      issues: [{ kind: 'pin' }],
      failed: false,
    });
  });

  it('marks a failed check as failed', async () => {
    checkVlans.mockRejectedValue(new Error('down'));
    const apply = vi.fn();
    createVlanCheck(apply)({ experiment: 'exp' });

    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    expect(apply).toHaveBeenLastCalledWith({
      checking: false,
      issues: null,
      failed: true,
    });
  });

  it.each([
    ['an answer', (d) => d.resolve({ data: { issues: [{ kind: 'old' }] } })],
    ['a failure', (d) => d.reject(new Error('old'))],
  ])('drops %s to an older check', async (_, settle) => {
    const old = deferred();
    checkVlans.mockReturnValueOnce(old.promise);
    checkVlans.mockResolvedValueOnce({ data: { issues: [] } });
    const apply = vi.fn();
    const run = createVlanCheck(apply);

    run({ experiment: 'a' });
    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    run({ experiment: 'b' });
    settle(old);
    await vi.advanceTimersByTimeAsync(0);
    expect(apply).toHaveBeenLastCalledWith(checking);

    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    expect(apply).toHaveBeenLastCalledWith({
      checking: false,
      issues: [],
      failed: false,
    });
    expect(apply).toHaveBeenCalledTimes(3);
  });

  it('drops a check that cancel stops', async () => {
    const pending = deferred();
    checkVlans.mockReturnValue(pending.promise);
    const apply = vi.fn();
    const run = createVlanCheck(apply);

    run({ experiment: 'a' });
    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    run.cancel();
    pending.resolve({ data: { issues: [] } });
    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    expect(apply.mock.calls).toEqual([[checking]]);
  });
});
