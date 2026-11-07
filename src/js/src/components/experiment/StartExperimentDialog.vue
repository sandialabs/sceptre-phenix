<!--
The confirm dialog of an experiment start, with the options of the start: a
dry run, minimega errors as warnings, the scheduled runs of the apps and a
VLAN range. It emits start with the request body, or cancel.
-->
<template>
  <form class="modal-card" novalidate @submit.prevent="submit">
    <header class="modal-card-head">
      <h2 class="modal-card-title">{{ title }}</h2>
    </header>
    <section class="modal-card-body is-flex">
      <div class="media">
        <div class="media-left">
          <b-icon icon="check-circle" type="is-success" size="is-large" />
        </div>
        <div class="media-content">
          <p class="start-message">{{ message }}</p>
          <p v-if="note" class="mt-3 has-text-grey-lighter">{{ note }}</p>
          <fieldset class="start-options">
            <legend class="label">Options</legend>
            <b-checkbox v-model="form.dryRun">
              Dry run (do not call minimega)
            </b-checkbox>
            <b-checkbox v-model="form.treatMinimegaErrorsAsWarnings">
              Treat minimega errors as warnings
            </b-checkbox>
            <b-checkbox
              v-model="form.honorRunPeriodically"
              :disabled="form.dryRun">
              Run the apps' running stage on its schedule
            </b-checkbox>
          </fieldset>
          <fieldset class="start-options">
            <legend class="label">VLAN range</legend>
            <p :id="`${uid}-vlan-hint`" class="help start-hint">
              Leave a field empty to use the range of the experiment.
            </p>
            <div class="start-vlan-fields">
              <div v-for="field in vlanFields" :key="field.key" class="field">
                <label class="label" :for="`${uid}-${field.key}`">
                  {{ field.label }}
                </label>
                <div class="control">
                  <input
                    :id="`${uid}-${field.key}`"
                    v-model="form[field.key]"
                    class="input"
                    :class="{ 'is-danger': errors[field.key] }"
                    type="number"
                    min="1"
                    max="4094"
                    step="1"
                    :aria-invalid="errors[field.key] ? 'true' : 'false'"
                    :aria-describedby="`${uid}-${field.key}-error ${uid}-vlan-hint`" />
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
        </div>
      </div>
    </section>
    <footer class="modal-card-foot">
      <b-button @click="cancel">Cancel</b-button>
      <b-button
        ref="start"
        type="is-success"
        native-type="submit"
        :disabled="!valid">
        Start
      </b-button>
    </footer>
  </form>
</template>

<script>
  import {
    START_DEFAULTS,
    startOptionErrors,
    startRequestBody,
  } from '@/utils/startOptions.js';

  let dialogs = 0;

  export default {
    name: 'StartExperimentDialog',
    props: {
      title: { type: String, required: true },
      // plain text, as are the other props
      message: { type: String, required: true },
      // which of a selection the start leaves out, and why
      note: { type: String, default: '' },
    },
    // start(body): the request body, {} for the defaults
    emits: ['close', 'start', 'cancel'],
    data() {
      return {
        uid: `start-options-${++dialogs}`,
        form: { ...START_DEFAULTS },
        vlanFields: [
          { key: 'vlanMin', label: 'VLAN range minimum' },
          { key: 'vlanMax', label: 'VLAN range maximum' },
        ],
      };
    },
    computed: {
      errors() {
        return startOptionErrors(this.form);
      },
      valid() {
        return !this.errors.vlanMin && !this.errors.vlanMax;
      },
    },
    mounted() {
      // As a confirm dialog does, focus Start, so Enter starts. The modal
      // focuses itself once shown, so this waits for that.
      this.focusTimer = setTimeout(() => this.$refs.start?.$el?.focus());
    },
    beforeUnmount() {
      clearTimeout(this.focusTimer);
    },
    methods: {
      submit() {
        if (!this.valid) return;
        this.$emit('start', startRequestBody(this.form));
        this.$emit('close');
      },
      cancel() {
        this.$emit('cancel');
        this.$emit('close');
      },
    },
  };
</script>

<style scoped>
  .start-options {
    margin-top: 0.75rem;
  }

  .start-options .label {
    margin-bottom: 0.25rem;
  }

  .start-options .b-checkbox.checkbox {
    display: flex;
    margin: 0.25rem 0;
  }

  .start-hint {
    margin-top: 0;
  }

  .start-vlan-fields {
    display: flex;
    align-items: flex-end;
    gap: 1rem;
  }

  .start-vlan-fields .field {
    flex: 1;
    margin-bottom: 0;
  }
</style>
