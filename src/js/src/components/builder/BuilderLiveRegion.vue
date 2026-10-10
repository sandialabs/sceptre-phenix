<!--
  The editor's polite live region.

  The announcer paces store announcements (see announce.js), and this
  region shows them, one element per message, so screen readers announce a
  repeat too. It is a component of its own so that each message re-renders
  only this region: a re-render of the view would also reset its form
  fields, and clear text that the user is typing into them.
-->
<template>
  <p
    class="builder-visually-hidden"
    role="status"
    aria-live="polite"
    data-testid="builder-live-region">
    <span :key="live.key">{{ live.text }}</span>
  </p>
</template>

<script setup>
  import { onBeforeUnmount, ref, watch } from 'vue';

  import { createAnnouncer } from '@/builder/announce.js';
  import { staleSaveMessage } from '@/builder/autosave.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  const live = ref({ text: '', key: 0 });

  // A modal dialog makes this region unreadable, so its messages wait.
  function modalOpen() {
    try {
      return Boolean(document.querySelector('dialog:modal'));
    } catch {
      // A browser without :modal. Builder dialogs are always modal.
      return Boolean(document.querySelector('dialog[open]'));
    }
  }

  // A test hook: browser tests that do not test the pacing shorten the hold
  // (see e2e/tests/builder-support.js). Nothing else sets it.
  const testHold = Number(window.__BUILDER_ANNOUNCE_HOLD_MS__);

  const announcer = createAnnouncer({
    show: (text) => {
      live.value = { text, key: live.value.key + 1 };
    },
    blocked: modalOpen,
    hold: testHold > 0 ? testHold : undefined,
  });

  // Store announcements arrive synchronously, so two in the same tick both
  // go in the queue, and the second does not replace the first. The region
  // drops a save-state message that still waits after the save state
  // changed. So "Offline" is never spoken after the conflict that followed
  // it, nor that the session ended after the user signed in again.
  watch(
    () => store.announcementSeq,
    () => {
      const slot = store.announcementSlot;
      const status = store.saveState.status;
      const ended = store.saveState.signInNeeded;

      announcer.push(
        store.announcement,
        slot === 'save'
          ? {
              slot,
              stale: () =>
                staleSaveMessage(status, store.saveState.status) ||
                (ended && !store.saveState.signInNeeded),
            }
          : { slot },
      );
    },
    { flush: 'sync' },
  );

  onBeforeUnmount(() => announcer.dispose());
</script>
