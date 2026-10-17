<!--
  The layers of the layers editor (dialogs/LayersDialog.vue): one row for
  each layer, the highest ID first, as the By layer layout stacks them, and
  Add layer under them.

  A row has the layer's ID (required, 0 to 99), its icon (optional, see
  LayerIconPicker.vue), its label (required) and Remove, which names the
  layer. Under them are its color (optional, see LayerColorField.vue), how
  many devices and switches are at the layer (with `counts`), and its node
  matchers. The label takes the layer's color where it keeps its contrast,
  as LayerName.vue does, else a swatch shows beside it.

  A node matcher is a field of a device, as a dotted path into its phenix
  spec (general.hostname), and a regular expression. A device that no one
  put at a layer is at the layer whose matcher matches its field. Each
  matcher is a row with the field, the pattern and Remove. Add node matcher
  adds an empty row, which is written into the layer once both of its
  fields are set.

  What the rows edit, and how, is the parent's (`ops`): the open diagram,
  where each change is one undo step, or a copy of a layer set, which Save
  writes. Each op gives what the dialog says of the change, or nothing when
  it did not change anything:

    add()                 {layer, message} or null
    renumber(from, to)    message or ''
    update(id, changes)   message or '': label, iconKey, icon, color or
                          matchers
    remove(id)            message or ''

  A field changes the layer when its text changes and it loses focus, or on
  Enter, so the typing in one field is one change. A value that the layer
  cannot hold (see layerLabelProblem, matcherPathProblem and
  matcherPatternProblem in model.js) shows an error under its row. The
  field keeps the text typed, and nothing is written until the text is
  fixed.

  A new ID, or a new field of a matcher, moves its row. The row keeps its
  key, so its fields stay the same elements, and focus goes back to the
  field it was in when the move took it away. A change waits until Tab has
  moved focus, because moving the row while Tab moves focus would stop
  that move.

  What the rows did goes to the dialog's status region (`status`).
-->
<template>
  <div
    ref="rootEl"
    class="builder-layer-rows"
    @focusin="lastFocus = $event.target">
    <p
      id="layer-rows-matcher-hint"
      class="builder-hint builder-layer-rows__hint">
      A node matcher puts a device at its layer when the pattern, a regular
      expression, matches a field of the device, such as general.hostname. A
      layer that you choose for a device goes first.
    </p>
    <div class="builder-layer-rows__head" aria-hidden="true">
      <span>ID</span>
      <span>Icon</span>
      <span>Label</span>
    </div>
    <ul class="builder-layer-rows__list">
      <li
        v-for="row in rows"
        :key="row.key"
        class="builder-layer-rows__row"
        :data-layer-id="row.id"
        data-testid="layer-row">
        <input
          type="number"
          class="builder-layer-rows__id"
          :value="typed[`${row.key}:id`] ?? row.id"
          min="0"
          :max="MAX_LAYER_ID"
          step="1"
          inputmode="numeric"
          autocomplete="off"
          required
          :readonly="readOnly"
          :aria-label="`Layer ${row.id} ID`"
          :aria-invalid="errors[`${row.key}:id`] ? 'true' : undefined"
          :aria-describedby="
            errors[`${row.key}:id`] ? `${row.key}-id-error` : undefined
          "
          data-testid="layer-id"
          @input="typed[`${row.key}:id`] = $event.target.value"
          @change="commitId(row, $event.target.value)" />
        <layer-icon-picker
          class="builder-layer-rows__icon"
          :icon-key="row.iconKey || ''"
          :icon="row.icon || ''"
          :label="`Icon of layer ${row.id}`"
          :read-only="readOnly"
          @change="(icon) => change(row, icon)" />
        <div
          class="builder-layer-rows__label"
          :class="labelInk(row)"
          :style="row.color ? { '--layer-color': row.color } : undefined">
          <input
            type="text"
            :value="typed[`${row.key}:label`] ?? row.label"
            autocomplete="off"
            spellcheck="false"
            required
            :readonly="readOnly"
            :aria-label="`Layer ${row.id} label`"
            :aria-invalid="errors[`${row.key}:label`] ? 'true' : undefined"
            :aria-describedby="labelDescription(row)"
            data-testid="layer-label"
            @input="typed[`${row.key}:label`] = $event.target.value"
            @change="commitLabel(row, $event.target.value)" />
          <span
            v-if="row.color"
            class="builder-layer-name__swatch"
            aria-hidden="true" />
        </div>
        <button
          v-if="!readOnly"
          type="button"
          class="builder-button builder-layer-rows__remove"
          :aria-label="`Remove layer ${row.id} – ${row.label}`"
          data-testid="layer-remove"
          @click="remove(row)">
          <builder-icon name="trash" :size="14" />
        </button>
        <p
          v-if="errors[`${row.key}:id`]"
          :id="`${row.key}-id-error`"
          class="builder-dialog__error builder-layer-rows__error"
          data-testid="layer-id-error">
          {{ errors[`${row.key}:id`] }}
        </p>
        <p
          v-if="errors[`${row.key}:label`]"
          :id="`${row.key}-label-error`"
          class="builder-dialog__error builder-layer-rows__error"
          data-testid="layer-label-error">
          {{ errors[`${row.key}:label`] }}
        </p>

        <div class="builder-layer-rows__more">
          <layer-color-field
            :id="`${row.key}-color`"
            :color="row.color || ''"
            :label="`Color of layer ${row.id}`"
            :read-only="readOnly"
            @change="(color) => change(row, { color })"
            @status="(text) => emit('status', text)" />
          <p
            v-if="counts"
            :id="`${row.key}-uses`"
            class="builder-hint builder-layer-rows__uses">
            {{ usesText(row.id) }}
          </p>
        </div>

        <div
          v-if="matcherRows(row).length || !readOnly"
          class="builder-layer-rows__matchers"
          data-testid="layer-matchers">
          <p class="builder-layer-rows__matchers-head">
            Node matchers<span
              v-if="!matcherRows(row).length"
              class="builder-layer-rows__none"
              >: none</span
            >
          </p>
          <ul v-if="matcherRows(row).length" class="builder-layer-rows__list">
            <li
              v-for="(matcher, index) in matcherRows(row)"
              :key="matcher.key"
              class="builder-layer-rows__matcher"
              :data-matcher-path="matcher.path || undefined"
              data-testid="layer-matcher">
              <input
                type="text"
                :value="typed[`${matcher.key}:path`] ?? matcher.path"
                placeholder="general.hostname"
                autocomplete="off"
                spellcheck="false"
                :readonly="readOnly"
                :aria-label="`Field of node matcher ${index + 1} of layer ${row.id}`"
                :aria-invalid="
                  errors[`${matcher.key}:path`] ? 'true' : undefined
                "
                :aria-describedby="
                  describe(`${matcher.key}:path`, 'layer-rows-matcher-hint')
                "
                data-testid="layer-matcher-path"
                @input="typed[`${matcher.key}:path`] = $event.target.value"
                @change="
                  commitMatcher(row, matcher, 'path', $event.target.value)
                " />
              <input
                type="text"
                :value="typed[`${matcher.key}:pattern`] ?? matcher.pattern"
                placeholder="^plc-"
                autocomplete="off"
                spellcheck="false"
                :readonly="readOnly"
                :aria-label="`Pattern of node matcher ${index + 1} of layer ${row.id}`"
                :aria-invalid="
                  errors[`${matcher.key}:pattern`] ? 'true' : undefined
                "
                :aria-describedby="describe(`${matcher.key}:pattern`)"
                data-testid="layer-matcher-pattern"
                @input="typed[`${matcher.key}:pattern`] = $event.target.value"
                @change="
                  commitMatcher(row, matcher, 'pattern', $event.target.value)
                " />
              <button
                v-if="!readOnly"
                type="button"
                class="builder-button builder-layer-rows__remove"
                :aria-label="`Remove node matcher ${index + 1} of layer ${row.id}`"
                data-testid="layer-matcher-remove"
                @click="removeMatcher(row, matcher)">
                <builder-icon name="trash" :size="14" />
              </button>
              <p
                v-for="field in ['path', 'pattern']"
                v-show="errors[`${matcher.key}:${field}`]"
                :id="`${matcher.key}-${field}-error`"
                :key="field"
                class="builder-dialog__error builder-layer-rows__error"
                :data-testid="`layer-matcher-${field}-error`">
                {{ errors[`${matcher.key}:${field}`] }}
              </p>
            </li>
          </ul>
          <button
            v-if="!readOnly"
            type="button"
            class="builder-button builder-layer-rows__add-matcher"
            :aria-label="`Add node matcher to layer ${row.id}`"
            aria-describedby="layer-rows-matcher-hint"
            data-testid="layer-matcher-add"
            @click="addMatcher(row)">
            <builder-icon name="plus" :size="12" />
            Add node matcher
          </button>
        </div>
      </li>
    </ul>

    <div v-if="!readOnly" class="builder-layer-rows__actions">
      <button
        ref="addButton"
        type="button"
        class="builder-button"
        :aria-disabled="full ? 'true' : undefined"
        aria-describedby="layer-rows-add-hint"
        data-testid="layer-add"
        @click="add">
        <builder-icon name="plus" :size="14" />
        Add layer
      </button>
      <p id="layer-rows-add-hint" class="builder-hint">
        A new layer takes the next free ID. You can have at most
        {{ MAX_LAYERS }} layers, with IDs from 0 to {{ MAX_LAYER_ID }}.
      </p>
    </div>
  </div>
</template>

<script setup>
  import { computed, nextTick, reactive, ref } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import LayerColorField from './LayerColorField.vue';
  import LayerIconPicker from './LayerIconPicker.vue';

  import { count } from '@/builder/announce.js';
  import { MAX_LAYER_MATCHERS } from '@/builder/layerMatchers.js';
  import { layerInk } from '@/builder/layerLook.js';
  import {
    effectiveLayerList,
    layerIdOf,
    layerLabelProblem,
    MAX_LAYER_ID,
    MAX_LAYERS,
    matcherPathProblem,
    matcherPatternProblem,
    nextLayerId,
  } from '@/builder/model.js';

  const props = defineProps({
    // The layers as a document or a set keeps them: empty for the
    // placeholder layers.
    layers: { type: Array, default: () => [] },
    // What a change does (see above).
    ops: { type: Object, required: true },
    readOnly: { type: Boolean, default: false },
    // How many devices and switches are at each layer ID, for the hint
    // under each row. null: no hint.
    counts: { type: Map, default: null },
  });

  // status: a message for the dialog's status region.
  const emit = defineEmits(['status']);

  const rootEl = ref(null);
  const addButton = ref(null);
  // The field or button that last had focus in the rows (see restoreFocus).
  const lastFocus = ref(null);

  const full = computed(() => nextLayerId(props.layers) === null);

  // A key for each layer ID, kept when the ID changes (see commitId), so a
  // row is the same element after its ID changes.
  const keys = new Map();
  let lastKey = 0;

  function newKey(kind) {
    lastKey += 1;

    return `layer-rows-${kind}-${lastKey}`;
  }

  function keyOf(id) {
    if (!keys.has(id)) {
      keys.set(id, newKey('layer'));
    }

    return keys.get(id);
  }

  const rows = computed(() =>
    effectiveLayerList(props.layers).map((layer) => ({
      ...layer,
      key: keyOf(layer.id),
    })),
  );

  // Text typed in a field and not written yet, and the error of a field, by
  // `${row key}:id`, `${row key}:label`, `${matcher key}:path` or
  // `${matcher key}:pattern`.
  const typed = reactive({});
  const errors = reactive({});

  function settle(field) {
    delete typed[field];
    delete errors[field];
  }

  function fail(field, message) {
    errors[field] = message;
    emit('status', message);
  }

  function describe(field, ...more) {
    return (
      [errors[field] ? `${field.replace(/:/g, '-')}-error` : '', ...more]
        .filter(Boolean)
        .join(' ') || undefined
    );
  }

  // The label's color, where it reads in the theme shown (see LayerName).
  function labelInk(row) {
    const ink = layerInk(row.color);

    return { 'has-ink-light': ink.light, 'has-ink-dark': ink.dark };
  }

  function usesText(id) {
    const used = props.counts?.get(id) || 0;

    return used
      ? `${count(used, 'node')} ${used === 1 ? 'is' : 'are'} at this layer.`
      : 'No nodes are at this layer.';
  }

  function labelDescription(row) {
    const ids = [];

    if (props.counts) {
      ids.push(`${row.key}-uses`);
    }

    if (errors[`${row.key}:label`]) {
      ids.push(`${row.key}-label-error`);
    }

    return ids.join(' ') || undefined;
  }

  // After a change moves a row, focus can leave the field it was in. It
  // goes back there.
  async function restoreFocus() {
    await nextTick();

    const field = lastFocus.value;

    if (
      field?.isConnected &&
      document.activeElement !== field &&
      !rootEl.value?.contains(document.activeElement)
    ) {
      field.focus();
    }
  }

  // A change fires while Tab is still moving focus. A change that moves a
  // row waits until focus is in the next field.
  function afterTab() {
    return new Promise((resolve) => setTimeout(resolve));
  }

  function said(message) {
    if (message) {
      emit('status', message);
    }

    return Boolean(message);
  }

  // --- the fields of a layer ----------------------------------------------

  async function commitId(row, text) {
    const field = `${row.key}:id`;
    const id = layerIdOf(String(text).trim());

    if (typeof id !== 'number') {
      fail(field, `Enter a layer ID from 0 to ${MAX_LAYER_ID}.`);

      return;
    }

    if (id === row.id) {
      settle(field);

      return;
    }

    if (rows.value.some((other) => other.id === id)) {
      fail(field, `Layer ID ${id} is in use. Enter a different ID.`);

      return;
    }

    await afterTab();

    const done = said(props.ops.renumber(row.id, id));

    settle(field);

    if (!done) {
      return;
    }

    keys.delete(row.id);
    keys.set(id, row.key);
    restoreFocus();
  }

  function commitLabel(row, text) {
    const field = `${row.key}:label`;
    const label = String(text).trim();
    const problem = layerLabelProblem(label);

    if (problem) {
      fail(field, problem);

      return;
    }

    if (label !== row.label) {
      said(props.ops.update(row.id, { label }));
    }

    settle(field);
  }

  // The icon or the color, which change at once.
  function change(row, changes) {
    said(props.ops.update(row.id, changes));
  }

  // Focus goes to the next row's Remove, else the one before, else Add
  // layer.
  async function remove(row) {
    const at = rows.value.findIndex((other) => other.key === row.key);

    if (!said(props.ops.remove(row.id))) {
      return;
    }

    for (const field of Object.keys(typed)) {
      if (field.startsWith(`${row.key}:`)) {
        settle(field);
      }
    }

    delete pending[row.key];

    await nextTick();

    const buttons = [
      ...(rootEl.value?.querySelectorAll('[data-testid="layer-remove"]') || []),
    ];

    (buttons[Math.min(at, buttons.length - 1)] || addButton.value)?.focus();
  }

  // Focus goes to the new layer's label, to type its name.
  async function add() {
    if (full.value) {
      emit('status', `You can have at most ${MAX_LAYERS} layers.`);

      return;
    }

    const added = props.ops.add();

    if (!added) {
      return;
    }

    emit('status', added.message);

    await nextTick();

    rootEl.value
      ?.querySelector(
        `[data-layer-id="${added.layer.id}"] [data-testid="layer-label"]`,
      )
      ?.select();
  }

  // --- node matchers -------------------------------------------------------

  // A key for each matcher, by its layer's key and its field, kept when its
  // field changes, and the matchers added and not written yet, by the
  // layer's key: [{key}].
  const matcherKeys = new Map();
  const pending = reactive({});

  function matcherKey(rowKey, path) {
    const id = `${rowKey}\u0000${path}`;

    if (!matcherKeys.has(id)) {
      matcherKeys.set(id, newKey('matcher'));
    }

    return matcherKeys.get(id);
  }

  // The matchers of a layer: those written, in the order of their fields,
  // then those added and not written yet.
  function matcherRows(row) {
    return [
      ...Object.entries(row.matchers || {}).map(([path, pattern]) => ({
        key: matcherKey(row.key, path),
        path,
        pattern,
        written: true,
      })),
      ...(pending[row.key] || []).map(({ key }) => ({
        key,
        path: '',
        pattern: '',
        written: false,
      })),
    ];
  }

  function textOf(matcher, field) {
    return typed[`${matcher.key}:${field}`] ?? matcher[field];
  }

  function matcherProblem(field, value) {
    return field === 'path'
      ? matcherPathProblem(value)
      : matcherPatternProblem(value);
  }

  // A field of a matcher changed. The field is checked. A matcher not
  // written yet waits for its other field, and is then written into the
  // layer. A written matcher is written again with the new value.
  async function commitMatcher(row, matcher, field, text) {
    const other = field === 'path' ? 'pattern' : 'path';
    const values = {
      [field]: field === 'path' ? String(text).trim() : String(text),
      [other]: textOf(matcher, other),
    };

    values.path = String(values.path).trim();

    const problem = matcherProblem(field, values[field]);

    if (problem) {
      fail(`${matcher.key}:${field}`, problem);

      return;
    }

    delete errors[`${matcher.key}:${field}`];

    const matchers = row.matchers || {};

    if (
      field === 'path' &&
      values.path !== matcher.path &&
      Object.hasOwn(matchers, values.path)
    ) {
      fail(
        `${matcher.key}:path`,
        `Layer ${row.id} has a node matcher for ${values.path} already. Enter a different field.`,
      );

      return;
    }

    if (matcherProblem(other, values[other])) {
      if (!matcher.written && values[other] === '') {
        return;
      }

      fail(`${matcher.key}:${other}`, matcherProblem(other, values[other]));

      return;
    }

    if (
      matcher.written &&
      values.path === matcher.path &&
      values.pattern === matcher.pattern
    ) {
      settle(`${matcher.key}:path`);
      settle(`${matcher.key}:pattern`);

      return;
    }

    const next = { ...matchers };

    if (matcher.written) {
      delete next[matcher.path];
    }

    next[values.path] = values.pattern;

    await afterTab();

    if (!said(props.ops.update(row.id, { matchers: next }))) {
      return;
    }

    settle(`${matcher.key}:path`);
    settle(`${matcher.key}:pattern`);
    matcherKeys.delete(`${row.key}\u0000${matcher.path}`);
    matcherKeys.set(`${row.key}\u0000${values.path}`, matcher.key);

    if (!matcher.written) {
      pending[row.key] = (pending[row.key] || []).filter(
        (entry) => entry.key !== matcher.key,
      );
    }

    restoreFocus();
  }

  // Focus goes to the next matcher's Remove, else the one before, else Add
  // node matcher.
  async function removeMatcher(row, matcher) {
    const list = matcherRows(row);
    const at = list.findIndex((entry) => entry.key === matcher.key);

    if (matcher.written) {
      const next = { ...(row.matchers || {}) };

      delete next[matcher.path];

      if (!said(props.ops.update(row.id, { matchers: next }))) {
        return;
      }
    } else {
      pending[row.key] = (pending[row.key] || []).filter(
        (entry) => entry.key !== matcher.key,
      );
      emit('status', `Removed a node matcher of layer ${row.id}.`);
    }

    settle(`${matcher.key}:path`);
    settle(`${matcher.key}:pattern`);

    await nextTick();

    const item = rootEl.value?.querySelector(
      `[data-layer-id="${row.id}"] [data-testid="layer-matchers"]`,
    );
    const buttons = [
      ...(item?.querySelectorAll('[data-testid="layer-matcher-remove"]') || []),
    ];

    (
      buttons[Math.min(at, buttons.length - 1)] ||
      item?.querySelector('[data-testid="layer-matcher-add"]')
    )?.focus();
  }

  // Focus goes to the new matcher's field.
  async function addMatcher(row) {
    if (matcherRows(row).length >= MAX_LAYER_MATCHERS) {
      emit(
        'status',
        `A layer has at most ${MAX_LAYER_MATCHERS} node matchers.`,
      );

      return;
    }

    const key = newKey('matcher');

    pending[row.key] = [...(pending[row.key] || []), { key }];

    await nextTick();

    const fields = rootEl.value?.querySelectorAll(
      `[data-layer-id="${row.id}"] [data-testid="layer-matcher-path"]`,
    );

    fields?.[fields.length - 1]?.focus();
  }

  defineExpose({
    // Whether a field holds text that is not written, for Save in a layer
    // set, which then asks to fix it.
    get unsettled() {
      return Object.keys(errors).length > 0;
    },
    // Focuses the first field with an error.
    focusError() {
      rootEl.value?.querySelector('[aria-invalid="true"]')?.focus();
    },
  });
</script>

<style scoped>
  .builder-layer-rows__head,
  .builder-layer-rows__row {
    display: grid;
    grid-template-columns: 4rem auto minmax(0, 1fr) auto;
    column-gap: 0.4rem;
    align-items: center;
  }

  .builder-layer-rows .builder-layer-rows__hint {
    margin: 0 0 0.5rem;
  }

  .builder-layer-rows__head {
    font-size: 0.85rem;
    font-weight: 600;
  }

  .builder-layer-rows__head span:last-child {
    grid-column: 3;
  }

  .builder-layer-rows__list {
    margin: 0.2rem 0 0;
    padding: 0;
    list-style: none;
  }

  .builder-layer-rows__row {
    margin-bottom: 0.6rem;
    padding-bottom: 0.6rem;
    border-bottom: 1px solid var(--bx-border);
    row-gap: 0.3rem;
  }

  .builder-layer-rows input[type='number'],
  .builder-layer-rows input[type='text'] {
    width: 100%;
    min-width: 0;
    padding: 0.3rem 0.4rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    color: var(--bx-text);
  }

  .builder-layer-rows__label {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    min-width: 0;
  }

  /* As LayerName.vue draws it. */
  .builder-layer-rows__label .builder-layer-name__swatch {
    flex: none;
    width: 0.8em;
    height: 0.8em;
    border: 1px solid var(--bx-text);
    border-radius: 2px;
    background: var(--layer-color);
  }

  .builder-layer-rows__remove {
    grid-column: 4;
  }

  .builder-layer-rows__error {
    grid-column: 1 / -1;
    margin: 0;
    font-size: 0.85em;
  }

  /* The color and the hint under the icon, the label and Remove. */
  .builder-layer-rows__more {
    grid-column: 2 / -1;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    column-gap: 0.75rem;
    row-gap: 0.2rem;
  }

  .builder-layer-rows .builder-layer-rows__uses {
    margin: 0;
  }

  .builder-layer-rows__matchers {
    grid-column: 2 / -1;
  }

  .builder-layer-rows__matchers-head {
    margin: 0.2rem 0 0.3rem;
    font-size: 0.85rem;
    font-weight: 600;
  }

  .builder-layer-rows__matcher {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
    column-gap: 0.3rem;
    align-items: center;
    margin-bottom: 0.3rem;
  }

  .builder-layer-rows__matcher .builder-layer-rows__remove {
    grid-column: 3;
  }

  .builder-layer-rows__matchers-head .builder-layer-rows__none {
    font-weight: 400;
    color: var(--bx-text-muted);
  }

  .builder-layer-rows__add-matcher {
    font-size: 0.85rem;
  }

  .builder-layer-rows__actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.4rem;
  }

  .builder-layer-rows__actions .builder-hint {
    margin: 0;
  }

  /* A narrow window: the label goes under the ID, the icon and Remove (the
     fields' names say which is which), and the color, the hint and the matchers under the label, each as wide
     as the row. The matcher's pattern goes under its field. */
  @media (max-width: 30rem) {
    .builder-layer-rows__head {
      display: none;
    }

    .builder-layer-rows__label {
      grid-column: 1 / -1;
      grid-row: 2;
    }

    .builder-layer-rows__remove {
      grid-row: 1;
    }

    .builder-layer-rows__more,
    .builder-layer-rows__matchers {
      grid-column: 1 / -1;
    }

    .builder-layer-rows__matcher {
      grid-template-columns: minmax(0, 1fr) auto;
      row-gap: 0.3rem;
    }

    .builder-layer-rows__matcher input + input {
      grid-column: 1;
      grid-row: 2;
    }

    .builder-layer-rows__matcher .builder-layer-rows__remove {
      grid-column: 2;
      grid-row: 1;
    }
  }
</style>

<!-- The theme shown is an attribute of the page's root, outside this
     component, so these rules are not scoped. The label takes the layer's
     color where it reads, and its swatch goes. -->
<style>
  .builder-root:not([data-builder-theme='dark'])
    .builder-layer-rows__label.has-ink-light
    input,
  .builder-root[data-builder-theme='dark']
    .builder-layer-rows__label.has-ink-dark
    input {
    color: var(--layer-color);
  }

  .builder-root:not([data-builder-theme='dark'])
    .builder-layer-rows__label.has-ink-light
    .builder-layer-name__swatch,
  .builder-root[data-builder-theme='dark']
    .builder-layer-rows__label.has-ink-dark
    .builder-layer-name__swatch {
    display: none;
  }

  /* Forced colors replace the label's color, so the swatch shows. */
  @media (forced-colors: active) {
    .builder-layer-rows__label .builder-layer-name__swatch {
      display: inline-block !important;
      forced-color-adjust: none;
      border-color: CanvasText;
    }
  }
</style>
