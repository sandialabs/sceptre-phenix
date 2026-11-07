<!-- The Keys panel of the console top bar (M6a): keys that the browser
     keeps for itself, Hold buttons for sticky modifier keys, and "Release
     held keys". -->
<script setup>
  import { CHORD_HINT } from './chord.js';
  import { CTRL_ALT_F, SEND_KEYS } from './keysyms.js';

  defineProps({
    // the Hold keys that are down: 'ctrl', 'alt', 'win'
    held: { type: Array, default: () => [] },
    // why the console cannot send keys now, or null
    disabledReason: { type: String, default: null },
  });

  const emit = defineEmits(['send', 'hold', 'release']);

  const HOLD = [
    { name: 'ctrl', label: 'Ctrl' },
    { name: 'alt', label: 'Alt' },
    { name: 'win', label: 'Win' },
  ];
</script>

<template>
  <div class="vnc-keys">
    <p v-if="disabledReason" class="vnc-panel-note">{{ disabledReason }}</p>
    <h2 class="vnc-panel-title">Send keys</h2>
    <div class="vnc-panel-row vnc-panel-row--single">
      <b-button
        v-for="key in SEND_KEYS"
        :key="key.name"
        size="is-small"
        :aria-disabled="disabledReason ? 'true' : undefined"
        @click="disabledReason || emit('send', key.name)">
        {{ key.label }}
      </b-button>
    </div>
    <div
      class="vnc-panel-row vnc-panel-row--single"
      role="group"
      aria-label="Ctrl+Alt+F1 to F12">
      <span class="vnc-panel-label" aria-hidden="true">Ctrl+Alt+</span>
      <b-button
        v-for="key in CTRL_ALT_F"
        :key="key.name"
        size="is-small"
        :aria-label="`Ctrl+Alt+${key.label}`"
        :aria-disabled="disabledReason ? 'true' : undefined"
        @click="disabledReason || emit('send', key.name)">
        {{ key.label }}
      </b-button>
    </div>
    <h2 class="vnc-panel-title">Hold</h2>
    <div class="vnc-panel-row">
      <b-button
        v-for="key in HOLD"
        :key="key.name"
        size="is-small"
        :type="held.includes(key.name) ? 'is-info' : undefined"
        :aria-pressed="String(held.includes(key.name))"
        :aria-disabled="disabledReason ? 'true' : undefined"
        @click="disabledReason || emit('hold', key.name)">
        {{ key.label }}
      </b-button>
    </div>
    <div class="vnc-panel-row">
      <b-button
        size="is-small"
        :aria-disabled="disabledReason ? 'true' : undefined"
        @click="disabledReason || emit('release')">
        Release held keys
      </b-button>
    </div>
    <p class="vnc-panel-hint">{{ CHORD_HINT }} the console</p>
  </div>
</template>
