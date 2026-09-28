<!--
  The warning before a logout that would delete Builder Flow changes the
  server does not have (see utils/logout.js).

  An alert dialog (APG Alert and Message Dialogs pattern) on a native
  <dialog> opened with showModal(), so the page behind it is inert. Its
  message is the dialog's description, and focus starts on the choice that
  keeps the changes. An automatic logout counts down a minute on screen
  and in the tab's title: the countdown is read with the description when
  the dialog opens, and once more near the end, not every second. Export
  saves one draft per click, as a browser may block a second download
  from one click. On the Builder's page, a session that expired can sign
  in again there, which stops the countdown and opens the Builder's
  sign-in (see builder/signin.js). It takes the Builder's theme, which
  follows the system's unless the viewer chose one.
-->
<template>
  <dialog
    v-if="warning"
    ref="panel"
    class="logout-warning"
    role="alertdialog"
    aria-modal="true"
    aria-labelledby="logout-warning-title"
    :aria-describedby="describedby"
    :data-theme="theme"
    data-testid="logout-warning"
    @cancel.prevent="stay"
    @close="keepOpen"
    @keydown="onKeydown">
    <h2 id="logout-warning-title">{{ text.title }}</h2>
    <p id="logout-warning-message">{{ text.message }}</p>
    <p
      v-if="warning.secondsLeft !== null"
      id="logout-warning-countdown"
      class="logout-warning__countdown"
      data-testid="logout-warning-countdown">
      {{ countdownText(warning.secondsLeft) }}
    </p>
    <!-- Rendered, empty, from the start, so screen readers know them before
         their first message. -->
    <p class="logout-warning__status" role="status">{{ status }}</p>
    <p
      class="logout-warning__hidden"
      aria-live="assertive"
      aria-atomic="true"
      data-testid="logout-warning-notice">
      {{ warning.notice }}
    </p>

    <div class="logout-warning__actions">
      <button
        v-for="draft in warning.drafts"
        :key="draft.key"
        type="button"
        data-testid="logout-warning-export"
        @click="exportDraft(draft)">
        {{ warning.drafts.length > 1 ? `Export ${draft.name}` : 'Export' }}
      </button>
      <button
        v-if="warning.canStay"
        ref="stayButton"
        type="button"
        data-testid="logout-warning-stay"
        @click="stay">
        {{ text.stay }}
      </button>
      <button
        v-if="warning.canSignIn"
        ref="signInButton"
        type="button"
        data-testid="logout-warning-signin"
        @click="signInAgain">
        {{ text.signIn }}
      </button>
      <button
        ref="confirmButton"
        type="button"
        class="logout-warning__danger"
        data-testid="logout-warning-confirm"
        @click="logOut">
        {{ text.confirm }}
      </button>
    </div>
  </dialog>
</template>

<script setup>
  import { computed, nextTick, ref, watch } from 'vue';

  import { draftExport } from '@/builder/session.js';
  import { readStoredTheme, resolveTheme } from '@/builder/theme.js';
  import { usePhenixStore } from '@/store.js';
  import {
    countdownText,
    countdownTitle,
    logoutWarningText,
  } from '@/utils/logout.js';

  const phenix = usePhenixStore();

  const panel = ref(null);
  const stayButton = ref(null);
  const signInButton = ref(null);
  const confirmButton = ref(null);
  const status = ref('');
  const theme = ref('light');
  // Where focus was when the warning opened, and whether the user chose to
  // stay or to sign in again: any other close logs out.
  let previous = null;
  let staying = false;

  const warning = computed(() => phenix.logoutWarning);
  const text = computed(() =>
    warning.value ? logoutWarningText(warning.value) : {},
  );
  const describedby = computed(() =>
    warning.value?.secondsLeft === null
      ? 'logout-warning-message'
      : 'logout-warning-message logout-warning-countdown',
  );

  function currentTheme() {
    try {
      return resolveTheme(
        readStoredTheme(window.localStorage),
        window.matchMedia?.bind(window),
      );
    } catch {
      return 'light';
    }
  }

  function stay() {
    staying = Boolean(warning.value?.canStay);
    phenix.answerLogoutWarning('stay');
  }

  // The Builder's sign-in opens once the warning has gone, and gives focus
  // back where it was when it closes.
  function signInAgain() {
    staying = Boolean(warning.value?.canSignIn);
    phenix.answerLogoutWarning('signin');
  }

  function logOut() {
    phenix.answerLogoutWarning('logout');
  }

  // The browser may close a modal dialog on its own after Escape; the
  // warning stays until it is answered.
  function keepOpen() {
    if (warning.value && panel.value && !panel.value.open) {
      panel.value.showModal();
    }
  }

  // Saves a file from inside the dialog: the page behind it is inert.
  function download({ name, text: contents }) {
    const url = URL.createObjectURL(
      new Blob([contents], { type: 'application/json;charset=utf-8' }),
    );
    const link = document.createElement('a');

    link.href = url;
    link.download = name;
    link.hidden = true;
    panel.value.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 30000);

    return name;
  }

  async function exportDraft({ key, name }) {
    status.value = '';

    const file = await draftExport({ username: phenix.username, key, name });

    if (!panel.value) {
      return;
    }

    status.value = file
      ? `Saved ${download(file)}.`
      : 'This browser no longer holds these changes.';
  }

  // Where focus starts, and goes when Stay signed in is taken away: the
  // choice that keeps the changes, or else the only one left.
  function firstChoice() {
    return (
      stayButton.value ||
      signInButton.value ||
      panel.value?.querySelector('[data-testid="logout-warning-export"]') ||
      confirmButton.value
    );
  }

  // Escape keeps the session where it can, as Stay signed in does; Tab and
  // Shift+Tab wrap inside the dialog rather than leave for the browser's
  // own controls.
  function onKeydown(event) {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      stay();

      return;
    }

    if (event.key !== 'Tab') {
      return;
    }

    const stops = [...panel.value.querySelectorAll('button')];
    const first = stops[0];
    const last = stops[stops.length - 1];

    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  // Staying, focus goes back where it was. Logging out, what had it goes
  // with the session, so it waits in the page's main region, which the
  // sign-in page fills; so it does when what had it is gone or hidden, or
  // nothing had it.
  function restoreFocus() {
    let target = staying ? previous : null;

    if (
      !target?.isConnected ||
      target === document.body ||
      target.getClientRects().length === 0
    ) {
      target = document.getElementById('main');

      if (target && !target.hasAttribute('tabindex')) {
        target.setAttribute('tabindex', '-1');
      }
    }

    target?.focus();
    previous = null;
  }

  watch(
    () => Boolean(warning.value),
    async (open) => {
      if (!open) {
        restoreFocus();

        return;
      }

      previous = document.activeElement;
      staying = false;
      status.value = '';
      theme.value = currentTheme();
      await nextTick();
      panel.value?.showModal();
      firstChoice()?.focus();
    },
    { flush: 'post' },
  );

  // An expired session takes Stay signed in away; focus on it moves on.
  watch(
    () => warning.value?.canStay,
    (canStay, before) => {
      if (
        before &&
        canStay === false &&
        !panel.value?.contains(document.activeElement)
      ) {
        firstChoice()?.focus();
      }
    },
    { flush: 'post' },
  );

  // The countdown shows in the tab's title too, which a hidden tab shows.
  watch(
    () => warning.value?.secondsLeft ?? null,
    (secondsLeft) => {
      document.title = countdownTitle(document.title, secondsLeft);
    },
  );
</script>

<style scoped>
  /* The Builder's colors (builder.css), which this dialog cannot rely on:
     it shows on every page. */
  .logout-warning {
    --lw-bg: #ffffff;
    --lw-hover: #f4f6f9;
    --lw-text: #1b2330;
    --lw-border: #7b8494;
    --lw-danger: #a4262c;
    --lw-focus: #0a58ca;

    box-sizing: border-box;
    width: min(32rem, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    overflow: auto;
    padding: 1rem;
    color: var(--lw-text);
    background: var(--lw-bg);
    border: 1px solid var(--lw-border);
    border-radius: 6px;
  }

  .logout-warning[data-theme='dark'] {
    --lw-bg: #1e2631;
    --lw-hover: #2a3441;
    --lw-text: #e8edf5;
    --lw-border: #7b8798;
    --lw-danger: #ef8b90;
    --lw-focus: #9dc4ff;
  }

  .logout-warning::backdrop {
    background: rgba(9, 12, 18, 0.55);
  }

  .logout-warning h2 {
    margin: 0 0 0.5rem;
    font-size: 1.1rem;
    font-weight: 700;
    color: inherit;
  }

  /* A draft's or a field's name can be long and unbroken. */
  .logout-warning p {
    margin: 0 0 0.75rem;
    overflow-wrap: anywhere;
  }

  .logout-warning .logout-warning__countdown {
    font-weight: 700;
  }

  .logout-warning .logout-warning__status:empty {
    margin: 0;
  }

  .logout-warning .logout-warning__hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: -1px;
    padding: 0;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
    border: 0;
  }

  .logout-warning__actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  /* An Export button names its draft's file, which can be long. */
  .logout-warning button {
    max-width: 100%;
    overflow-wrap: anywhere;
    padding: 0.4rem 0.75rem;
    font: inherit;
    color: var(--lw-text);
    background: var(--lw-bg);
    border: 1px solid var(--lw-border);
    border-radius: 6px;
    cursor: pointer;
  }

  .logout-warning button:hover {
    background: var(--lw-hover);
  }

  .logout-warning button:focus-visible {
    outline: 2px solid var(--lw-focus);
    outline-offset: 2px;
  }

  .logout-warning .logout-warning__danger {
    color: var(--lw-danger);
    border-color: var(--lw-danger);
  }

  @media (forced-colors: active) {
    .logout-warning {
      border-color: CanvasText;
    }

    .logout-warning button,
    .logout-warning .logout-warning__danger {
      color: ButtonText;
      background: ButtonFace;
      border-color: ButtonText;
    }

    .logout-warning button:focus-visible {
      outline-color: Highlight;
    }
  }
</style>
