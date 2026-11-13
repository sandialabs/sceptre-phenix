<!-- The Keys panel of the console top bar (M6a): keys that the browser
     keeps for itself, Hold buttons for sticky modifier keys, "Release held
     keys", and the keyboard shortcuts of the console. -->
<script setup>
  import { CHORD_KEYS, CHORD_START } from './chord.js';
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
    <h2 id="vnc-shortcuts-title" class="vnc-panel-title">Keyboard shortcuts</h2>
    <p class="vnc-panel-hint">Press {{ CHORD_START }}, then:</p>
    <dl class="vnc-shortcuts" aria-labelledby="vnc-shortcuts-title">
      <template v-for="item in CHORD_KEYS" :key="item.key">
        <dt>
          <kbd>{{ item.key }}</kbd>
        </dt>
        <dd>{{ item.does }}</dd>
      </template>
    </dl>
  </div>
</template>

<style scoped>
  .vnc-shortcuts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.2rem 0.6rem;
    margin: 0;
    font-size: 0.8rem;
  }

  .vnc-shortcuts dd {
    margin: 0;
  }

  .vnc-shortcuts kbd {
    padding: 0 0.3rem;
    color: #fff;
    background: #1a1d22;
    border: 1px solid #6b7280;
    border-radius: 3px;
  }
</style>
