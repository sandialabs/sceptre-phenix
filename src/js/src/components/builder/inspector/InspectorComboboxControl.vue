<!--
  A text field that suggests values: an editable combobox with list autocomplete
  (WAI-ARIA APG). Focus stays in the field.
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
        :aria-label="`Suggestions for ${control.label || 'the value'}`">
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
      <!-- How many suggestions match, said once typing pauses. -->
      <span
        class="builder-visually-hidden"
        role="status"
        data-testid="inspector-combobox-count"
        >{{ countText }}</span
      >
    </div>
  </inspector-field>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, ref, unref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import InspectorField from './InspectorField.vue';
  import {
    suggestionsFor,
    useFieldText,
    useInspectorControl,
    useInspectorSuggestions,
  } from './control.js';
  import { COUNT_DELAY_MS, count } from '@/builder/announce.js';
  import { comboboxKey } from '@/builder/combobox.js';
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
    note,
    onChange,
  } = useInspectorControl(input, (target) => target.value || undefined);

  const ids = computed(() => ({
    ...fieldIds.value,
    listbox: `${fieldIds.value.input}-suggestions`,
  }));

  const field = ref();
  const listbox = ref();
  // The text in the field, as typed (see useFieldText). The list filters on
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

  // How many suggestions match while the list is open. With no match the
  // list closes, and says so.
  const matchCount = computed(() => {
    if (!suggesting.value || !open.value) {
      return '';
    }

    const n = matches.value.length;

    return n ? count(n, 'suggestion') : 'No suggestions';
  });

  // The count waits for typing to pause (see COUNT_DELAY_MS).
  const countText = ref('');
  let countTimer = null;

  watch(matchCount, (now) => {
    clearTimeout(countTimer);
    if (!now) {
      countText.value = '';

      return;
    }
    countTimer = setTimeout(() => {
      countText.value = now;
    }, COUNT_DELAY_MS);
  });

  onBeforeUnmount(() => clearTimeout(countTimer));

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
    if (!suggesting.value) {
      return;
    }

    const key = comboboxKey(event, {
      expanded: expanded.value,
      active: active.value,
      count: matches.value.length,
    });

    // Enter with no suggestion in view submits the form, which applies the
    // edits as in any other field.
    if (!key || key.action === 'add') {
      return;
    }

    if (key.prevent) {
      event.preventDefault();
    }

    switch (key.action) {
      case 'open':
        open.value = true;
        break;
      case 'move':
        open.value = true;
        active.value = key.index;
        break;
      case 'choose':
        choose(matches.value[key.index]);
        break;
      case 'close':
        // Escape closes the list, not the Inspector's edit.
        if (key.prevent) {
          event.stopPropagation();
        }
        close();
        break;
      default:
        break;
    }
  }
</script>
