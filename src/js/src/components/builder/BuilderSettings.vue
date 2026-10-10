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
      <label for="settings-layout">Default layout</label>
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
        Auto layout and Auto-group use it on a draft whose layout menu says
        Default, and the draft then keeps it, as it keeps a layout you choose in
        that menu. It does not move anything by itself.
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
    <div class="builder-field">
      <builder-switch
        :model-value="builderSettings.showNodeNotes"
        aria-describedby="settings-node-notes-hint"
        data-testid="settings-node-notes"
        @update:model-value="setSetting('showNodeNotes', $event)">
        Show node notes
      </builder-switch>
      <p id="settings-node-notes-hint" class="builder-hint">
        The notes of devices and switches, below each node on the canvas and in
        PNG and SVG downloads. Auto layout leaves room for them while they show.
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
        <!-- Custom, with its percentage beside it: a field that is always
             enabled, and that chooses Custom when it is given a number. -->
        <div class="builder-settings__custom">
          <label class="builder-choice">
            <input
              type="radio"
              name="settings-zoom"
              value="custom"
              :checked="builderSettings.openZoom === 'custom'"
              aria-describedby="settings-zoom-hint"
              data-testid="settings-zoom-custom"
              @change="chooseCustomZoom" />
            Custom
          </label>
          <input
            id="settings-zoom-percent"
            ref="percentField"
            v-model="percentText"
            type="number"
            class="builder-settings__percent"
            :min="OPEN_ZOOM_PERCENT.min"
            :max="OPEN_ZOOM_PERCENT.max"
            :step="OPEN_ZOOM_PERCENT.step"
            inputmode="numeric"
            aria-label="Custom zoom, percent"
            :aria-invalid="percentError.text ? 'true' : undefined"
            :aria-describedby="
              percentError.text
                ? 'settings-zoom-percent-hint settings-zoom-hint settings-zoom-percent-error'
                : 'settings-zoom-percent-hint settings-zoom-hint'
            "
            data-testid="settings-zoom-percent"
            @change="onZoomPercent" />
          <span aria-hidden="true">%</span>
          <p id="settings-zoom-percent-hint" class="builder-hint">
            {{ PERCENT_RANGE }}
          </p>
        </div>
      </div>
      <p
        id="settings-zoom-percent-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span
          v-if="percentError.text"
          :key="percentError.key"
          data-testid="settings-zoom-percent-error"
          >{{ percentError.text }}</span
        >
      </p>
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
  import { computed, ref, watch } from 'vue';

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
    OPEN_ZOOM_PERCENT,
    builderSettings,
    resetSettings,
    setSetting,
    settingsAtDefaults,
  } from '@/builder/settings.js';
  import { singleKeyHint } from '@/builder/shortcuts.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    DEFAULT_THEME,
    pageMatchMedia,
    prefersReducedMotion,
  } from '@/builder/theme.js';

  // `theme` asks the view to apply a theme to the Builder, as the toolbar's
  // toggle does, but without announcing it.
  const emit = defineEmits(['close', 'theme']);

  const THEMES = [
    { value: 'system', label: 'System' },
    { value: 'light', label: 'Light' },
    { value: 'dark', label: 'Dark' },
  ];

  // Custom, the third choice, has a field beside it and is drawn apart.
  const ZOOMS = [
    { value: 'actual', label: '100%' },
    { value: 'fit', label: 'Fit the whole diagram in view' },
  ];

  const PERCENT_RANGE = `From ${OPEN_ZOOM_PERCENT.min} to ${OPEN_ZOOM_PERCENT.max}.`;

  const store = useBuilderStore();
  const status = useMessage();
  const sheetOpen = ref(false);

  // The custom zoom's field holds what the user types until it is a
  // percentage the setting takes; the message says when it is not.
  const percentField = ref(null);
  const percentText = ref(builderSettings.openZoomPercent);
  const percentError = useMessage();

  // The kept percentage back in the field, which drops what was typed and
  // refused. The field is set as well as its model: the model holds the
  // number typed, which can equal the kept one while the text differs.
  function showKeptPercent() {
    percentError.clear();
    percentText.value = builderSettings.openZoomPercent;

    if (percentField.value) {
      percentField.value.value = String(builderSettings.openZoomPercent);
    }
  }

  // A number in the range is rounded to the field's step, kept, and chosen:
  // Custom becomes the zoom diagrams open with. Anything else keeps nothing.
  function onZoomPercent(event) {
    const typed = event.target.value.trim();
    const number = typed === '' ? NaN : Number(typed);

    if (
      !Number.isFinite(number) ||
      number < OPEN_ZOOM_PERCENT.min ||
      number > OPEN_ZOOM_PERCENT.max
    ) {
      percentError.set(
        `Enter a number from ${OPEN_ZOOM_PERCENT.min} to ${OPEN_ZOOM_PERCENT.max}.`,
      );

      return;
    }

    const percent =
      Math.round(number / OPEN_ZOOM_PERCENT.step) * OPEN_ZOOM_PERCENT.step;

    setSetting('openZoomPercent', percent);
    setSetting('openZoom', 'custom');
    showKeptPercent();
    status.set(`Diagrams open at ${percent}%.`);
  }

  // Custom on its own takes the percentage the field shows: the kept one,
  // which replaces a value the field refused.
  function chooseCustomZoom() {
    setSetting('openZoom', 'custom');
    showKeptPercent();
  }

  // Reset to defaults, or another tab, changed the percentage.
  watch(() => builderSettings.openZoomPercent, showKeptPercent);

  const systemReducesMotion = prefersReducedMotion(pageMatchMedia());

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
    showKeptPercent();
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

  /* A few short choices, side by side while they fit, in line along their
     tops: the Custom zoom is taller, with a hint under its field. */
  .builder-settings__options {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    column-gap: 1.25rem;
  }

  /* The Custom choice with its field and the field's hint under it; they
     wrap to the next line together. */
  .builder-settings__custom {
    display: inline-grid;
    grid-template-columns: auto auto auto;
    align-items: center;
    column-gap: 0.4rem;
  }

  /* As tall as a choice's row, so Custom stays in line with the others. */
  .builder-settings .builder-settings__custom input.builder-settings__percent {
    width: 5rem;
    height: 1.75rem;
    padding-top: 0;
    padding-bottom: 0;
  }

  .builder-settings .builder-settings__custom .builder-hint {
    grid-column: 2 / -1;
    margin: 0;
  }

  .builder-settings .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-settings__actions {
    margin-top: 1rem;
  }
</style>
