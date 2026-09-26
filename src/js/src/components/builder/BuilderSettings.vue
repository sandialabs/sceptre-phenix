<!--
  Builder settings: how this viewer likes the editor, whatever the diagram.
  Every change applies at once and is kept in this browser, through logout:
  the theme by theme.js (as the toolbar's toggle), the single-key switch by
  keymap.js (as the shortcut sheet's), the rest by builder/settings.js.
  Reset to defaults puts all of them back; custom shortcut keys have their
  own Reset all in the sheet.

  Change keyboard shortcuts opens the sheet's customization over this
  dialog, and closing it comes back here. The Builder's live region waits
  while a modal dialog is open, so this dialog speaks through a status
  region of its own.
-->
<template>
  <builder-dialog
    title="Builder settings"
    title-id="settings-dialog-title"
    class="builder-settings"
    data-testid="settings-dialog"
    @close="$emit('close')">
    <p class="builder-hint builder-settings__intro">
      Changes apply at once. They are kept in this browser only, and stay after
      you log out.
    </p>

    <h3 class="builder-settings__heading">Appearance</h3>
    <fieldset class="builder-field builder-settings__choices">
      <legend>Theme</legend>
      <div class="builder-settings__options">
        <label
          v-for="option in THEMES"
          :key="option.value"
          class="builder-choice">
          <input
            type="radio"
            name="settings-theme"
            :value="option.value"
            :checked="store.theme === option.value"
            :aria-describedby="
              option.value === 'system' ? 'settings-theme-hint' : undefined
            "
            :data-testid="`settings-theme-${option.value}`"
            @change="$emit('theme', option.value)" />
          {{ option.label }}
        </label>
      </div>
      <p id="settings-theme-hint" class="builder-hint">
        System follows your device’s light or dark appearance.
      </p>
    </fieldset>
    <div class="builder-field">
      <builder-switch
        :model-value="builderSettings.reduceMotion"
        aria-describedby="settings-motion-hint"
        data-testid="settings-reduce-motion"
        @update:model-value="setSetting('reduceMotion', $event)">
        Reduce motion
      </builder-switch>
      <p id="settings-motion-hint" class="builder-hint">
        The canvas pans and zooms at once, and nothing animates.
        {{
          systemReducesMotion
            ? 'Your device asks for reduced motion, so motion is reduced either way.'
            : 'Motion is also reduced whenever your device asks for it.'
        }}
      </p>
    </div>

    <h3 class="builder-settings__heading">Canvas</h3>
    <div class="builder-field">
      <label for="settings-layout">Auto layout algorithm</label>
      <select
        id="settings-layout"
        :value="builderSettings.layoutAlgorithm"
        aria-describedby="settings-layout-hint"
        data-testid="settings-layout"
        @change="setSetting('layoutAlgorithm', $event.target.value)">
        <option
          v-for="algorithm in LAYOUT_ALGORITHMS"
          :key="algorithm.id"
          :value="algorithm.id">
          {{ algorithm.label }}
        </option>
      </select>
      <p id="settings-layout-hint" class="builder-hint">
        {{ layoutAlgorithm(builderSettings.layoutAlgorithm)?.description }}
        Auto layout arranges the diagram with it.
      </p>
    </div>
    <div class="builder-field">
      <builder-switch
        :model-value="builderSettings.showMinimap"
        aria-describedby="settings-minimap-hint"
        data-testid="settings-minimap"
        @update:model-value="setSetting('showMinimap', $event)">
        Show the minimap
      </builder-switch>
      <p id="settings-minimap-hint" class="builder-hint">
        The toolbar’s Minimap button shows or hides it until the next diagram
        opens.
      </p>
    </div>
    <fieldset class="builder-field builder-settings__choices">
      <legend>Zoom when a diagram opens</legend>
      <div class="builder-settings__options">
        <label
          v-for="option in ZOOMS"
          :key="option.value"
          class="builder-choice">
          <input
            type="radio"
            name="settings-zoom"
            :value="option.value"
            :checked="builderSettings.openZoom === option.value"
            aria-describedby="settings-zoom-hint"
            :data-testid="`settings-zoom-${option.value}`"
            @change="setSetting('openZoom', option.value)" />
          {{ option.label }}
        </label>
      </div>
      <p id="settings-zoom-hint" class="builder-hint">
        Reset view goes back to it too.
      </p>
    </fieldset>

    <h3 class="builder-settings__heading">Keyboard</h3>
    <div class="builder-field">
      <builder-switch
        :model-value="keymapState.singleKeys"
        aria-describedby="settings-single-hint"
        data-testid="settings-single-keys"
        @update:model-value="setSingleKeyShortcuts">
        Single-key shortcuts
      </builder-switch>
      <p id="settings-single-hint" class="builder-hint">
        {{ singleKeyHint() }}
      </p>
    </div>
    <div class="builder-field">
      <button
        type="button"
        class="builder-button"
        aria-haspopup="dialog"
        aria-describedby="settings-keys-hint"
        data-testid="settings-change-shortcuts"
        @click="sheetOpen = true">
        <builder-icon name="keyboard" :size="14" />
        Change keyboard shortcuts
      </button>
      <p id="settings-keys-hint" class="builder-hint">
        Choose other keys for any command, or none.
      </p>
    </div>

    <div class="builder-dialog__actions builder-settings__actions">
      <button
        type="button"
        class="builder-button"
        :aria-disabled="atDefaults ? 'true' : undefined"
        data-testid="settings-reset"
        @click="resetAll">
        Reset to defaults
      </button>
      <button
        type="button"
        class="builder-button builder-button--primary"
        data-testid="settings-done"
        @click="$emit('close')">
        Done
      </button>
    </div>

    <p
      class="builder-visually-hidden"
      role="status"
      data-testid="settings-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>

  <!-- Over this dialog, which it returns focus to when it closes. -->
  <builder-shortcuts
    v-if="sheetOpen"
    customize
    data-testid="shortcuts-dialog"
    @close="sheetOpen = false" />
</template>

<script setup>
  import { computed, ref } from 'vue';

  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderShortcuts from './BuilderShortcuts.vue';
  import BuilderSwitch from './BuilderSwitch.vue';
  import { useMessage } from './dialogs/message.js';

  import { keymapState, setSingleKeyShortcuts } from '@/builder/keymap.js';
  import {
    LAYOUT_ALGORITHMS,
    layoutAlgorithm,
  } from '@/builder/layouts/index.js';
  import {
    builderSettings,
    resetSettings,
    setSetting,
    settingsAtDefaults,
  } from '@/builder/settings.js';
  import { singleKeyHint } from '@/builder/shortcuts.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { DEFAULT_THEME, prefersReducedMotion } from '@/builder/theme.js';

  // `theme` asks the view to apply a theme to the Builder, as the toolbar's
  // toggle does, but without announcing it.
  const emit = defineEmits(['close', 'theme']);

  const THEMES = [
    { value: 'system', label: 'System' },
    { value: 'light', label: 'Light' },
    { value: 'dark', label: 'Dark' },
  ];

  const ZOOMS = [
    { value: 'actual', label: '100%' },
    { value: 'fit', label: 'Fit the whole diagram in view' },
  ];

  const store = useBuilderStore();
  const status = useMessage();
  const sheetOpen = ref(false);

  const systemReducesMotion = prefersReducedMotion(
    typeof window !== 'undefined' && window.matchMedia
      ? window.matchMedia.bind(window)
      : undefined,
  );

  const atDefaults = computed(
    () =>
      settingsAtDefaults() &&
      store.theme === DEFAULT_THEME &&
      keymapState.singleKeys,
  );

  // Focus stays on the button, which no longer applies.
  function resetAll() {
    if (atDefaults.value) {
      status.set('Every setting is at its default already.');
      return;
    }

    resetSettings();
    setSingleKeyShortcuts(true);
    if (store.theme !== DEFAULT_THEME) {
      emit('theme', DEFAULT_THEME);
    }

    status.set(
      Object.keys(keymapState.overrides).length
        ? 'Every setting is back to its default. Shortcut keys you changed are kept: reset them under Change keyboard shortcuts.'
        : 'Every setting is back to its default.',
    );
  }
</script>

<style scoped>
  .builder-settings__intro {
    margin: 0 0 0.25rem;
  }

  .builder-settings__heading {
    margin: 1rem 0 0.4rem;
    padding-top: 0.6rem;
    border-top: 1px solid var(--bx-border);
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--bx-text-muted);
  }

  .builder-settings__choices {
    border: 0;
    padding: 0;
  }

  .builder-settings__choices legend {
    padding: 0;
    font-size: 0.85rem;
    font-weight: 600;
  }

  /* A few short choices, side by side while they fit. */
  .builder-settings__options {
    display: flex;
    flex-wrap: wrap;
    column-gap: 1.25rem;
  }

  .builder-settings .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-settings__actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 1rem;
  }
</style>
