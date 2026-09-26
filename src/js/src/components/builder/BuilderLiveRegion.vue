<!--
  The editor's polite live region.

  Store announcements are paced by the announcer (see announce.js) and shown
  here, one element per message, so a repeat is announced too. It is a
  component of its own so that each message re-renders only this region:
  re-rendering the view would also reset its form fields, and wipe text
  being typed into them.
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

  // A modal dialog makes this region unreadable; its messages wait.
  function modalOpen() {
    try {
      return Boolean(document.querySelector('dialog:modal'));
    } catch {
      // A browser without :modal; Builder dialogs are always modal.
      return Boolean(document.querySelector('dialog[open]'));
    }
  }

  const announcer = createAnnouncer({
    show: (text) => {
      live.value = { text, key: live.value.key + 1 };
    },
    blocked: modalOpen,
  });

  // Store announcements arrive synchronously, so two made in the same tick
  // are both queued rather than the second replacing the first. A save-state
  // message still waiting when the save state has moved on is dropped, so
  // "Offline" is never spoken after the conflict that followed it.
  watch(
    () => store.announcementSeq,
    () => {
      const slot = store.announcementSlot;
      const status = store.saveState.status;

      announcer.push(
        store.announcement,
        slot === 'save'
          ? {
              slot,
              stale: () => staleSaveMessage(status, store.saveState.status),
            }
          : { slot },
      );
    },
    { flush: 'sync' },
  );

  onBeforeUnmount(() => announcer.dispose());
</script>
