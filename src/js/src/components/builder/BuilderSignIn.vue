<!--
  Signing in again without leaving Builder Flow (see builder/signin.js).

  When the session ends while the Builder is open (a request refused, or
  the token expiring), a modal dialog asks for the password again, for the
  same user: the username is shown, not typed, as signing in as someone
  else goes through Logout. Signing in keeps the new token where the app
  keeps it and sends the changes the server refused meanwhile, which the
  live region says. A wrong password is said in the dialog, and focus goes
  back to the field. Cancel keeps the changes queued in this browser; a
  notice then says the session has ended and offers Sign in again, as
  Retry saving does.

  The token's expiry is timed while the Builder is open, and checked again
  when the page shows, as a hidden tab's timers run late.
-->
<template>
  <div
    v-if="signIn.needed && !signIn.open"
    class="builder-panel builder-signin-notice"
    data-testid="builder-signin-notice">
    <p>
      <builder-icon name="warning" :size="14" />
      <span>
        Your session has ended. Changes not saved yet stay in this browser until
        you sign in again.
      </span>
    </p>
    <button
      ref="noticeButton"
      type="button"
      class="builder-button builder-button--primary"
      data-testid="signin-again"
      aria-haspopup="dialog"
      @click="requestSignIn">
      Sign in again
    </button>
  </div>

  <builder-dialog
    v-if="signIn.open"
    class="builder-signin"
    title="Sign in again"
    title-id="builder-signin-title"
    describedby="builder-signin-message"
    data-testid="builder-signin"
    @close="cancel">
    <form novalidate @submit.prevent="submit">
      <p id="builder-signin-message">
        Your session has ended. Sign in again to keep working; changes not saved
        yet stay in this browser until then.
      </p>

      <div class="builder-field">
        <label for="builder-signin-user">Username</label>
        <input
          id="builder-signin-user"
          :value="phenix.username"
          type="text"
          name="username"
          autocomplete="username"
          readonly
          aria-describedby="builder-signin-user-hint"
          data-testid="signin-username" />
        <p id="builder-signin-user-hint" class="builder-hint">
          To sign in as someone else, cancel, then log out.
        </p>
      </div>

      <div class="builder-field">
        <label for="builder-signin-password">Password</label>
        <input
          id="builder-signin-password"
          v-model="password"
          type="password"
          name="password"
          autocomplete="current-password"
          required
          :aria-invalid="invalid('password')"
          :aria-describedby="describedBy('password')"
          data-testid="signin-password" />
      </div>

      <p
        id="builder-signin-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="signin-error">{{
          error.text
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="signin-cancel"
          @click="cancel">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="signin-submit"
          :aria-disabled="busy || undefined"
          :aria-busy="busy || undefined">
          {{ busy ? 'Signing in…' : 'Sign in' }}
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';

  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import { useFieldError } from './dialogs/message.js';

  import { resumeBuilderSaves } from '@/builder/session.js';
  import {
    declineSignIn,
    hostSignIn,
    requestSignIn,
    sessionEnded,
    signIn,
    signInAgain,
    signInError,
    tokenExpiry,
  } from '@/builder/signin.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { usePhenixStore } from '@/store.js';

  const emit = defineEmits(['signed-in']);

  const builder = useBuilderStore();
  const phenix = usePhenixStore();

  const password = ref('');
  const busy = ref(false);
  const noticeButton = ref(null);
  const { error, invalid, describedBy, fail } = useFieldError(
    'builder-signin-error',
    { password: 'builder-signin-password' },
  );

  // A password sign-in works for a session with a JWT; proxy
  // authentication takes none.
  const stopHosting = hostSignIn({
    available: () =>
      import.meta.env.VITE_AUTH !== 'proxy' &&
      Boolean(phenix.auth && phenix.username) &&
      tokenExpiry(phenix.token) !== null,
    busy: () => phenix.loggingOut,
  });

  async function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    if (!password.value) {
      await fail('Enter your password.', 'password');

      return;
    }

    busy.value = true;
    error.clear();

    try {
      const { changes } = await signInAgain({
        username: phenix.username,
        password: password.value,
        renew: (response) => phenix.renewLogin(response),
        resume: resumeBuilderSaves,
      });

      password.value = '';
      builder.announce(
        changes > 0
          ? 'Signed in again. Saving your changes.'
          : 'Signed in again.',
      );
      emit('signed-in');
    } catch (failure) {
      await fail(signInError(failure), 'password');
      document.getElementById('builder-signin-password')?.select();
    } finally {
      busy.value = false;
    }
  }

  // Focus goes back where it was (see BuilderDialog), or, when that has
  // gone, to the notice's Sign in again.
  async function cancel() {
    password.value = '';
    error.clear();
    declineSignIn();
    builder.announce(
      'Not signed in. Changes not saved yet stay in this browser.',
    );

    await nextTick();

    const active = document.activeElement;

    if (!active || active === document.body) {
      noticeButton.value?.focus();
    }
  }

  // BuilderDialog focuses its panel as it opens; the password field takes
  // it from there.
  watch(
    () => signIn.open,
    async (open) => {
      if (open) {
        await nextTick();
        document.getElementById('builder-signin-password')?.focus();
      }
    },
    { flush: 'post' },
  );

  // The session ends as the token expires: timed from its claims, and
  // checked again when the page shows. A timer waits at most about 24
  // days. A new token that has expired already as it arrives says this
  // device's clock runs ahead of the server's: the server's refusals tell
  // instead, rather than the dialog opening again at once.
  const MAX_WAIT_MS = 2 ** 31 - 1;
  let expiryTimer = null;
  let skewed = null;

  function followExpiry() {
    clearTimeout(expiryTimer);
    expiryTimer = null;

    const expiry = phenix.auth ? tokenExpiry(phenix.token) : null;

    if (
      expiry === null ||
      phenix.token === skewed ||
      document.visibilityState === 'hidden'
    ) {
      return;
    }

    const left = expiry - Date.now();

    if (left <= 0) {
      sessionEnded();

      return;
    }

    expiryTimer = setTimeout(followExpiry, Math.min(left, MAX_WAIT_MS));
  }

  watch(
    () => phenix.token,
    (token) => {
      skewed = (tokenExpiry(token) ?? Infinity) <= Date.now() ? token : null;
      followExpiry();
    },
  );

  onMounted(() => {
    document.addEventListener('visibilitychange', followExpiry);
    followExpiry();
  });

  onBeforeUnmount(() => {
    clearTimeout(expiryTimer);
    document.removeEventListener('visibilitychange', followExpiry);
    stopHosting();
  });
</script>

<style scoped>
  /* The warning sign beside the text, then the button, which wraps below
     in a narrow window. */
  .builder-signin-notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem 0.6rem;
    padding: 0.6rem 0.75rem;
  }

  .builder-signin-notice p {
    display: flex;
    align-items: flex-start;
    gap: 0.35rem;
    flex: 1 1 16rem;
    min-width: 0;
    margin: 0;
    overflow-wrap: anywhere;
  }

  .builder-signin-notice p > :first-child {
    flex: none;
    margin-top: 0.2em;
    color: var(--bx-warning);
  }

  .builder-signin {
    width: min(26rem, 92vw);
  }

  .builder-signin p {
    margin: 0 0 0.75rem;
  }

  .builder-signin .builder-hint {
    margin: 0.2rem 0 0;
  }

  /* The username is shown, not typed: it keeps its text readable but does
     not look like a box to type in. */
  .builder-signin input[readonly] {
    border-style: dashed;
    background: var(--bx-bg-alt);
    color: var(--bx-text);
  }

  .builder-signin .builder-dialog__actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 0.5rem;
  }
</style>
