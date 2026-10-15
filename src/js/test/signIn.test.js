// What the app starts for a signed-in user, and tears down on sign-out, is
// followed across every sign-in and sign-out, whether the page loaded signed
// in or not.
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
});
