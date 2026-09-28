<!--
  Picker for a `oneOf` field (an interface's kind), in place of the vanilla
  JSON Forms renderer. A number phenix also takes as text (Memory, VCPUs) is
  a number field instead (see InspectorInputControl).

  Options are named by the branch titles schema.js adds, as option text; the
  value field of a primitive branch is labelled with the field and the kind
  ("Memory (megabytes)"). Switching kinds clears the value, so a filled field
  asks first, in the Builder's confirmation dialog (BuilderConfirm): its
  first focus is the choice that keeps the value, Escape or a click outside
  it keeps the value too, its buttons cannot submit the Inspector form, and
  focus returns to the picker. A kind stepped to with keys, which change a
  closed select at every step on Windows and Linux, is held until the
  choice is made (see heldCommit): focus leaves the picker, or a key that
  is no step, such as Enter. One chosen with the pointer asks at once. A
  locked field (see useInspectorLocked) names its kind in a read-only text
  input instead.
-->
<template>
  <div v-if="control.visible" class="one-of">
    <inspector-field
      :control="control"
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :changed="changed">
      <input
        v-if="locked"
        v-bind="inputAttrs"
        type="text"
        class="input"
        :value="branches[selectedIndex]?.label ?? 'Not set'" />
      <select
        v-else
        ref="picker"
        v-bind="inputAttrs"
        class="select"
        :value="selectedIndex ?? ''"
        @change="onSelect"
        @keydown="onKey"
        @pointerdown="choice.point()"
        @blur="flushSoon">
        <option v-if="selectedIndex === undefined" value="" disabled>
          Not set
        </option>
        <option
          v-for="branch in branches"
          :key="branch.index"
          :value="branch.index"
          class="option">
          {{ branch.label }}
        </option>
      </select>
    </inspector-field>

    <dispatch-renderer
      v-if="branchUiSchema"
      :schema="branches[selectedIndex].schema"
      :uischema="branchUiSchema"
      :path="control.path"
      :renderers="control.renderers"
      :cells="control.cells"
      :enabled="control.enabled" />

    <builder-confirm
      v-if="pending !== undefined"
      :id="`${ids.input}-switch`"
      :title="`Switch ${fieldName} to ${pendingLabel}?`"
      :message="`Switching clears the ${fieldName} values entered so far.`"
      confirm-label="Switch and clear"
      cancel-label="Keep current value"
      @cancel="close"
      @confirm="confirm" />
  </div>
</template>

<script setup>
  import { computed, nextTick, ref, watch } from 'vue';
  import {
    createCombinatorRenderInfos,
    createDefaultValue,
  } from '@jsonforms/core';
  import {
    DispatchRenderer,
    rendererProps,
    useAjv,
    useJsonFormsOneOfControl,
  } from '@jsonforms/vue';

  import { closestBranch } from '@/builder/form-validator.js';
  import BuilderConfirm from '../BuilderConfirm.vue';
  import InspectorField from './InspectorField.vue';
  import {
    labelWholeValue,
    useInspectorControl,
    useInspectorResets,
  } from './control.js';
  import { heldCommit, keyEffect } from './heldCommit.js';

  const props = defineProps(rendererProps());
  const {
    control,
    ids,
    errorText,
    warnings,
    inputAttrs,
    locked,
    changed,
    handleChange,
  } = useInspectorControl(useJsonFormsOneOfControl(props));
  const ajv = useAjv(true);
  const resets = useInspectorResets();

  // The kind a switch waits to be confirmed for, or undefined.
  const pending = ref(undefined);
  const picker = ref();

  const fieldName = computed(() => control.value.label || 'this field');

  const branches = computed(() =>
    createCombinatorRenderInfos(
      control.value.schema.oneOf || [],
      control.value.rootSchema,
      'oneOf',
      control.value.uischema,
      control.value.path,
      control.value.uischemas,
    )
      .filter((info) => info.uischema)
      .map((info, index) => ({ ...info, index })),
  );

  // The kind the data has. Data no branch accepts yet (a new interface
  // without a VLAN) shows the branch it comes closest to, so its missing
  // fields can be filled in; the vanilla renderer showed no fields at all.
  function kindOfData() {
    return (
      control.value.indexOfFittingSchema ??
      closestBranch(
        ajv,
        branches.value.map((branch) => branch.schema),
        control.value.data,
      )
    );
  }

  const selectedIndex = ref(kindOfData());

  // The picker follows the data when it changes: Cancel or undo replaces it,
  // and a list reuses this picker for the item that moves into its place.
  // A kind chosen for a field with no value stays chosen: emptying the field
  // keeps it, and so does a reset that leaves this field's data as it was
  // (after Apply, Move or Add connection point). Removing the field there
  // took focus with it when Enter in the empty field applied the form. Only
  // a reset that replaces this field's data with no value goes back to
  // "Not set".
  // Post flush: the replaced data reaches JSON Forms after the reset count.
  watch(
    [() => control.value.data, resets],
    ([data, count], [previous, previousCount]) => {
      const index = kindOfData();
      const replaced =
        count !== previousCount &&
        JSON.stringify(data) !== JSON.stringify(previous);

      if (index !== undefined || replaced) {
        selectedIndex.value = index;
      }
    },
    { flush: 'post' },
  );

  const pendingLabel = computed(
    () => branches.value[pending.value]?.label || '',
  );

  function lowerFirst(text) {
    return /^[A-Z][a-z]/.test(text)
      ? text[0].toLowerCase() + text.slice(1)
      : text;
  }

  const branchUiSchema = computed(() => {
    const branch = branches.value[selectedIndex.value];

    return branch
      ? labelWholeValue(
          branch.uischema,
          `${fieldName.value} (${lowerFirst(branch.label)})`,
        )
      : undefined;
  });

  // Switching a field with a value asks first, once the kind is chosen.
  const choice = heldCommit(ask);

  function onSelect(event) {
    // "Not set" is not a kind: Number('') would read it as the first one.
    if (event.target.value === '') {
      return;
    }

    const index = Number(event.target.value);

    // With no value there is nothing to lose, so the kind switches at once.
    if (!control.value.enabled || control.value.data === undefined) {
      selectedIndex.value = index;

      return;
    }

    choice.change(index);
  }

  function onKey(event) {
    const effect = keyEffect(event);

    if (effect === 'hold') {
      choice.key();
    } else if (effect === 'flush') {
      flushSoon();
    }
  }

  // Once the key is done: the dialog opened while Enter is still down took
  // its keypress on its first button, and Tab moved focus on from it.
  function flushSoon() {
    setTimeout(() => choice.flush());
  }

  // Asks to switch to the kind chosen, keeping the current one shown until
  // the switch is confirmed. Returns whether it asked.
  function ask(index) {
    if (picker.value) {
      picker.value.value = String(selectedIndex.value ?? '');
    }

    if (index === selectedIndex.value || pending.value !== undefined) {
      return false;
    }

    pending.value = index;

    return true;
  }

  // The dialog returns focus to the control that had it as it opened, which
  // a kind chosen with the pointer may have left elsewhere.
  async function close() {
    pending.value = undefined;
    await nextTick();
    picker.value?.focus();
  }

  function confirm() {
    const branch = branches.value[pending.value];

    if (branch) {
      handleChange(
        control.value.path,
        createDefaultValue(branch.schema, control.value.rootSchema),
      );
      selectedIndex.value = pending.value;
    }

    close();
  }
</script>
