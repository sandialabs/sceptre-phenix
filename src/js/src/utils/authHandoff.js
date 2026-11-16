// The session token handoff between tabs of this origin. A console tab that a
// link opened with noopener has no sessionStorage, so a user who did not
// choose to stay signed in has no token there. Such a tab asks the signed-in
// phēnix tabs for the token over a BroadcastChannel.
import { toRaw } from 'vue';

import { usePhenixStore } from '@/store.js';
import { tokenExpired } from '@/utils/token.js';

/** The BroadcastChannel of the token handoff. */
export const AUTH_CHANNEL = 'phenix-auth';
/** How long a tab waits for another tab to answer, in milliseconds. */
export const TOKEN_WAIT_MS = 500;

const channelOf = () =>
  typeof globalThis.BroadcastChannel === 'function'
    ? new globalThis.BroadcastChannel(AUTH_CHANNEL)
    : null;

/**
 * Answers the token requests of other tabs while this tab is signed in.
 * App.vue runs it for every signed-in tab.
 *
 * @param {() => { auth: boolean, token: string|null, username: string|null,
 *   role: object|null }} [store] the store to answer from
 * @returns {() => void} stops answering
 */
export function answerTokenRequests(store = usePhenixStore) {
  const channel = channelOf();
  if (!channel) return () => {};
  channel.onmessage = ({ data }) => {
    if (data?.type !== 'token-request') return;
    const { auth, token, username, role } = store();
    if (!auth || !token || tokenExpired(token)) return;
    channel.postMessage({
      type: 'token',
      id: data.id,
      token,
      user: { username, role: toRaw(role) },
    });
  };
  return () => channel.close();
}

function requestToken() {
  const channel = channelOf();
  if (!channel) return Promise.resolve(null);
  const id = Array.from(crypto.getRandomValues(new Uint32Array(2))).join('-');
  return new Promise((resolve) => {
    const done = (answer) => {
      clearTimeout(timer);
      channel.close();
      resolve(answer);
    };
    const timer = setTimeout(() => done(null), TOKEN_WAIT_MS);
    channel.onmessage = ({ data }) => {
      if (data?.type === 'token' && data.id === id && data.token) done(data);
    };
    channel.postMessage({ type: 'token-request', id });
  });
}

/**
 * Signs this tab in with the token of another signed-in tab, for this
 * session only. The caller shows the sign-in page when it resolves to false.
 *
 * @returns {Promise<boolean>} true when the tab is signed in
 */
export async function signInFromOtherTab() {
  const store = usePhenixStore();
  if (store.auth && store.token) return true;
  const answer = await requestToken();
  if (!answer) return false;
  store.login({ token: answer.token, user: answer.user }, false, false);
  return true;
}
