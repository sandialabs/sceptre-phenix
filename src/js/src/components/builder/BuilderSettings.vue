<!-- Builder settings: this viewer's editor preferences, kept in this browser
     (see settings.js, theme.js and keymap.js). It speaks through its own status
     region, because the Builder's live region waits while a modal is open. -->
<template>
  <builder-dialog
    title="Builder settings"
    title-id="settings-dialog-title"
    class="builder-settings"
    data-testid="settings-dialog"
    @close="$emit('close')">
    <!-- The settings scroll, and Reset to defaults and Done stay in view.
         tabindex: Firefox makes a box that scrolls a Tab stop of its own.
         This box holds fields, which Tab reaches and scrolls into view. -->
    <div class="builder-settings__body" tabindex="-1">
      <p class="builder-hint builder-settings__intro">
        Changes apply at once. This browser keeps the settings, also after you
        log out.
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
          Default. The draft then keeps it, as it keeps a layout you choose in
          that menu.
        </p>
      </div>
      <div class="builder-field">
        <builder-switch
          :model-value="builderSettings.showMinimap"
          aria-describedby="settings-minimap-hint"
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
          :model-value="builderSettings.showNodeIcons"
          aria-describedby="settings-node-icons-hint"
          data-testid="settings-node-icons"
          @update:model-value="setSetting('showNodeIcons', $event)">
          Show node icons
        </builder-switch>
        <p id="settings-node-icons-hint" class="builder-hint">
          The icons of devices, switches and groups, on the canvas and in PNG,
          SVG and draw.io downloads. The diagram keeps its icons.
        </p>
      </div>
      <div class="builder-field">
        <builder-switch
          :model-value="builderSettings.showNodeDescriptions"
          aria-describedby="settings-node-descriptions-hint"
          data-testid="settings-node-descriptions"
          @update:model-value="setSetting('showNodeDescriptions', $event)">
          Show node descriptions
        </builder-switch>
        <p id="settings-node-descriptions-hint" class="builder-hint">
          The descriptions of devices and groups, on the canvas and in PNG, SVG
          and draw.io downloads. A node can also hide its own in the Inspector.
          The diagram keeps its descriptions.
        </p>
      </div>
      <div class="builder-field">
        <builder-switch
          :model-value="builderSettings.showConnections"
          aria-describedby="settings-connections-hint"
          data-testid="settings-connections"
          @update:model-value="setSetting('showConnections', $event)">
          Show connections
        </builder-switch>
        <p id="settings-connections-hint" class="builder-hint">
          The connections between nodes, on the canvas. Hidden connections
          cannot be selected. The diagram, its checks and PNG, SVG and draw.io
          downloads keep them.
        </p>
      </div>
      <fieldset class="builder-field builder-settings__choices">
        <legend>Node icon size</legend>
        <div class="builder-settings__options">
          <label
            v-for="option in ICON_SIZE_CHOICES"
            :key="option.value"
            class="builder-choice">
            <input
              type="radio"
              name="settings-icon-size"
              :value="option.value"
              :checked="builderSettings.nodeIconSize === option.value"
              aria-describedby="settings-icon-size-hint"
              :data-testid="`settings-icon-size-${option.value}`"
              @change="setSetting('nodeIconSize', option.value)" />
            {{ option.label }}
          </label>
        </div>
        <p id="settings-icon-size-hint" class="builder-hint">
          Off uses the sizes of the diagram. Another size replaces them on your
          canvas and in PNG, SVG and draw.io downloads. The diagram does not
          change.
        </p>
      </fieldset>
      <div class="builder-field">
        <builder-switch
          :model-value="builderSettings.showCoordinates"
          aria-describedby="settings-coordinates-hint"
          data-testid="settings-coordinates"
          @update:model-value="setSetting('showCoordinates', $event)">
          Show coordinates
        </builder-switch>
        <p id="settings-coordinates-hint" class="builder-hint">
          Shows X and Y along the edges of the canvas. Use them with Position in
          the Inspector.
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
                  ? 'settings-zoom-percent-hint settings-zoom-percent-error'
                  : 'settings-zoom-percent-hint'
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
      </fieldset>

      <h3 class="builder-settings__heading">Editing</h3>
      <div class="builder-field">
        <builder-switch
          :model-value="builderSettings.snapToGrid"
          aria-describedby="settings-snap-hint"
          data-testid="settings-snap-to-grid"
          @update:model-value="setSetting('snapToGrid', $event)">
          Snap to grid
        </builder-switch>
        <p id="settings-snap-hint" class="builder-hint">
          Nodes that you drag or drop, new nodes and the points of lines go to
          the nearest point of the diagram’s grid. When this is off, they go
          where you put them. Each diagram has its grid size, in the Inspector.
        </p>
      </div>
      <div class="builder-field">
        <builder-switch
          :model-value="builderSettings.assignAddressesOnConnect"
          aria-describedby="settings-assign-addresses-hint"
          data-testid="settings-assign-addresses"
          @update:model-value="setSetting('assignAddressesOnConnect', $event)">
          Assign addresses on connect
        </builder-switch>
        <p id="settings-assign-addresses-hint" class="builder-hint">
          A static interface with no address gets the next free address of the
          network’s subnet when you connect, paste or duplicate it. When this is
          off, the interface stays without an address. Assign addresses in the
          Inspector works either way.
        </p>
      </div>

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

      <h3 class="builder-settings__heading">Layers</h3>
      <div class="builder-field">
        <button
          type="button"
          class="builder-button"
          :aria-haspopup="diagram ? 'dialog' : undefined"
          aria-describedby="settings-layers-hint"
          data-testid="settings-edit-layers"
          @click="$emit('layers')">
          <builder-icon name="layer-group" :size="14" />
          Edit layers…
        </button>
        <p id="settings-layers-hint" class="builder-hint">
          {{
            diagram
              ? 'The layers of a diagram are not a setting. The diagram keeps them, and the layers editor changes them.'
              : 'The layer sets of the server are on the Layers tab. There you change them and choose the default set of the server.'
          }}
        </p>
      </div>

      <h3 class="builder-settings__heading">Storage</h3>
      <div class="builder-field">
        <builder-busy-button
          :busy="cleanup.busy !== ''"
          :busy-label="cleanup.busy"
          aria-haspopup="dialog"
          aria-describedby="settings-cleanup-hint"
          data-testid="settings-cleanup"
          @click="checkCleanup">
          <builder-icon name="trash" :size="14" />
          Clean up…
        </builder-busy-button>
        <p id="settings-cleanup-hint" class="builder-hint">
          Deletes the drafts of all users that are published and unchanged, and
          the data the server stores that nothing uses. Published topologies
          stay. The server holds at most {{ MAX_DRAFTS }} drafts.
        </p>
      </div>
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

  <!-- Over this dialog too. Focus returns to Clean up… when it closes. -->
  <builder-confirm
    v-if="cleanup.plan"
    id="settings-cleanup-confirm"
    title="Clean up storage?"
    :message="cleanupQuestion(cleanup.plan)"
    confirm-label="Clean up"
    @confirm="runCleanup"
    @cancel="cleanup.plan = null" />
</template>

<script setup>
  import { computed, reactive, ref, watch } from 'vue';

  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderShortcuts from './BuilderShortcuts.vue';
  import BuilderSwitch from './BuilderSwitch.vue';
  import { useMessage } from './dialogs/message.js';

  import { count } from '@/builder/announce.js';
  import { keymapState, setSingleKeyShortcuts } from '@/builder/keymap.js';
  import {
    LAYOUT_ALGORITHMS,
    layoutAlgorithm,
  } from '@/builder/layouts/index.js';
  import { MAX_DRAFTS } from '@/builder/limits.js';
  import { ICON_SIZES } from '@/builder/model.js';
  import { ICON_SIZE_TITLES } from '@/builder/schema.js';
  import {
    ICON_SIZE_OFF,
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
  // `layers` asks the view for the layers editor, or the drafts page's Layers tab.
  const emit = defineEmits(['close', 'theme', 'layers']);

  defineProps({
    // A diagram is open.
    diagram: { type: Boolean, default: false },
  });

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

  // Off, then the sizes, as the Inspector's Icon size names them.
  const ICON_SIZE_CHOICES = [
    { value: ICON_SIZE_OFF, label: 'Off' },
    ...ICON_SIZES.map((size) => ({
      value: size,
      label: ICON_SIZE_TITLES[size],
    })),
  ];

  const PERCENT_RANGE = `From ${OPEN_ZOOM_PERCENT.min} to ${OPEN_ZOOM_PERCENT.max}.`;

  const store = useBuilderStore();
  const status = useMessage();
  const sheetOpen = ref(false);

  // The custom zoom's field holds what the user types until it is a
  // percentage the setting takes. The message says when it is not.
  const percentField = ref(null);
  const percentText = ref(builderSettings.openZoomPercent);
  const percentError = useMessage();

  // Puts the kept percentage back in the field, which drops the refused
  // text. This sets the field as well as its model: the model holds the
  // number typed, which can equal the kept one while the text differs.
  function showKeptPercent() {
    percentError.clear();
    percentText.value = builderSettings.openZoomPercent;

    if (percentField.value) {
      percentField.value.value = String(builderSettings.openZoomPercent);
    }
  }

  // Rounds a number in the range to the field's step, keeps it, and chooses
  // it: Custom becomes the zoom diagrams open with. Anything else keeps
  // nothing.
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
        ? 'Every setting is back to its default. The Builder keeps the shortcut keys that you changed. To reset them, use Change keyboard shortcuts.'
        : 'Every setting is back to its default.',
    );
  }

  // Clean up…: busy is what the button's spinner says ('' when idle), and
  // plan is what a dry run found, which the confirmation asks about.
  const cleanup = reactive({ busy: '', plan: null });

  // The unused stored data: published documents no topology references and
  // stored content nothing names. The user sees them as one kind.
  function unusedItems(plan) {
    return plan.documents + plan.content;
  }

  // "12 published, unchanged drafts and 3 unused stored items".
  function cleanupParts(plan, drafts) {
    return [
      plan.drafts.length > 0 && count(plan.drafts.length, drafts),
      unusedItems(plan) > 0 && count(unusedItems(plan), 'unused stored item'),
    ]
      .filter(Boolean)
      .join(' and ');
  }

  // The drafts' published topologies stay, which the question says when it
  // names drafts.
  function cleanupQuestion(plan) {
    const drafts = plan.drafts.length > 0;
    const open = plan.drafts.includes(store.draftId)
      ? ' The open draft is one of them.'
      : '';
    const kept = drafts
      ? ' They can be drafts of any user. Their published topologies stay.'
      : '';

    return `Delete ${cleanupParts(plan, 'published, unchanged draft')}?${open}${kept}`;
  }

  // Asks the server what a cleanup would delete. When there is something,
  // the confirmation asks before anything is deleted.
  async function checkCleanup() {
    if (cleanup.busy) {
      return;
    }

    cleanup.busy = 'Checking storage';

    try {
      const plan = await store.planStorageCleanup();

      if (plan.drafts.length === 0 && unusedItems(plan) === 0) {
        status.set('There is nothing to clean up.');
      } else {
        cleanup.plan = plan;
      }
    } catch (error) {
      status.set(store.describeError(error, 'check the storage'));
    } finally {
      cleanup.busy = '';
    }
  }

  // Why nothing was cleaned up: drafts it would delete have changes that
  // only this device holds (see cleanUpStorage in the store).
  function unsentText(n) {
    const drafts = n === 1 ? 'a draft' : `${n} drafts`;
    const have = n === 1 ? 'has' : 'have';

    return `Clean up deleted nothing, because ${drafts} that it would delete ${have} changes on this device that are not saved yet. Wait for the saves to finish, and try again.`;
  }

  async function runCleanup() {
    cleanup.plan = null;
    cleanup.busy = 'Cleaning up storage';

    try {
      const result = await store.cleanUpStorage();

      if (!result) {
        status.set(
          'Clean up deleted nothing, because your latest changes are not saved yet. Wait for the save to finish, or select Retry saving.',
        );

        return;
      }

      if (result.unsent) {
        status.set(unsentText(result.unsent));

        return;
      }

      const parts = cleanupParts(result, 'draft');
      const open = result.open ? ' The open draft was one of them.' : '';
      const failed = result.incomplete
        ? ' Clean up could not remove some stored data. Try again later.'
        : '';

      status.set(
        parts
          ? `Deleted ${parts}.${open}${failed}`
          : `There was nothing left to clean up.${failed}`,
      );
    } catch (error) {
      status.set(store.describeError(error, 'clean up the storage'));
    } finally {
      cleanup.busy = '';
    }
  }
</script>

<style scoped>
  .builder-settings__intro {
    margin: 0 0 0.25rem;
  }

  .builder-settings__body :deep(.builder-settings__heading) {
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
     tops. The Custom zoom is taller, with a hint under its field. */
  .builder-settings__options {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    column-gap: 1.25rem;
  }

  /* The Custom choice with its field and the field's hint under it. They
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

  /* As tall as the settings, up to the dialog's limit: then the settings
     scroll, and the buttons stay in view under them. */
  .builder-settings[open] {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  /* The box that scrolls clips what is outside it, so it reaches 0.4rem
     past the settings on each side, and a focus ring (3px, 2px out) of a
     field at an edge shows whole. */
  .builder-settings__body {
    flex: 1 1 auto;
    min-height: 0;
    margin: -0.4rem;
    padding: 0.4rem;
    overflow-y: auto;
  }

  .builder-settings__actions {
    flex: none;
    margin-top: 0.75rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--bx-border);
  }
</style>
