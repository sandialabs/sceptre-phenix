<!--
  Keyboard shortcut sheet, and where the shortcuts are changed.

  Written from the command registry (builder/commands.js), so it lists the
  keys the dispatcher handles, as this platform writes them, and where each
  one works. "Change shortcuts" turns the same dialog into the customization:
  every command whose keys can change, a key recorder per command, Remove,
  Reset and Reset all, all kept per browser by builder/keymap.js. The
  single-key switch (WCAG 2.1.4) is in both. The Settings dialog opens the
  customization directly (customize), and Done then closes the sheet.

  The Builder's live region waits while a modal dialog is open, so the sheet
  speaks through status regions of its own: the recorder's message, and a
  hidden one for everything else.
-->
<template>
  <builder-dialog
    :title="customizing ? 'Change keyboard shortcuts' : 'Keyboard shortcuts'"
    title-id="shortcuts-dialog-title"
    class="builder-shortcuts"
    @close="$emit('close')">
    <p ref="introEl" class="builder-hint builder-shortcuts__intro">
      {{
        customizing
          ? 'Changes apply at once, everywhere, and are kept in this browser only.'
          : `Keys for ${platformName}, and where each one works.`
      }}
    </p>

    <!-- The single-key switch, and the way in and out of customization,
         first: the switch is what WCAG 2.1.4 asks for, and the list below
         can be long. -->
    <div class="builder-shortcuts__settings">
      <div class="builder-shortcuts__single">
        <builder-switch
          :model-value="keymapState.singleKeys"
          aria-describedby="shortcuts-single-hint"
          data-testid="shortcuts-single-keys"
          @update:model-value="setSingleKeyShortcuts">
          Single-key shortcuts
        </builder-switch>
        <p id="shortcuts-single-hint" class="builder-hint">
          {{ singleKeyHint() }}
        </p>
      </div>

      <div class="builder-shortcuts__buttons">
        <template v-if="customizing">
          <button
            type="button"
            class="builder-button"
            :aria-disabled="anyChanged ? undefined : 'true'"
            data-testid="shortcuts-reset-all"
            @click="resetAll">
            Reset all shortcuts
          </button>
          <button
            type="button"
            class="builder-button builder-button--primary"
            data-testid="shortcuts-done"
            @click="setCustomizing(false)">
            Done
          </button>
        </template>
        <button
          v-else
          ref="customizeEl"
          type="button"
          class="builder-button"
          data-testid="shortcuts-customize"
          @click="setCustomizing(true)">
          Change shortcuts
        </button>
      </div>
    </div>

    <div class="builder-field builder-shortcuts__filter">
      <label for="shortcuts-filter">Filter shortcuts</label>
      <input
        id="shortcuts-filter"
        ref="filterEl"
        v-model="query"
        type="text"
        autocomplete="off"
        spellcheck="false"
        aria-describedby="shortcuts-filter-hint"
        data-testid="shortcuts-filter" />
      <p id="shortcuts-filter-hint" class="builder-hint">
        An action, or a key such as G
      </p>
    </div>

    <p v-if="!groups.length" data-testid="shortcuts-empty">
      Nothing matches “{{ query.trim() }}”.
    </p>

    <div
      class="builder-shortcuts__groups"
      :class="{ 'builder-shortcuts__groups--edit': customizing }">
      <!-- Headed lists rather than regions: nine landmarks in one dialog
           would crowd a screen reader's landmark list. -->
      <div
        v-for="group in groups"
        :key="group.group"
        class="builder-shortcuts__group">
        <h3 :id="groupId(group.group)">{{ group.group }}</h3>
        <ul
          class="builder-shortcuts__rows"
          :aria-labelledby="groupId(group.group)">
          <li
            v-for="row in group.rows"
            :key="row.id"
            class="builder-shortcuts__row"
            :data-testid="`shortcut-${row.id}`"
            @keydown="onRowKeydown($event, row)">
            <div class="builder-shortcuts__name">
              <span class="builder-shortcuts__title">{{ row.title }}</span>
              <span class="builder-shortcuts__where">{{ row.where }}</span>
              <span
                v-if="customizing && row.changed"
                class="builder-shortcuts__where">
                Default: {{ defaultText(row) }}
              </span>
            </div>

            <span class="builder-shortcuts__keys" data-testid="shortcut-keys">
              <template v-if="row.keys.length">
                <template v-for="(key, index) in row.keys" :key="key.spec">
                  <span
                    v-if="index && row.keys.length === 2"
                    class="builder-shortcuts__or"
                    aria-hidden="true">
                    or
                  </span>
                  <span
                    class="builder-shortcuts__combo"
                    :class="{ 'builder-shortcuts__combo--off': key.off }">
                    <span aria-hidden="true">
                      <kbd v-for="(cap, at) in key.caps" :key="at">{{
                        cap
                      }}</kbd>
                    </span>
                    <span class="builder-visually-hidden">
                      {{ index ? 'or ' : '' }}{{ key.spoken }}
                    </span>
                    <span v-if="key.off" class="builder-shortcuts__off">
                      off
                    </span>
                  </span>
                </template>
              </template>
              <span v-else class="builder-shortcuts__none">No shortcut</span>
            </span>

            <div
              v-if="customizing && recording?.id !== row.id"
              class="builder-shortcuts__actions">
              <button
                :ref="(el) => setChangeButton(row.id, el)"
                type="button"
                class="builder-button"
                data-testid="shortcut-change"
                @click="startRecording(row)">
                Change<span class="builder-visually-hidden">
                  shortcut for {{ row.name }}</span
                >
              </button>
              <button
                v-if="row.keys.length"
                type="button"
                class="builder-button"
                data-testid="shortcut-remove"
                @click="remove(row)">
                Remove<span class="builder-visually-hidden">
                  shortcut for {{ row.name }}</span
                >
              </button>
              <button
                v-if="row.changed"
                type="button"
                class="builder-button"
                data-testid="shortcut-reset"
                @click="reset(row)">
                Reset<span class="builder-visually-hidden">
                  {{ row.name }} to its default</span
                >
              </button>
            </div>

            <!-- The key recorder. A text field, so screen readers pass the
                 keys on to it; it types nothing, and Tab still leaves it. -->
            <div
              v-if="recording?.id === row.id"
              class="builder-shortcuts__recorder"
              data-testid="shortcut-recorder">
              <label for="shortcuts-recorder" class="builder-shortcuts__prompt">
                Press the new shortcut<span class="builder-visually-hidden">
                  for {{ row.name }}</span
                >
              </label>
              <div class="builder-shortcuts__record">
                <input
                  id="shortcuts-recorder"
                  :ref="setRecorder"
                  class="builder-shortcuts__input"
                  type="text"
                  :value="recording.verdict?.label || ''"
                  autocomplete="off"
                  spellcheck="false"
                  :aria-invalid="refused ? 'true' : undefined"
                  aria-describedby="shortcuts-recorder-hint"
                  data-testid="shortcut-recorder-input"
                  @keydown="onRecorderKeydown($event, row)"
                  @beforeinput.prevent
                  @input="showRecorded"
                  @paste.prevent
                  @drop.prevent />
                <button
                  v-if="canKeep"
                  type="button"
                  class="builder-button builder-button--primary"
                  data-testid="shortcut-keep"
                  @click="keep(row)">
                  Keep
                </button>
                <button
                  v-if="recording.verdict?.status === 'conflict'"
                  type="button"
                  class="builder-button builder-button--primary"
                  data-testid="shortcut-take"
                  @click="keep(row, { take: true })">
                  Use for {{ row.name }}
                </button>
                <button
                  type="button"
                  class="builder-button"
                  data-testid="shortcut-cancel"
                  @click="stopRecording({ cancelled: true })">
                  Cancel
                </button>
              </div>
              <p id="shortcuts-recorder-hint" class="builder-hint">
                Enter keeps it and Escape cancels. Keys the browser keeps, such
                as {{ browserKey }}, cannot be chosen.
              </p>
              <p
                class="builder-shortcuts__message"
                :data-tone="recording.message?.tone"
                role="status"
                data-testid="shortcut-recorder-message">
                <span v-if="recording.message" :key="recording.seq">
                  <template
                    v-for="(part, index) in recording.message.parts"
                    :key="index">
                    <span
                      v-if="part.key"
                      class="builder-shortcuts__combo"
                      aria-hidden="true">
                      <kbd v-for="(cap, at) in recordedCaps" :key="at">{{
                        cap
                      }}</kbd>
                    </span>
                    <span v-if="part.key" class="builder-visually-hidden">{{
                      recording.verdict.spoken
                    }}</span>
                    <template v-else>{{ part.text }}</template>
                  </template>
                </span>
              </p>
            </div>
          </li>
        </ul>
      </div>
    </div>

    <p
      class="builder-visually-hidden"
      role="status"
      data-testid="shortcuts-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    ref,
    watch,
  } from 'vue';

  import BuilderDialog from './BuilderDialog.vue';
  import BuilderSwitch from './BuilderSwitch.vue';
  import { useMessage } from './dialogs/message.js';

  import { count } from '@/builder/announce.js';
  import { commandKeys } from '@/builder/commands.js';
  import {
    currentPlatform,
    eventToKey,
    keyCaps,
    keyLabel,
    keymapState,
    resetAllShortcuts,
    resetShortcut,
    setSingleKeyShortcuts,
  } from '@/builder/keymap.js';
  import {
    assignShortcut,
    judgeShortcut,
    recorderMessage,
    removeShortcut,
    shortcutGroups,
    singleKeyHint,
    spokenKeys,
  } from '@/builder/shortcuts.js';

  const props = defineProps({
    // Opens on the customization, for the Settings dialog's Change keyboard
    // shortcuts: Done then closes the sheet, back to Settings.
    customize: { type: Boolean, default: false },
  });

  const emit = defineEmits(['close']);

  const introEl = ref(null);
  const filterEl = ref(null);
  const customizeEl = ref(null);
  const query = ref('');
  const customizing = ref(props.customize);
  // The command whose key is being recorded: {id, name, verdict, message,
  // seq}; verdict and message are null until a key is pressed.
  const recording = ref(null);
  const status = useMessage();

  const platformName = computed(() =>
    currentPlatform() === 'mac' ? 'macOS' : 'Windows and Linux',
  );
  const browserKey = computed(() => keyLabel('Mod+T'));

  const groups = computed(() =>
    shortcutGroups({
      query: query.value,
      customize: customizing.value,
      keep: recording.value?.id || '',
    }),
  );

  const anyChanged = computed(
    () => Object.keys(keymapState.overrides).length > 0,
  );

  const recordedCaps = computed(() =>
    recording.value?.verdict?.spec ? keyCaps(recording.value.verdict.spec) : [],
  );
  const refused = computed(() =>
    ['refused', 'fixed'].includes(recording.value?.verdict?.status),
  );
  const canKeep = computed(() =>
    ['free', 'same'].includes(recording.value?.verdict?.status),
  );

  function groupId(group) {
    return `shortcuts-group-${group.toLowerCase().replace(/\W+/g, '-')}`;
  }

  function defaultText(row) {
    return row.defaults.length
      ? row.defaults.map((key) => key.label).join(' or ')
      : 'none';
  }

  // The filter's result count, once typing pauses. Anything else said in
  // the meantime is about what the user did since, so the count is dropped.
  let countTimer = null;

  function say(text) {
    clearTimeout(countTimer);
    status.set(text);
  }

  watch(query, () => {
    clearTimeout(countTimer);
    countTimer = setTimeout(() => {
      const rows = groups.value.reduce(
        (sum, group) => sum + group.rows.length,
        0,
      );

      status.set(
        rows
          ? count(rows, customizing.value ? 'command' : 'shortcut')
          : 'Nothing matches.',
      );
    }, 400);
  });

  // --- focus ---------------------------------------------------------------------

  const changeButtons = new Map();
  let recorderEl = null;

  function setChangeButton(id, element) {
    if (element) {
      changeButtons.set(id, element);
    } else {
      changeButtons.delete(id);
    }
  }

  function setRecorder(element) {
    recorderEl = element;
  }

  // Focus returns to a row's Change button when the control that had it
  // (the recorder, Remove, Reset) is gone.
  async function focusChange(id) {
    await nextTick();
    changeButtons.get(id)?.focus();
  }

  // The dialog's content changes as a whole, so it starts again at the top,
  // and focus moves to the dialog, which screen readers then name by its
  // new title; back on the sheet, to the button that changed it.
  async function setCustomizing(on) {
    if (!on && props.customize) {
      emit('close');
      return;
    }

    customizing.value = on;
    recording.value = null;
    await nextTick();

    const dialog = introEl.value?.closest('dialog');

    if (dialog) {
      dialog.scrollTop = 0;
    }
    if (on) {
      dialog?.focus();
    } else {
      customizeEl.value?.focus();
    }
  }

  // --- recording -------------------------------------------------------------------

  async function startRecording(row) {
    recording.value = {
      id: row.id,
      name: row.name,
      verdict: null,
      message: null,
      seq: 0,
    };
    await nextTick();
    recorderEl?.focus();
  }

  function currentKeys(id) {
    return spokenKeys(commandKeys(id, { all: true }));
  }

  function stopRecording({ cancelled = false } = {}) {
    const current = recording.value;

    if (!current) {
      return;
    }

    recording.value = null;
    if (cancelled) {
      say(`Cancelled. ${current.name} keeps ${currentKeys(current.id)}.`);
    }
    focusChange(current.id);
  }

  function showVerdict(verdict, name) {
    recording.value = {
      ...recording.value,
      verdict,
      message: recorderMessage(verdict, name),
      seq: recording.value.seq + 1,
    };
  }

  // Every key goes to the recorder except Tab, which leaves it as it leaves
  // any field. Plain Enter keeps the key, plain Escape cancels, and plain
  // Backspace or Delete clears what was pressed; with modifiers they are
  // keys like any other.
  function onRecorderKeydown(event, row) {
    if (event.key === 'Tab') {
      return;
    }

    event.preventDefault();
    event.stopPropagation();

    if (event.isComposing || event.keyCode === 229) {
      return;
    }

    const plain = !(
      event.ctrlKey ||
      event.metaKey ||
      event.altKey ||
      event.shiftKey
    );

    if (plain && event.key === 'Escape') {
      stopRecording({ cancelled: true });
    } else if (plain && event.key === 'Enter') {
      keep(row);
    } else if (plain && ['Backspace', 'Delete'].includes(event.key)) {
      recording.value = { ...recording.value, verdict: null, message: null };
      say('Cleared. Press the new shortcut.');
    } else {
      const spec = eventToKey(event);

      if (spec) {
        showVerdict(judgeShortcut(row.id, spec), row.name);
      }
    }
  }

  // Text that got in anyway (composition cannot always be cancelled) is
  // replaced by the recorded key.
  function showRecorded(event) {
    event.target.value = recording.value?.verdict?.label || '';
  }

  // Escape anywhere in the row being recorded (its Cancel or Keep button)
  // cancels the recording rather than closing the dialog.
  function onRowKeydown(event, row) {
    if (event.key === 'Escape' && recording.value?.id === row.id) {
      event.preventDefault();
      event.stopPropagation();
      stopRecording({ cancelled: true });
    }
  }

  function keep(row, { take = false } = {}) {
    const current = recording.value;
    const verdict = current?.verdict;

    if (!verdict) {
      say(`Press the new shortcut for ${row.name} first, or Escape to cancel.`);
      return;
    }

    const result = assignShortcut(row.id, verdict.spec, { take });

    if (!result.ok) {
      // Said again, since Enter did nothing.
      showVerdict(result.verdict, row.name);
      return;
    }

    const taken = result.taken.map((other) =>
      other.keys.length
        ? `${other.name} keeps ${spokenKeys(other.keys)}.`
        : `${other.name} has no shortcut now.`,
    );

    say([`${row.name} now uses ${verdict.spoken}.`, ...taken].join(' '));
    stopRecording();
  }

  // --- remove and reset ------------------------------------------------------------

  function remove(row) {
    removeShortcut(row.id);
    say(`${row.name} has no shortcut now.`);
    focusChange(row.id);
  }

  function reset(row) {
    resetShortcut(row.id);

    const keys = commandKeys(row.id, { all: true });
    say(
      keys.length
        ? `${row.name} uses ${spokenKeys(keys)} again.`
        : `${row.name} has no shortcut again.`,
    );
    focusChange(row.id);
  }

  function resetAll() {
    if (!anyChanged.value) {
      say('Every shortcut is at its default already.');
      return;
    }

    // Focus stays on Reset all, which no longer applies.
    recording.value = null;
    resetAllShortcuts();
    say(
      `Every shortcut is back to its default.${keymapState.singleKeys ? '' : ' Single-key shortcuts are still off.'}`,
    );
  }

  onMounted(() => {
    // BuilderDialog has focused itself; the filter is where to start.
    filterEl.value?.focus();
  });

  onBeforeUnmount(() => clearTimeout(countTimer));
</script>

<style scoped>
  .builder-shortcuts__intro {
    margin: 0 0 0.6rem;
  }

  .builder-shortcuts__filter .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-shortcuts__groups {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 19rem), 1fr));
    align-items: start;
    gap: 0 1.5rem;
  }

  .builder-shortcuts__groups--edit {
    grid-template-columns: minmax(0, 1fr);
  }

  .builder-shortcuts__group h3 {
    margin: 0.75rem 0 0.2rem;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--bx-text-muted);
  }

  .builder-shortcuts__rows {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .builder-shortcuts__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem 0.75rem;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-shortcuts__name {
    display: flex;
    flex: 1 1 10rem;
    flex-direction: column;
    min-width: 0;
  }

  .builder-shortcuts__title {
    font-size: 0.875rem;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .builder-shortcuts__where,
  .builder-shortcuts__none,
  .builder-shortcuts__or,
  .builder-shortcuts__off {
    font-size: 0.78rem;
    color: var(--bx-text-muted);
  }

  .builder-shortcuts__keys {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.3rem;
    max-width: 100%;
  }

  .builder-shortcuts__combo {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  .builder-shortcuts__combo > span[aria-hidden='true'] {
    display: inline-flex;
    gap: 2px;
  }

  .builder-shortcuts kbd {
    display: inline-block;
    min-width: 1.6em;
    padding: 0.1em 0.35em;
    border: 1px solid var(--bx-border-strong);
    border-bottom-width: 2px;
    border-radius: 4px;
    background: var(--bx-surface);
    color: var(--bx-text);
    font: inherit;
    font-size: 0.78rem;
    font-weight: 600;
    line-height: 1.3;
    text-align: center;
    white-space: nowrap;
  }

  /* Switched off: struck through, and "off" says so in words. */
  .builder-shortcuts__combo--off kbd {
    color: var(--bx-text-muted);
    text-decoration: line-through;
  }

  .builder-shortcuts__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }

  .builder-shortcuts__recorder {
    flex: 1 1 100%;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    background: var(--bx-bg-alt);
  }

  .builder-shortcuts__prompt {
    display: block;
    margin-bottom: 0.25rem;
    font-size: 0.85rem;
    font-weight: 600;
  }

  .builder-shortcuts__record {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  /* Nothing is typed into it, so it shows no caret. */
  .builder-shortcuts__input {
    flex: 1 1 8rem;
    min-width: 0;
    padding: 0.35rem 0.45rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    font-weight: 600;
    caret-color: transparent;
  }

  .builder-shortcuts__recorder .builder-hint {
    margin: 0.3rem 0 0;
  }

  /* Rendered, empty, from the start, so the first message is announced; it
     takes no room until then. The words say what kind of message it is,
     the border's color only repeats them. */
  .builder-shortcuts__message {
    margin: 0.4rem 0 0;
    padding: 0.35rem 0.5rem;
    border: 1px solid var(--bx-border-strong);
    border-left-width: 4px;
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }

  .builder-shortcuts__message:empty {
    margin: 0;
    padding: 0;
    border: 0;
  }

  .builder-shortcuts__message[data-tone='warning'] {
    border-color: var(--bx-warning);
  }

  .builder-shortcuts__message[data-tone='error'] {
    border-color: var(--bx-danger);
  }

  .builder-shortcuts__message kbd {
    margin-inline: 1px;
  }

  .builder-shortcuts__settings {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: 0.5rem 1rem;
    margin-bottom: 0.75rem;
    padding-bottom: 0.6rem;
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-shortcuts__single {
    flex: 1 1 16rem;
  }

  .builder-shortcuts__single .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-shortcuts__buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-inline-start: auto;
  }
</style>
