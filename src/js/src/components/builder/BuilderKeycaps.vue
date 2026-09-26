<!--
  A shortcut as key caps, the platform's way: ⇧ ⌘ G on macOS, Ctrl + Shift +
  G on Windows and Linux (keymap.js). Only a picture of the keys: it is
  aria-hidden, so the control it sits in says them in words, as a
  description or with aria-keyshortcuts.
-->
<template>
  <span v-if="caps.length" class="builder-keycaps" aria-hidden="true">
    <template v-for="(cap, index) in caps" :key="index">
      <span v-if="index > 0 && joined" class="builder-keycaps__plus">+</span>
      <kbd>{{ cap }}</kbd>
    </template>
  </span>
</template>

<script setup>
  import { computed } from 'vue';

  import { currentPlatform, keyCaps } from '@/builder/keymap.js';

  const props = defineProps({
    // A key spec, such as 'Mod+K'.
    spec: { type: String, required: true },
  });

  const caps = computed(() => keyCaps(props.spec));
  // macOS sets its key symbols side by side; elsewhere they are joined by +.
  const joined = computed(() => currentPlatform() !== 'mac');
</script>

<style scoped>
  .builder-keycaps {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 2px;
    white-space: nowrap;
  }

  .builder-keycaps kbd {
    min-width: 1.5em;
    padding: 0.05em 0.35em;
    border: 1px solid var(--bx-border-strong);
    border-bottom-width: 2px;
    border-radius: 4px;
    background: var(--bx-surface);
    color: var(--bx-text);
    font-family: inherit;
    font-size: 0.72rem;
    font-weight: 600;
    line-height: 1.3;
    text-align: center;
  }

  .builder-keycaps__plus {
    font-size: 0.7rem;
    color: var(--bx-text-muted);
  }
</style>
