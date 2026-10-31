<!--
  A layer's "#rrggbb" color field with a native picker. Text commits on blur or
  Enter, so typing is one change.
-->
<template>
  <div class="builder-layer-color">
    <div class="builder-layer-color__row">
      <input
        type="color"
        class="builder-layer-color__picker"
        :class="{ 'is-none': !color }"
        :value="color || '#808080'"
        :disabled="readOnly"
        :aria-label="`Choose ${lowerFirst(label)}`"
        @change="choose($event.target.value)" />
      <input
        :id="id"
        type="text"
        class="builder-layer-color__text"
        :value="typed ?? color"
        placeholder="No color"
        autocomplete="off"
        spellcheck="false"
        maxlength="7"
        :readonly="readOnly"
        :aria-label="label"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="error ? `${id}-error` : undefined"
        data-testid="layer-color"
        @input="typed = $event.target.value"
        @change="commit($event.target.value)" />
      <button
        v-if="color && !readOnly"
        type="button"
        class="builder-button builder-layer-color__remove"
        :aria-label="`Remove ${lowerFirst(label)}`"
        @click="choose('')">
        <builder-icon name="close" :size="12" />
      </button>
    </div>
    <p
      v-if="error"
      :id="`${id}-error`"
      class="builder-dialog__error builder-layer-color__error"
      data-testid="layer-color-error">
      {{ error }}
    </p>
  </div>
</template>

<script setup>
  import { ref, watch } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';

  import { layerColorProblem } from '@/builder/model.js';
  import { lowerFirst } from '@/builder/text.js';

  const props = defineProps({
    // "#rrggbb", or '' for none.
    color: { type: String, default: '' },
    // The field's name: "Color of layer 3".
    label: { type: String, required: true },
    id: { type: String, required: true },
    readOnly: { type: Boolean, default: false },
  });

  const emit = defineEmits(['change', 'status']);

  // Text typed and not written yet, and why it cannot be written.
  const typed = ref(null);
  const error = ref('');

  // A color changed from outside (undo, a new copy) shows.
  watch(
    () => props.color,
    () => {
      typed.value = null;
      error.value = '';
    },
  );

  function choose(value) {
    typed.value = null;
    error.value = '';

    if (value.toLowerCase() !== props.color.toLowerCase()) {
      emit('change', value.toLowerCase());
    }
  }

  function commit(text) {
    const value = String(text).trim();
    const problem = layerColorProblem(value);

    if (problem) {
      error.value = problem;
      emit('status', problem);

      return;
    }

    choose(value);
  }

  defineExpose({
    // Writes the text typed and not written yet, for Save and Done.
    flush() {
      if (typed.value !== null) {
        commit(typed.value);
      }
    },
    // Whether the field has an error.
    get unsettled() {
      return Boolean(error.value);
    },
  });
</script>

<style scoped>
  .builder-layer-color {
    min-width: 0;
  }

  .builder-layer-color__row {
    display: flex;
    align-items: center;
    gap: 0.3rem;
  }

  /* A swatch as tall as the text field. "No color" shows as a dashed box. */
  .builder-layer-color__picker {
    flex: none;
    width: 2.25rem;
    height: 2.25rem;
    padding: 0.15rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    cursor: pointer;
  }

  .builder-layer-color__picker.is-none {
    border-style: dashed;
  }

  .builder-layer-color__picker.is-none::-webkit-color-swatch {
    opacity: 0;
  }

  .builder-layer-color__picker.is-none::-moz-color-swatch {
    opacity: 0;
  }

  .builder-layer-color__text {
    width: 6.5rem;
    min-width: 0;
    padding: 0.3rem 0.4rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    color: var(--bx-text);
    font-family: inherit;
  }

  .builder-layer-color__error {
    margin: 0.15rem 0 0;
    font-size: 0.85em;
  }
</style>
