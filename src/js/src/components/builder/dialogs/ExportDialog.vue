<!--
  Export shell: builder documents (JSON/YAML) and PNG/SVG images.

  Topology YAML is deliberately absent: the server owns that conversion, so a
  client rendering could disagree with what Publish actually produces.

  Image exports always cover the whole diagram (all node bounds), not just the
  part currently visible on screen.
-->
<template>
  <builder-dialog
    title="Export diagram"
    title-id="export-dialog-title"
    @close="$emit('close')">
    <p class="builder-field">
      Diagram bounds: {{ bounds.width }} × {{ bounds.height }} px
    </p>

    <div class="builder-export__actions">
      <button
        type="button"
        class="builder-button"
        data-testid="export-json"
        @click="exportText('json')">
        <builder-icon name="download" :size="14" />
        Builder JSON
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-yaml"
        @click="exportText('yaml')">
        <builder-icon name="download" :size="14" />
        Builder YAML
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-png"
        :disabled="busy"
        @click="exportImageAs('png')">
        <builder-icon name="image" :size="14" />
        PNG
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-svg"
        :disabled="busy"
        @click="exportImageAs('svg')">
        <builder-icon name="image" :size="14" />
        SVG
      </button>
    </div>

    <p v-if="message" role="status">{{ message }}</p>
    <p v-if="error" role="alert" data-testid="export-error">{{ error }}</p>

    <div class="builder-dialog__actions">
      <button type="button" class="builder-button" @click="$emit('close')">
        Close
      </button>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed, ref } from 'vue';
  import { saveAs } from 'file-saver';
  import { toPng, toSvg } from 'html-to-image';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';

  import {
    documentBounds,
    exportFileName,
    exportImage,
    saveText,
    toJSONString,
    toYAMLString,
  } from '@/builder/exporters.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    viewportElement: { type: Function, default: () => null },
  });

  defineEmits(['close']);

  const store = useBuilderStore();
  const busy = ref(false);
  const message = ref('');
  const error = ref('');

  const bounds = computed(() => documentBounds(store.doc));

  function exportText(kind) {
    error.value = '';

    const map = {
      json: {
        text: toJSONString(store.doc),
        mime: 'application/json',
        ext: 'json',
      },
      yaml: { text: toYAMLString(store.doc), mime: 'text/yaml', ext: 'yaml' },
    }[kind];

    saveText({
      text: map.text,
      mime: map.mime,
      fileName: exportFileName(store.doc, map.ext),
      saveAs,
    });

    message.value = `Saved ${exportFileName(store.doc, map.ext)}.`;
  }

  async function exportImageAs(format) {
    error.value = '';
    message.value = '';
    busy.value = true;

    try {
      await exportImage({
        element: props.viewportElement(),
        doc: store.doc,
        format,
        toPng,
        toSvg,
        saveAs,
        backgroundColor: store.resolvedTheme === 'dark' ? '#12171f' : '#ffffff',
      });

      message.value = `Saved ${exportFileName(store.doc, format)}.`;
    } catch (err) {
      error.value = `Image export failed: ${err.message}`;
    } finally {
      busy.value = false;
    }
  }
</script>

<style scoped>
  .builder-export__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }

  .builder-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }
</style>
