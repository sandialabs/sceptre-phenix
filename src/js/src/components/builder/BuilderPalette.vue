<!--
  Add nodes: the panel of node kinds and device templates.

  Each entry is a button first (click or Enter adds the node in a free spot of
  the canvas in view) and draggable second, so pointer-free operation is never a
  second-class path. An entry shows only its name; its description appears as
  a tooltip on hover and keyboard focus and is its accessible description.
-->
<template>
  <section
    class="builder-palette builder-panel"
    aria-labelledby="palette-title">
    <div class="builder-palette__header">
      <h2 id="palette-title" class="builder-panel__title">Add nodes</h2>
      <button
        type="button"
        class="builder-help-button"
        data-testid="palette-help"
        aria-label="Add nodes help"
        aria-describedby="palette-help"
        @click="showTip($event, PALETTE_HELP)"
        @mouseenter="showTip($event, PALETTE_HELP)"
        @mouseleave="scheduleHide"
        @focus="showTip($event, PALETTE_HELP)"
        @blur="hideTip">
        <builder-icon name="help" :size="16" />
      </button>
    </div>
    <!-- Screen readers get the tooltips' text from these hidden
         descriptions; sighted users see it in the tooltip. -->
    <p id="palette-help" hidden>{{ PALETTE_HELP }}</p>

    <ul class="builder-palette__list">
      <li v-for="item in palette" :key="item.id">
        <span :id="`palette-hint-${item.id}`" hidden>{{ item.hint }}</span>
        <button
          type="button"
          class="builder-palette__item"
          :draggable="!store.readOnly"
          :disabled="store.readOnly"
          :data-testid="`palette-${item.kind}`"
          :aria-label="`Add ${item.label}`"
          :aria-describedby="`palette-hint-${item.id}`"
          @click="add(item)"
          @dragstart="onDragStart($event, item)"
          @mouseenter="showTip($event, item.hint)"
          @mouseleave="scheduleHide"
          @focus="showTip($event, item.hint)"
          @blur="hideTip">
          <builder-icon :name="item.iconKey" :size="18" />
          <span class="builder-palette__label">{{ item.label }}</span>
        </button>
      </li>
    </ul>

    <h3 class="builder-palette__subtitle">Device templates</h3>
    <ul class="builder-palette__list">
      <li v-for="template in templates" :key="template.id">
        <span :id="`palette-hint-template-${template.id}`" hidden>
          {{ template.description }}
        </span>
        <button
          type="button"
          class="builder-palette__item"
          :draggable="!store.readOnly"
          :disabled="store.readOnly"
          :data-testid="`palette-template-${template.id}`"
          :aria-label="`Add ${deviceName(template.label)}`"
          :aria-describedby="`palette-hint-template-${template.id}`"
          @click="addTemplate(template)"
          @dragstart="onDragStart($event, { kind: 'device', template })"
          @mouseenter="showTip($event, template.description)"
          @mouseleave="scheduleHide"
          @focus="showTip($event, template.description)"
          @blur="hideTip">
          <builder-icon :name="template.iconKey" :size="18" />
          <span class="builder-palette__label">{{ template.label }}</span>
        </button>
      </li>
    </ul>

    <!-- The text already reaches screen readers as each button's
         description. -->
    <div
      v-if="tip"
      ref="tipEl"
      class="builder-tooltip builder-tooltip--fixed"
      data-testid="palette-tooltip"
      aria-hidden="true"
      :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
      {{ tip.text }}
    </div>
  </section>
</template>

<script setup>
  import { inject } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import { DEVICE_TEMPLATES, PALETTE } from '@/builder/catalog.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    PALETTE_MIME,
    PALETTE_TEMPLATE_MIME,
    addInView,
    paletteNode,
  } from './paletteDnd.js';

  const store = useBuilderStore();
  // The view's command context (BuilderBeta.vue): where the canvas is
  // looking, so a node is added in view.
  const commands = inject('builderCommands', null);
  const palette = PALETTE;
  const templates = DEVICE_TEMPLATES;
  const PALETTE_HELP =
    'Select an item to add it to the diagram, or drag it onto the canvas.';

  // "Router device", but "External device" rather than "External device
  // device".
  function deviceName(label) {
    return /\bdevice$/i.test(label) ? label : `${label} device`;
  }

  // Palette descriptions, and the palette's own instructions on its help
  // button, are tooltips (WCAG 1.4.13): shown on hover and focus, kept while
  // the pointer moves onto them, and dismissed with Escape.
  const { tip, tipEl, showTip, scheduleHide, hideTip } = useFixedTooltip();

  // In a free spot of the part of the canvas in view, as the command
  // palette's Add commands do.
  function add(item) {
    addInView(commands || { store }, { kind: item.kind });
  }

  function addTemplate(template) {
    addInView(commands || { store }, paletteNode('device', template.id));
  }

  // A template goes by its id, so the canvas adds the node a click adds.
  function onDragStart(event, item) {
    event.dataTransfer?.setData(PALETTE_MIME, item.kind);

    if (item.template) {
      event.dataTransfer?.setData(PALETTE_TEMPLATE_MIME, item.template.id);
    }

    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'copy';
    }
  }
</script>

<style scoped>
  .builder-panel__title,
  .builder-palette__subtitle {
    font-weight: 700;
    font-size: 0.9rem;
    margin: 0 0 0.35rem;
  }

  .builder-palette__header {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    margin-bottom: 0.35rem;
  }

  .builder-palette__header .builder-panel__title {
    margin: 0;
  }

  .builder-palette__subtitle {
    margin-top: 0.75rem;
  }

  .builder-palette__list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
</style>
