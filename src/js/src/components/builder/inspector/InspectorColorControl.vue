<!--
  A color field and its swatch picker. The suggested colors are the addNetwork
  colors, which keep their contrast in both themes (see colors.js).
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="control"
    :ids="ids"
    :error-text="errorText"
    :warnings="warnings"
    :note="note"
    :changed="changed">
    <div
      ref="root"
      class="inspector-color"
      @focusout="onFocusOut"
      @keydown.esc="onEscape">
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
        data-testid="inspector-color-popup">
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
  import { opaqueHex } from '@/builder/colors.js';
  import { DEFAULT_NETWORK_COLORS } from '@/builder/model.js';
  import { rowTarget } from '@/builder/roving.js';
  import { lowerFirst } from '@/builder/text.js';

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
    note,
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
    return unref(drawnBy)(value, control.value.path);
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

  // The native color input takes only #rrggbb: it shows a translucent
  // color without its alpha, and black for one it cannot show.
  const customValue = computed(() => opaqueHex(current.value) || '#000000');

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

  // Escape closes an open picker, from the text field too, and goes no
  // further. With the picker closed, it goes on to the Inspector or dialog.
  function onEscape(event) {
    if (!open.value) {
      return;
    }

    event.stopPropagation();
    event.preventDefault();
    close(false);
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
