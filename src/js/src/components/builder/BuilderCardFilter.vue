<!-- The filter field above a tab's cards (see cardFilter.js), with its label
     above it and how many cards show below it. Escape clears it. -->
<template>
  <div class="builder-field builder-card-filter">
    <label :for="id">{{ label }}</label>
    <input
      :id="id"
      :value="modelValue"
      class="builder-input"
      type="search"
      autocomplete="off"
      spellcheck="false"
      :aria-describedby="`${id}-count`"
      :data-testid="id"
      @input="change($event.target.value)"
      @keydown.enter.prevent
      @keydown.esc="clear" />
    <span
      :id="`${id}-count`"
      class="builder-card-filter__hint"
      :data-testid="`${id}-count`">
      {{ hint }}
    </span>
  </div>
</template>

<script setup>
  import { nextTick, onBeforeUnmount } from 'vue';

  const props = defineProps({
    // The field's id and test id. The count is <id>-count.
    id: { type: String, required: true },
    label: { type: String, required: true },
    modelValue: { type: String, default: '' },
    // "3 of 12 templates shown.", or '' with no filter.
    hint: { type: String, default: '' },
    // What is said once typing stops: the hint, or the count without one.
    announcement: { type: String, default: '' },
  });

  // announce: the text to say in the live region's filter slot.
  const emit = defineEmits(['update:modelValue', 'announce']);

  // How many cards show, said 500 ms after typing stops, so the count of the
  // last text is said.
  const ANNOUNCE_MS = 500;
  let timer = null;

  function change(text) {
    emit('update:modelValue', text);
    clearTimeout(timer);
    timer = setTimeout(async () => {
      await nextTick();
      emit('announce', props.announcement || props.hint);
    }, ANNOUNCE_MS);
  }

  // Escape clears the text, and does nothing more. In an empty field it
  // goes on to the page.
  function clear(event) {
    if (props.modelValue) {
      event.preventDefault();
      event.stopPropagation();
      change('');
    }
  }

  onBeforeUnmount(() => clearTimeout(timer));
</script>

<style scoped>
  .builder-card-filter {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    max-width: 24rem;
    margin-bottom: 0.75rem;
  }

  .builder-card-filter__hint {
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }
</style>
