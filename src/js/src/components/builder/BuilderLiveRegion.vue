<!-- The editor's polite live region (see announce.js), one element per message,
     so a repeat is read too. It is a component of its own, so a message does
     not re-render the view and clear its fields. -->
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

  import { createAnnouncer, unsaidPart } from '@/builder/announce.js';
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

  const announcer = createAnnouncer({
    show: (text) => {
      live.value = { text, key: live.value.key + 1 };
    },
    blocked: modalOpen,
  });

  // The region drops a waiting save-state message once the save state changes,
  // and the part that a dialog said already (see noteDialogSaid in store.js).
  watch(
    () => store.announcementSeq,
    () => {
      const slot = store.announcementSlot;
      const status = store.saveState.status;
      const message = { text: store.announcement, seq: store.announcementSeq };

      announcer.push(message.text, {
        slot,
        stale:
          slot === 'save'
            ? () => staleSaveMessage(status, store.saveState.status)
            : null,
        rest: () => unsaidPart(message, store.dialogSaid),
      });
    },
    { flush: 'sync' },
  );

  onBeforeUnmount(() => announcer.dispose());
</script>
