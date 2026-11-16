<!--
The confirm dialog of an experiment start, with the options of the start: a
dry run, minimega errors as warnings, the scheduled runs of the apps and a
VLAN range. It emits start with the request body, or cancel.
-->
<template>
  <experiment-action-dialog
    :title="title"
    :message="message"
    :note="note"
    icon="check-circle"
    confirm-text="Start"
    confirm-type="is-success"
    :valid="valid"
    @confirm="submit"
    @cancel="cancel">
    <start-options-fields v-model="form" dry-run />
  </experiment-action-dialog>
</template>

<script>
  import ExperimentActionDialog from '@/components/experiment/ExperimentActionDialog.vue';
  import StartOptionsFields from '@/components/experiment/StartOptionsFields.vue';
  import {
    START_DEFAULTS,
    startOptionsValid,
    startRequestBody,
  } from '@/utils/startOptions.js';

  export default {
    name: 'StartExperimentDialog',
    components: { ExperimentActionDialog, StartOptionsFields },
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
      return { form: { ...START_DEFAULTS } };
    },
    computed: {
      valid() {
        return startOptionsValid(this.form);
      },
    },
    methods: {
      submit() {
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
