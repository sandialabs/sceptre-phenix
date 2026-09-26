<!--
  A text field that suggests values as the user types: a drive's image, from
  the disk images the server has (see SPEC_SUGGESTIONS in schema.js and
  INSPECTOR_SUGGESTIONS in control.js).

  It is an editable combobox with list autocomplete (WAI-ARIA APG): typing
  filters a listbox of suggestions under the field, Down and Up Arrow move
  through them (Alt+Down Arrow opens the list without moving), Enter or a
  click takes one, and Escape closes the list. Focus stays in the field, which
  names the suggestion in view with aria-activedescendant. Any other text can
  still be typed, and commits on change like any text field. With nothing to
  suggest (the images are unknown, or the field is locked) it is a plain text
  field.
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="control"
    :ids="ids"
    :error-text="errorText"
    :warnings="warnings"
    :changed="changed">
    <div class="inspector-combobox">
      <input
        v-bind="attrs"
        ref="field"
        type="text"
        class="input"
        autocomplete="off"
        :value="text"
        @input="onInput"
        @change="onChange"
        @keydown="onKeydown"
        @blur="close" />
      <ul
        v-if="suggesting"
        v-show="expanded"
        :id="ids.listbox"
        ref="listbox"
        class="inspector-combobox__listbox"
        role="listbox"
        :aria-label="`Suggestions for ${control.label || 'the value'}`"
        data-testid="inspector-suggestions">
        <li
          v-for="(option, index) in matches"
          :id="`${ids.listbox}-${index}`"
          :key="option"
          class="inspector-combobox__option"
          role="option"
          :aria-selected="index === active ? 'true' : 'false'"
          @mousedown.prevent
          @pointermove="active = index"
          @click="choose(option)">
          {{ option }}
        </li>
      </ul>
    </div>
  </inspector-field>
</template>

<script setup>
  import { computed, nextTick, ref, unref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import InspectorField from './InspectorField.vue';
  import {
    suggestionsFor,
    useFieldText,
    useInspectorControl,
    useInspectorSuggestions,
  } from './control.js';
  import { SUGGESTIONS_KEYWORD } from '@/builder/schema.js';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);
  const suggestions = useInspectorSuggestions();

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
  } = useInspectorControl(input, (target) => target.value || undefined);

  const ids = computed(() => ({
    ...fieldIds.value,
    listbox: `${fieldIds.value.input}-suggestions`,
  }));

  const field = ref();
  const listbox = ref();
  // The text in the field, as typed (see useFieldText); the list filters on
  // it. Opening the list re-renders the field.
  const { text, onInput: typed } = useFieldText(control);
  const open = ref(false);
  // The suggestion in view, or -1 for none.
  const active = ref(-1);

  const values = computed(() => {
    const kind = control.value.schema?.[SUGGESTIONS_KEYWORD];
    const list = unref(suggestions)?.[kind];

    return Array.isArray(list) ? list : [];
  });

  const suggesting = computed(
    () => values.value.length > 0 && control.value.enabled && !locked.value,
  );

  const matches = computed(() => suggestionsFor(values.value, text.value));
  const expanded = computed(
    () => suggesting.value && open.value && matches.value.length > 0,
  );

  const attrs = computed(() =>
    suggesting.value
      ? {
          ...inputAttrs.value,
          role: 'combobox',
          'aria-autocomplete': 'list',
          'aria-expanded': expanded.value ? 'true' : 'false',
          'aria-controls': ids.value.listbox,
          'aria-activedescendant':
            expanded.value && active.value >= 0
              ? `${ids.value.listbox}-${active.value}`
              : undefined,
        }
      : inputAttrs.value,
  );

  watch(matches, () => {
    active.value = -1;
  });

  watch(active, async (index) => {
    await nextTick();
    listbox.value?.children[index]?.scrollIntoView?.({ block: 'nearest' });
  });

  function onInput(event) {
    typed(event);
    open.value = true;
  }

  function close() {
    open.value = false;
    active.value = -1;
  }

  // Down Arrow from the field, or past the last suggestion, goes to the
  // first; Up Arrow to the last.
  function move(step) {
    const count = matches.value.length;

    if (!expanded.value || active.value < 0) {
      open.value = true;
      active.value = step > 0 ? 0 : count - 1;
    } else {
      active.value = (active.value + step + count) % count;
    }
  }

  // Takes a suggestion as if it had been typed: the Inspector sees the
  // field's input and change, so Apply appears and the value commits.
  function choose(option) {
    const element = field.value;

    if (element) {
      element.value = option;
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new Event('change', { bubbles: true }));
    }

    close();
  }

  function onKeydown(event) {
    if (!suggesting.value || event.ctrlKey || event.metaKey) {
      return;
    }

    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();

        if (event.altKey) {
          open.value = true;
        } else if (matches.value.length) {
          move(1);
        }

        break;
      case 'ArrowUp':
        if (!event.altKey && matches.value.length) {
          event.preventDefault();
          move(-1);
        }

        break;
      case 'Enter':
        // Enter on a suggestion takes it; otherwise it submits the form,
        // which applies the edits as in any other field.
        if (expanded.value && active.value >= 0) {
          event.preventDefault();
          choose(matches.value[active.value]);
        }

        break;
      case 'Escape':
        if (expanded.value) {
          event.preventDefault();
          event.stopPropagation();
          close();
        }

        break;
      case 'Tab':
        close();
        break;
      default:
        break;
    }
  }
</script>
