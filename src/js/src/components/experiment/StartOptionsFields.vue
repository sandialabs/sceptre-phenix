<!--
The options of an experiment start in the start and restart dialogs: a dry run
(only when dryRun is set), minimega errors as warnings, the scheduled runs of
the apps and a VLAN range. v-model is the form (see START_DEFAULTS).
-->
<template>
  <fieldset class="options">
    <legend class="label">{{ legend }}</legend>
    <b-checkbox
      v-if="dryRun"
      :model-value="modelValue.dryRun"
      @update:model-value="set('dryRun', $event)">
      Dry run (do not call minimega)
    </b-checkbox>
    <b-checkbox
      :model-value="modelValue.treatMinimegaErrorsAsWarnings"
      @update:model-value="set('treatMinimegaErrorsAsWarnings', $event)">
      Treat minimega errors as warnings
    </b-checkbox>
    <b-checkbox
      :model-value="modelValue.honorRunPeriodically"
      :disabled="dryRun && modelValue.dryRun"
      @update:model-value="set('honorRunPeriodically', $event)">
      Run the apps' running stage on its schedule
    </b-checkbox>
  </fieldset>
  <fieldset class="options">
    <legend class="label">VLAN range</legend>
    <p :id="`${uid}-vlan-hint`" class="help vlan-hint">
      Leave a field empty to use the range of the experiment.
    </p>
    <div class="vlan-fields">
      <div v-for="field in vlanFields" :key="field.key" class="field">
        <label class="label" :for="`${uid}-${field.key}`">
          {{ field.label }}
        </label>
        <div class="control">
          <input
            :id="`${uid}-${field.key}`"
            :value="modelValue[field.key]"
            class="input"
            :class="{ 'is-danger': errors[field.key] }"
            type="number"
            min="1"
            max="4094"
            step="1"
            :aria-invalid="errors[field.key] ? 'true' : 'false'"
            :aria-describedby="`${uid}-${field.key}-error ${uid}-vlan-hint`"
            @input="set(field.key, $event.target.value)" />
        </div>
        <p
          :id="`${uid}-${field.key}-error`"
          class="help is-danger"
          aria-live="polite">
          {{ errors[field.key] }}
        </p>
      </div>
    </div>
  </fieldset>
</template>

<script>
  import { startOptionErrors } from '@/utils/startOptions.js';

  let fields = 0;

  export default {
    name: 'StartOptionsFields',
    props: {
      modelValue: { type: Object, required: true },
      legend: { type: String, default: 'Options' },
      // whether to offer the dry run
      dryRun: { type: Boolean, default: false },
    },
    emits: ['update:modelValue'],
    data() {
      return {
        uid: `start-options-${++fields}`,
        vlanFields: [
          { key: 'vlanMin', label: 'VLAN range minimum' },
          { key: 'vlanMax', label: 'VLAN range maximum' },
        ],
      };
    },
    computed: {
      errors() {
        return startOptionErrors(this.modelValue);
      },
    },
    methods: {
      set(key, value) {
        this.$emit('update:modelValue', { ...this.modelValue, [key]: value });
      },
    },
  };
</script>

<style scoped>
  .vlan-hint {
    margin-top: 0;
  }

  .vlan-fields {
    display: flex;
    align-items: flex-end;
    gap: 1rem;
  }

  .vlan-fields .field {
    flex: 1;
    margin-bottom: 0;
  }
</style>
