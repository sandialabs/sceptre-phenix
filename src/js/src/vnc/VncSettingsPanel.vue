<!-- The View panel of the console top bar (M6a): scaling, the zoom of
     Actual, quality and compression, and whether the panes show their
     headers and banners (kept for each user); View only, full screen and
     Open alone in a new tab for the pane. Resize VM shows disabled with the
     reason when the VM does not use vga virtio. -->
<script setup>
  import { VNC_ZOOMS } from '@/utils/prefs.js';
  import { keyboardCaptureNote } from './fullscreen.js';

  defineProps({
    scaling: { type: String, required: true },
    // the zoom of Actual scaling, one of VNC_ZOOMS
    zoom: { type: Number, default: 1 },
    quality: { type: Number, required: true },
    compression: { type: Number, required: true },
    viewOnly: { type: Boolean, default: false },
    // display.remoteResize of the session JSON
    remoteResize: { type: Boolean, default: false },
    paneHeaders: { type: Boolean, default: true },
    banners: { type: Boolean, default: true },
  });

  const emit = defineEmits([
    'update:scaling',
    'update:zoom',
    'update:quality',
    'update:compression',
    'update:viewOnly',
    'update:paneHeaders',
    'update:banners',
    'fullscreen',
    'open-alone',
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

  const percent = (zoom) => `${zoom * 100}%`;

  // a zoom level applies to Actual, so it also selects Actual
  function pickZoom(zoom) {
    emit('update:zoom', zoom);
    emit('update:scaling', 'actual');
  }

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
    <h2 id="vnc-zoom-title" class="vnc-panel-title">Zoom</h2>
    <div
      class="buttons has-addons vnc-panel-row"
      role="group"
      aria-labelledby="vnc-zoom-title"
      aria-describedby="vnc-zoom-hint">
      <b-button
        v-for="level in VNC_ZOOMS"
        :key="level"
        size="is-small"
        :type="scaling === 'actual' && zoom === level ? 'is-info' : undefined"
        :aria-pressed="String(scaling === 'actual' && zoom === level)"
        @click="pickZoom(level)">
        {{ percent(level) }}
      </b-button>
    </div>
    <p id="vnc-zoom-hint" class="vnc-panel-hint">
      A zoom level selects Actual.
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
      <b-tooltip
        label="Open the VM of this pane alone in a new tab"
        type="is-dark"
        position="is-bottom">
        <b-button
          size="is-small"
          icon-left="up-right-from-square"
          class="vnc-open-alone"
          @click="emit('open-alone')">
          Open alone in a new tab
        </b-button>
      </b-tooltip>
    </div>
    <h2 id="vnc-show-title" class="vnc-panel-title">Show</h2>
    <div class="vnc-panel-row" role="group" aria-labelledby="vnc-show-title">
      <b-tooltip
        label="The header of each pane in split view: the VM name, its state, the pane number and the pane menu"
        type="is-dark"
        position="is-bottom"
        multilined>
        <b-checkbox
          :model-value="paneHeaders"
          @update:model-value="emit('update:paneHeaders', $event)">
          Pane headers
        </b-checkbox>
      </b-tooltip>
      <b-tooltip
        label="The top and bottom banners of each VM"
        type="is-dark"
        position="is-bottom"
        multilined>
        <b-checkbox
          :model-value="banners"
          @update:model-value="emit('update:banners', $event)">
          Banners
        </b-checkbox>
      </b-tooltip>
    </div>
  </div>
</template>
