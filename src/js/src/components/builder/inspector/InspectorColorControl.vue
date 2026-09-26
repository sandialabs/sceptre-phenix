<!--
  The color of a network, note, group or connection: a text field, which
  takes any CSS color, and before it a swatch button that opens a color
  picker.

  The picker is a non-modal dialog under the field. Its suggested colors are
  a toolbar of swatch buttons, one Tab stop that the arrow keys move
  through (WAI-ARIA APG toolbar), each named by its color's name and value
  and pressed while it is the field's color. A native color input takes any
  other color, and No color empties the field. A choice goes into the text
  field as a hex color ("#2f6fbf"), as typing it would, and commits; the
  picker then closes and focus returns to the swatch button. Escape closes
  it too, and so do a click outside it and Tab out of it.

  The suggested colors are those addNetwork picks, which the canvas draws
  for a network with the theme's own network colors, so a network given one
  keeps its contrast in both themes (see colors.js). The chip in the swatch
  button and the swatches show a color as the canvas draws it (see
  useInspectorDrawnColor). The swatches keep their colors in forced colors
  mode, where they are what is being chosen.

  The picker is scrolled into view as it opens, so at phone width it does
  not open below the window.
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="control"
    :ids="ids"
    :error-text="errorText"
    :warnings="warnings"
    :changed="changed">
    <div ref="root" class="inspector-color" @focusout="onFocusOut">
      <button
        ref="trigger"
        type="button"
        class="inspector-color__trigger"
        data-testid="inspector-color-picker"
        :aria-label="`Choose ${lowerFirst(control.label || 'color')}`"
        aria-haspopup="dialog"
        :aria-expanded="open ? 'true' : 'false'"
        :aria-controls="open ? ids.popup : undefined"
        :aria-describedby="ids.current"
        :disabled="inputAttrs.disabled || locked"
        @click="toggle">
        <span
          class="inspector-color__chip"
          :class="{ 'is-none': !drawn }"
          :style="drawn ? { background: drawn } : undefined" />
      </button>
      <span :id="ids.current" hidden>{{ currentText }}</span>
      <input
        v-bind="inputAttrs"
        ref="field"
        type="text"
        class="input"
        :value="text"
        @input="onInput"
        @change="commit" />

      <div
        v-if="open"
        :id="ids.popup"
        ref="popup"
        class="inspector-color__popup"
        role="dialog"
        :aria-label="`${control.label || 'Color'} picker`"
        data-testid="inspector-color-popup"
        @keydown.esc.stop.prevent="close(true)">
        <div
          class="inspector-color__palette"
          role="toolbar"
          aria-label="Suggested colors"
          @keydown="onPaletteKey">
          <button
            v-for="(swatch, index) in SWATCHES"
            :key="swatch.value"
            type="button"
            class="inspector-color__option"
            :tabindex="index === roving ? 0 : -1"
            :aria-label="`${swatch.name}, ${swatch.value}`"
            :aria-pressed="swatch.value === current ? 'true' : 'false'"
            @focus="roving = index"
            @click="choose(swatch.value)">
            <span
              class="inspector-color__chip"
              :style="{ background: drawColor(swatch.value) }" />
          </button>
        </div>
        <label class="inspector-color__custom">
          <!-- Its input and change are the picker's own, not the form's. -->
          <input
            type="color"
            :value="customValue"
            @input.stop
            @change.stop="choose($event.target.value)" />
          Custom color
        </label>
        <button
          type="button"
          class="builder-button inspector-color__none"
          :aria-pressed="current ? 'false' : 'true'"
          @click="choose('')">
          No color
        </button>
      </div>
    </div>
  </inspector-field>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, ref, unref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import InspectorField from './InspectorField.vue';
  import {
    useFieldText,
    useInspectorControl,
    useInspectorDrawnColor,
  } from './control.js';
  import { DEFAULT_NETWORK_COLORS } from '@/builder/model.js';
  import { rowTarget } from '@/builder/roving.js';

  // Names for the suggested colors, which a screen reader reads with each.
  const NAMES = {
    '#2f6fbf': 'Blue',
    '#b05c17': 'Orange',
    '#1f7a5a': 'Green',
    '#7a3fb0': 'Purple',
    '#a3273f': 'Red',
    '#0f6f77': 'Teal',
    '#6b6f18': 'Olive',
    '#8a4b8f': 'Plum',
  };

  const SWATCHES = DEFAULT_NETWORK_COLORS.map((value) => ({
    value,
    name: NAMES[value] || value,
  }));

  // The swatches are four to a row.
  const COLUMNS = 4;

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);

  // An emptied field removes its value, as other text fields do.
  const {
    control,
    ids: fieldIds,
    errorText,
    warnings,
    inputAttrs,
    locked,
    changed,
    onChange,
  } = useInspectorControl(input, (target) => target.value.trim() || undefined);

  const ids = computed(() => ({
    ...fieldIds.value,
    popup: `${fieldIds.value.input}-picker`,
    current: `${fieldIds.value.input}-current`,
  }));

  const { text, onInput, sync } = useFieldText(control);
  const drawnBy = useInspectorDrawnColor();

  const root = ref();
  const trigger = ref();
  const field = ref();
  const popup = ref();
  const open = ref(false);
  // The swatch the toolbar's Tab stop is on.
  const roving = ref(0);

  // The color the field holds, as typed, and as the canvas would draw it.
  const current = computed(() =>
    String(text.value ?? '')
      .trim()
      .toLowerCase(),
  );

  function drawColor(value) {
    return unref(drawnBy)(value);
  }

  const drawn = computed(() => drawColor(current.value));

  const currentText = computed(() => {
    if (!current.value) {
      return 'No color';
    }

    const name = SWATCHES.find(
      (swatch) => swatch.value === current.value,
    )?.name;

    return name ? `${name}, ${current.value}` : current.value;
  });

  // The native color input takes only #rrggbb.
  const customValue = computed(() => {
    const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/.exec(current.value)?.[1];

    if (!hex) {
      return '#000000';
    }

    return hex.length === 3
      ? `#${[...hex].map((digit) => digit + digit).join('')}`
      : `#${hex}`;
  });

  function lowerFirst(label) {
    return /^[A-Z][a-z]/.test(label)
      ? label[0].toLowerCase() + label.slice(1)
      : label;
  }

  // Committed, the field shows its value as the form reads it.
  function commit(event) {
    onChange(event);
    sync();
  }

  function onPointerDown(event) {
    if (!root.value?.contains(event.target)) {
      close(false);
    }
  }

  watch(open, (now) => {
    const method = now ? 'addEventListener' : 'removeEventListener';

    document[method]('pointerdown', onPointerDown, true);
  });

  onBeforeUnmount(() => {
    document.removeEventListener('pointerdown', onPointerDown, true);
  });

  async function toggle() {
    if (open.value) {
      close(false);

      return;
    }

    const index = SWATCHES.findIndex(
      (swatch) => swatch.value === current.value,
    );

    roving.value = Math.max(index, 0);
    open.value = true;
    await nextTick();
    // All of it, not only the swatch that takes focus.
    popup.value?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    popup.value
      ?.querySelectorAll('.inspector-color__option')
      [roving.value]?.focus({ preventScroll: true });
  }

  // Focus goes back to the swatch button when it was in the picker, or is
  // asked for (Escape, a choice).
  async function close(refocus) {
    if (!open.value) {
      return;
    }

    const inside = popup.value?.contains(document.activeElement);

    open.value = false;

    if (refocus || inside) {
      await nextTick();
      trigger.value?.focus();
    }
  }

  // Focus leaving the picker and its button closes it. Focus leaving the
  // window, for the native color dialog, does not.
  function onFocusOut(event) {
    const next = event.relatedTarget;

    if (open.value && next && !root.value?.contains(next)) {
      close(false);
    }
  }

  // A choice goes into the text field as typing it would, and commits there,
  // so the Inspector sees an edit of the field.
  function choose(value) {
    const element = field.value;

    if (element && element.value !== value) {
      element.value = value;
      element.dispatchEvent(new Event('change', { bubbles: true }));
    }

    close(true);
  }

  function onPaletteKey(event) {
    const count = SWATCHES.length;
    let next = rowTarget(event.key, roving.value, count);

    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      const step = event.key === 'ArrowDown' ? COLUMNS : -COLUMNS;

      next = Math.min(Math.max(roving.value + step, 0), count - 1);
    }

    if (next === undefined) {
      return;
    }

    event.preventDefault();
    roving.value = next;
    popup.value?.querySelectorAll('.inspector-color__option')[next]?.focus();
  }
</script>
