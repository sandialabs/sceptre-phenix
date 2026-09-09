<!--
  Palette.

  Each entry is a button first (click or Enter adds the node at a deterministic
  position) and draggable second, so pointer-free operation is never a
  second-class path.
-->
<template>
  <section
    class="builder-palette builder-panel"
    aria-labelledby="palette-title">
    <h2 id="palette-title" class="builder-panel__title">Palette</h2>
    <p class="builder-palette__hint">
      Select an item to add it to the diagram, or drag it onto the canvas.
    </p>

    <ul class="builder-palette__list">
      <li v-for="item in palette" :key="item.id">
        <button
          type="button"
          class="builder-palette__item"
          :draggable="!store.readOnly"
          :disabled="store.readOnly"
          :data-testid="`palette-${item.kind}`"
          :aria-label="`Add ${item.label}. ${item.hint}`"
          @click="add(item)"
          @dragstart="onDragStart($event, item)">
          <builder-icon :name="item.iconKey" :size="18" />
          <span>
            <strong>{{ item.label }}</strong>
            <span class="builder-palette__hint">{{ item.hint }}</span>
          </span>
        </button>
      </li>
    </ul>

    <h3 class="builder-palette__subtitle">Device templates</h3>
    <ul class="builder-palette__list">
      <li v-for="template in templates" :key="template.id">
        <button
          type="button"
          class="builder-palette__item"
          :draggable="!store.readOnly"
          :disabled="store.readOnly"
          :data-testid="`palette-template-${template.id}`"
          :aria-label="`Add ${template.label} device. ${template.description}`"
          @click="addTemplate(template)"
          @dragstart="onDragStart($event, { kind: 'device' })">
          <builder-icon :name="template.iconKey" :size="18" />
          <span>
            <strong>{{ template.label }}</strong>
            <span class="builder-palette__hint">{{
              template.description
            }}</span>
          </span>
        </button>
      </li>
    </ul>
  </section>
</template>

<script setup>
  import BuilderIcon from './BuilderIcon.vue';

  import { DEVICE_TEMPLATES, PALETTE } from '@/builder/catalog.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { PALETTE_MIME } from './paletteDnd.js';

  const store = useBuilderStore();
  const palette = PALETTE;
  const templates = DEVICE_TEMPLATES;

  /**
   * Deterministic placement for keyboard/click adds: a simple grid walk so
   * nodes never land on top of each other and tests can assert positions.
   *
   * @returns {{x: number, y: number}}
   */
  function nextPosition() {
    const count = store.doc.nodes.length;

    return { x: 80 + (count % 5) * 220, y: 80 + Math.floor(count / 5) * 160 };
  }

  function add(item) {
    store.addNode({ kind: item.kind, position: nextPosition() });
  }

  function addTemplate(template) {
    store.addNode({
      kind: 'device',
      template,
      hostname: template.id,
      iconKey: template.iconKey,
      position: nextPosition(),
    });
  }

  function onDragStart(event, item) {
    event.dataTransfer?.setData(PALETTE_MIME, item.kind);

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

  .builder-palette__subtitle {
    margin-top: 0.75rem;
  }

  .builder-palette__list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
</style>
