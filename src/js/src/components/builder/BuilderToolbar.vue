<!--
  Editor toolbar.

  Every control is a labelled button with a keyboard shortcut hint; the save
  state is announced through a polite live region so screen reader users learn
  about autosave/conflicts without polling the UI.
-->
<template>
  <div
    class="builder-toolbar builder-panel"
    role="toolbar"
    aria-label="Builder actions">
    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-undo"
        :disabled="store.readOnly || !store.canUndo"
        title="Undo (Ctrl+Z)"
        aria-keyshortcuts="Control+Z"
        @click="store.undo()">
        <builder-icon name="undo" :size="14" />
        Undo
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-redo"
        :disabled="store.readOnly || !store.canRedo"
        title="Redo (Ctrl+Shift+Z)"
        aria-keyshortcuts="Control+Shift+Z"
        @click="store.redo()">
        <builder-icon name="redo" :size="14" />
        Redo
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-copy"
        title="Copy (Ctrl+C)"
        @click="store.copy()">
        <builder-icon name="copy" :size="14" />
        Copy
      </button>
      <button
        type="button"
        class="builder-button"
        :disabled="store.readOnly"
        data-testid="toolbar-paste"
        title="Paste (Ctrl+V)"
        @click="store.paste()">
        <builder-icon name="paste" :size="14" />
        Paste
      </button>
      <button
        type="button"
        class="builder-button builder-button--danger"
        data-testid="toolbar-delete"
        title="Delete selection (Delete)"
        :disabled="store.readOnly || !hasSelection"
        @click="store.removeSelection()">
        <builder-icon name="trash" :size="14" />
        Delete
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-group"
        :disabled="store.readOnly || !store.selection.nodes.length"
        @click="store.group()">
        <builder-icon name="group" :size="14" />
        Group
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-ungroup"
        :disabled="store.readOnly || !store.selection.nodes.length"
        @click="store.ungroup()">
        <builder-icon name="ungroup" :size="14" />
        Ungroup
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-layout"
        :disabled="store.readOnly"
        title="Deterministic Dagre layout"
        @click="store.layout()">
        <builder-icon name="layout" :size="14" />
        Auto layout
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-save"
        :disabled="store.readOnly"
        @click="store.saveNow()">
        <builder-icon name="save" :size="14" />
        Save now
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-history"
        @click="$emit('history')">
        <builder-icon name="refresh" :size="14" />
        History
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-export"
        @click="$emit('export')">
        <builder-icon name="download" :size="14" />
        Export
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-import"
        @click="$emit('import')">
        <builder-icon name="upload" :size="14" />
        Import
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-scenario"
        :disabled="store.readOnly"
        @click="$emit('scenario')">
        <builder-icon name="document" :size="14" />
        Scenario
      </button>
      <button
        type="button"
        class="builder-button builder-button--primary"
        data-testid="toolbar-publish"
        :disabled="store.readOnly"
        @click="$emit('publish')">
        <builder-icon name="publish" :size="14" />
        Publish
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-theme"
        :aria-label="`Theme: ${store.theme}. Activate to change.`"
        @click="$emit('cycle-theme')">
        <builder-icon :name="themeIcon" :size="14" />
        {{ themeLabel }}
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-minimap"
        :aria-pressed="String(minimap)"
        @click="$emit('toggle-minimap')">
        <builder-icon name="image" :size="14" />
        Minimap
      </button>
    </div>

    <p
      class="builder-status"
      :class="`builder-status--${store.saveState.status}`"
      data-testid="builder-save-state"
      role="status">
      <span class="builder-status__dot" aria-hidden="true"></span>
      <builder-icon
        v-if="needsAttention"
        name="warning"
        :size="14"
        :title="store.saveStateText" />
      {{ store.saveStateText }}
    </p>

    <button
      v-if="canRetry"
      type="button"
      class="builder-button"
      data-testid="toolbar-retry"
      @click="store.retrySave()">
      <builder-icon name="refresh" :size="14" />
      Retry saving
    </button>
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  import { useBuilderStore } from '@/builder/store.js';

  defineProps({
    minimap: { type: Boolean, default: true },
  });

  defineEmits([
    'publish',
    'export',
    'import',
    'scenario',
    'history',
    'cycle-theme',
    'toggle-minimap',
  ]);

  const store = useBuilderStore();

  const hasSelection = computed(
    () => store.selection.nodes.length > 0 || store.selection.edges.length > 0,
  );

  const needsAttention = computed(() =>
    ['conflict', 'forbidden', 'error', 'offline'].includes(
      store.saveState.status,
    ),
  );

  const canRetry = computed(() =>
    ['error', 'offline'].includes(store.saveState.status),
  );

  const themeIcon = computed(
    () => ({ light: 'sun', dark: 'moon', system: 'system' })[store.theme],
  );

  const themeLabel = computed(
    () =>
      ({ light: 'Light', dark: 'Dark', system: 'System' })[store.theme] ||
      'System',
  );
</script>
