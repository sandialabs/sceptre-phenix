// What the app starts for a signed-in user and tears down on sign-out, across
// every sign-in and sign-out, whether the page loaded signed in or not.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';
import { nextTick, ref } from 'vue';

import { followSignIn } from '@/utils/signIn.js';

function follow(signedIn, authDisabled = false) {
  const auth = ref(signedIn);
  const calls = [];
  const stop = followSignIn(auth, {
    authDisabled,
    onSignIn: () => calls.push('in'),
    onSignOut: () => calls.push('out'),
  });
  return { auth, calls, stop };
}

describe('followSignIn', () => {
  it('tears down on sign-out after a page loaded signed in, and starts again for the next user', async () => {
    const { auth, calls } = follow(true);
    expect(calls).toEqual(['in']);

    auth.value = false;
    await nextTick();
    expect(calls).toEqual(['in', 'out']);

    auth.value = true;
    await nextTick();
    expect(calls).toEqual(['in', 'out', 'in']);
  });

  it('starts on the first sign-in of a page loaded signed out', async () => {
    const { auth, calls } = follow(false);
    expect(calls).toEqual([]);

    auth.value = true;
    await nextTick();
    expect(calls).toEqual(['in']);
  });

  it('starts once, and never tears down, with auth disabled', async () => {
    const { auth, calls } = follow(false, true);
    auth.value = true;
    await nextTick();
    auth.value = false;
    await nextTick();
    expect(calls).toEqual(['in']);
  });

  it('stops following when asked', async () => {
    const { auth, calls, stop } = follow(true);
    stop();
    auth.value = false;
    await nextTick();
    expect(calls).toEqual(['in']);
  });

  // each step signs in or out, or lets Vue run the watchers
  const steps = fc.array(fc.oneof(fc.boolean(), fc.constant('tick')), {
    maxLength: 20,
  });

  it('alternates start and teardown, started exactly while signed in, over any run of sign-ins', async () => {
    await fc.assert(
      fc.asyncProperty(fc.boolean(), steps, async (loadedIn, run) => {
        const { auth, calls, stop } = follow(loadedIn);
        for (const step of run) {
          if (step === 'tick') await nextTick();
          else auth.value = step;
        }
        await nextTick();
        stop();
        calls.forEach((call, i) => expect(call).toBe(i % 2 ? 'out' : 'in'));
        expect(calls.at(-1) === 'in').toBe(auth.value);
      }),
    );
  });
});
