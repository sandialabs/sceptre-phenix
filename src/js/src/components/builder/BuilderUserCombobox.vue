<!--
  The field that names a user to share with: an editable combobox with list
  autocomplete (WAI-ARIA APG) over the users offered (users), less those in
  exclude. Typing filters the list, and its button opens it whole. Until the
  users are known (users is null), a username can still be typed.

  The field holds what was typed, or the label of the user chosen from the
  list ("Name (username)", see combobox.js), as modelValue. Choosing a user
  emits choose with their username. Enter with no suggestion in view emits
  submit, for the form to take the name. A status region of its own says
  how many users match the typed text, after typing pauses.

  The label, the hints and the error message are the form's: describedby
  names them, and invalid marks the field. Its elements are named for id:
  <id>, <id>-toggle, <id>-options and <id>-option-<username> as test ids.
-->
<template>
  <div class="builder-user-combobox">
    <input
      :id="id"
      ref="field"
      :value="modelValue"
      type="text"
      class="builder-user-combobox__field"
      role="combobox"
      autocomplete="off"
      autocapitalize="none"
      spellcheck="false"
      aria-autocomplete="list"
      :aria-expanded="String(listShown)"
      :aria-controls="`${id}-options`"
      :aria-activedescendant="
        listShown && active >= 0 ? `${id}-option-${active}` : undefined
      "
      :disabled="disabled"
      :aria-invalid="invalid ? 'true' : undefined"
      :aria-describedby="describedby || undefined"
      :data-testid="id"
      @input="onInput"
      @keydown="onKeydown"
      @click="openList"
      @blur="closeList" />
    <!-- Out of the tab order, as in the APG example: the keys open the list
         from the field. -->
    <button
      type="button"
      tabindex="-1"
      class="builder-user-combobox__toggle"
      aria-label="Users"
      :aria-expanded="String(listShown)"
      :aria-controls="`${id}-options`"
      :disabled="disabled"
      :data-testid="`${id}-toggle`"
      @mousedown.prevent
      @click="toggleList">
      <builder-icon name="chevron-down" :size="14" />
    </button>
    <ul
      v-show="listShown"
      :id="`${id}-options`"
      ref="listEl"
      class="builder-user-combobox__options"
      role="listbox"
      aria-label="Users"
      :data-testid="`${id}-options`">
      <li
        v-for="(option, index) in options"
        :id="`${id}-option-${index}`"
        :key="option.username"
        class="builder-user-combobox__option"
        role="option"
        :aria-selected="index === active ? 'true' : 'false'"
        :data-testid="`${id}-option-${option.username}`"
        @mousedown.prevent
        @pointermove="active = index"
        @click="choose(index)">
        {{ userLabel(option) }}
      </li>
    </ul>
    <p
      class="builder-visually-hidden"
      role="status"
      :data-testid="`${id}-matches`">
      <span v-if="matches.text" :key="matches.key">{{ matches.text }}</span>
    </p>
  </div>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import { useMessage } from './dialogs/message.js';

  import { COUNT_DELAY_MS } from '@/builder/announce.js';
  import { comboboxKey, userLabel, userOptions } from '@/builder/combobox.js';
  import { matchesMessage } from '@/builder/share.js';

  const props = defineProps({
    // The field's id, which names its other elements too.
    id: { type: String, required: true },
    // The field's text.
    modelValue: { type: String, default: '' },
    // The users offered, [{ username, name }], or null while they are not
    // known.
    users: { type: Array, default: null },
    // The usernames left out of the list: anyone listed already.
    exclude: { type: Array, default: () => [] },
    disabled: { type: Boolean, default: false },
    invalid: { type: Boolean, default: false },
    // The ids of the field's hints and error message.
    describedby: { type: String, default: '' },
  });

  const emit = defineEmits(['update:modelValue', 'choose', 'submit']);

  const field = ref(null);
  const listEl = ref(null);
  const listOpen = ref(false);
  const active = ref(-1);
  // The user last chosen from the list, until the user types in the field.
  // Until then, the field shows their label, which names them.
  const chosen = ref(null);
  const matches = useMessage();
  let matchTimer = null;

  // What the list filters on: nothing while the field shows the user
  // chosen, so the whole list opens again.
  const query = computed(() =>
    chosen.value && props.modelValue === userLabel(chosen.value)
      ? ''
      : props.modelValue,
  );
  const options = computed(() =>
    userOptions(props.users, query.value, { exclude: props.exclude }),
  );
  const listShown = computed(() => listOpen.value && options.value.length > 0);

  // A count still to come would describe a list no longer shown.
  function closeList() {
    listOpen.value = false;
    active.value = -1;
    clearTimeout(matchTimer);
  }

  function openList() {
    listOpen.value = true;
  }

  function toggleList() {
    if (listShown.value) {
      closeList();
    } else {
      openList();
    }

    field.value?.focus();
  }

  function choose(index) {
    const option = options.value[index];

    if (option) {
      chosen.value = option;
      emit('update:modelValue', userLabel(option));
      emit('choose', option.username);
    }

    closeList();
  }

  function onInput(event) {
    const text = event.target.value;

    emit('update:modelValue', text);
    chosen.value = null;
    active.value = -1;
    listOpen.value = true;

    clearTimeout(matchTimer);

    if (props.users && text.trim()) {
      matchTimer = setTimeout(() => {
        matches.set(matchesMessage(options.value.length));
      }, COUNT_DELAY_MS);
    }
  }

  async function onKeydown(event) {
    const key = comboboxKey(event, {
      expanded: listShown.value,
      active: active.value,
      count: options.value.length,
    });

    if (!key) {
      return;
    }

    if (key.prevent) {
      event.preventDefault();
    }

    switch (key.action) {
      case 'open':
        listOpen.value = true;
        break;
      case 'move':
        listOpen.value = true;
        active.value = key.index;
        await nextTick();
        listEl.value?.children[key.index]?.scrollIntoView?.({
          block: 'nearest',
        });
        break;
      case 'choose':
        choose(key.index);
        break;
      case 'close':
        // Escape closes the list, not the dialog.
        if (key.prevent) {
          event.stopPropagation();
        }
        closeList();
        break;
      case 'add':
        closeList();
        emit('submit');
        break;
      default:
        break;
    }
  }

  onBeforeUnmount(() => {
    clearTimeout(matchTimer);
  });

  defineExpose({
    focus() {
      field.value?.focus();
    },
    closeList,
  });
</script>

<style scoped>
  .builder-user-combobox {
    position: relative;
  }

  /* The field as a form field of the Builder's, with room for the button
     that opens the list inside its end. It outranks the form's own rule
     for its fields (.builder-field input in builder.css). */
  .builder-user-combobox .builder-user-combobox__field[role='combobox'] {
    width: 100%;
    padding: 0.35rem 0.45rem;
    padding-inline-end: 2.25rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
  }

  .builder-user-combobox
    .builder-user-combobox__field[role='combobox'][aria-invalid='true'] {
    border-color: var(--bx-danger);
    box-shadow: inset 0 0 0 1px var(--bx-danger);
  }

  .builder-user-combobox__toggle {
    position: absolute;
    top: 50%;
    right: 2px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: 0;
    border-radius: var(--bx-radius);
    background: none;
    color: var(--bx-text-muted);
    cursor: pointer;
    transform: translateY(-50%);
  }

  .builder-user-combobox__toggle:hover:not(:disabled) {
    background: var(--bx-bg-alt);
    color: var(--bx-text);
  }

  .builder-user-combobox__toggle:disabled {
    cursor: not-allowed;
  }

  .builder-user-combobox__toggle[aria-expanded='true'] .builder-icon {
    transform: rotate(180deg);
  }

  .builder-user-combobox__options {
    position: absolute;
    z-index: 1;
    top: calc(100% + 2px);
    right: 0;
    left: 0;
    max-height: 12rem;
    margin: 0;
    padding: 0.2rem 0;
    overflow-y: auto;
    list-style: none;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    box-shadow: var(--bx-shadow);
  }

  .builder-user-combobox__option {
    min-height: 24px;
    overflow-wrap: anywhere;
    padding: 0.25rem 0.5rem;
    border-left: 3px solid transparent;
    cursor: pointer;
  }

  .builder-user-combobox__option[aria-selected='true'] {
    border-left-color: var(--bx-accent);
    background: var(--bx-selected-bg);
  }

  @media (pointer: coarse) {
    .builder-user-combobox .builder-user-combobox__field[role='combobox'] {
      min-height: 44px;
      padding-inline-end: 3rem;
    }

    .builder-user-combobox__toggle {
      width: 40px;
      height: 40px;
    }

    .builder-user-combobox__option {
      min-height: 44px;
    }
  }

  /* Forced colors draw transparent borders, so only the active option keeps
     a bar that shows. */
  @media (forced-colors: active) {
    .builder-user-combobox__option {
      border-left-color: Canvas;
    }

    .builder-user-combobox__option[aria-selected='true'] {
      forced-color-adjust: none;
      border-left-color: HighlightText;
      background: Highlight;
      color: HighlightText;
    }
  }
</style>
