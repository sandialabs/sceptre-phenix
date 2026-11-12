<!-- The View panel of the console top bar (M6a): scaling, quality and
     compression (kept for each user), View only, full screen for the pane,
     and the keyboard shortcuts of the console. Resize VM shows disabled
     with the reason when the VM does not use vga virtio. -->
<script setup>
  import { CHORD_KEYS, CHORD_START } from './chord.js';
  import { keyboardCaptureNote } from './fullscreen.js';

  defineProps({
    scaling: { type: String, required: true },
    quality: { type: Number, required: true },
    compression: { type: Number, required: true },
    viewOnly: { type: Boolean, default: false },
    // display.remoteResize of the session JSON
    remoteResize: { type: Boolean, default: false },
  });

  const emit = defineEmits([
    'update:scaling',
    'update:quality',
    'update:compression',
    'update:viewOnly',
    'fullscreen',
  ]);

  const RESIZE_REASON = 'Resize VM needs vga virtio.';
  const VIEW_ONLY_TIP =
    'Show the VM screen, but send no keyboard or mouse input to the VM';
  const SCALINGS = [
    { name: 'fit', label: 'Fit' },
    { name: 'actual', label: 'Actual' },
    { name: 'remote', label: 'Resize VM' },
  ];

  const fullscreenNote = keyboardCaptureNote();

  const level = (event) => Number(event.target.value);
</script>

<template>
  <div class="vnc-settings">
    <h2 id="vnc-scaling-title" class="vnc-panel-title">Scaling</h2>
    <div
      class="buttons has-addons vnc-panel-row"
      role="group"
      aria-labelledby="vnc-scaling-title">
      <b-button
        v-for="option in SCALINGS"
        :key="option.name"
        size="is-small"
        :type="scaling === option.name ? 'is-info' : undefined"
        :aria-pressed="String(scaling === option.name)"
        :aria-disabled="
          option.name === 'remote' && !remoteResize ? 'true' : undefined
        "
        :aria-describedby="
          option.name === 'remote' && !remoteResize
            ? 'vnc-resize-reason'
            : undefined
        "
        @click="
          (option.name !== 'remote' || remoteResize) &&
          emit('update:scaling', option.name)
        ">
        {{ option.label }}
      </b-button>
    </div>
    <p v-if="!remoteResize" id="vnc-resize-reason" class="vnc-panel-hint">
      {{ RESIZE_REASON }}
    </p>
    <label class="vnc-panel-field">
      <span>Quality · {{ quality }}</span>
      <input
        type="range"
        min="0"
        max="9"
        step="1"
        :value="quality"
        @input="emit('update:quality', level($event))" />
    </label>
    <label class="vnc-panel-field">
      <span>Compression · {{ compression }}</span>
      <input
        type="range"
        min="0"
        max="9"
        step="1"
        :value="compression"
        @input="emit('update:compression', level($event))" />
    </label>
    <div class="vnc-panel-row">
      <b-tooltip
        :label="VIEW_ONLY_TIP"
        type="is-dark"
        position="is-bottom"
        multilined>
        <b-checkbox
          :model-value="viewOnly"
          @update:model-value="emit('update:viewOnly', $event)">
          View only
        </b-checkbox>
      </b-tooltip>
      <b-tooltip
        :label="fullscreenNote ?? 'Show this pane full screen'"
        type="is-dark"
        position="is-bottom">
        <b-button
          size="is-small"
          icon-left="expand"
          @click="emit('fullscreen')">
          Full screen
        </b-button>
      </b-tooltip>
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
