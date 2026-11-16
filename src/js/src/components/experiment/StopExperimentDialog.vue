<!--
The confirm dialog of an experiment stop, with the options of the stop: keep
the injection snapshots, and force the stop. Each opens unchecked. It emits
stop with the options that are set, or cancel.
-->
<template>
  <experiment-action-dialog
    :title="title"
    :message="message"
    :note="note"
    icon="exclamation-circle"
    confirm-text="Stop"
    confirm-type="is-danger"
    @confirm="submit"
    @cancel="cancel">
    <stop-options-fields v-model="form" />
  </experiment-action-dialog>
</template>

<script>
  import ExperimentActionDialog from '@/components/experiment/ExperimentActionDialog.vue';
  import StopOptionsFields from '@/components/experiment/StopOptionsFields.vue';
  import { STOP_DEFAULTS, stopRequestBody } from '@/utils/stopOptions.js';

  export default {
    name: 'StopExperimentDialog',
    components: { ExperimentActionDialog, StopOptionsFields },
    props: {
      title: { type: String, required: true },
      // plain text, as are the other props
      message: { type: String, required: true },
      // which of a selection the stop leaves out, and why
      note: { type: String, default: '' },
    },
    // stop(options): { force, keepInjectionSnapshots } when set, {} for none
    emits: ['close', 'stop', 'cancel'],
    data() {
      return { form: { ...STOP_DEFAULTS } };
    },
    methods: {
      submit() {
        this.$emit('stop', stopRequestBody(this.form));
        this.$emit('close');
      },
      cancel() {
        this.$emit('cancel');
        this.$emit('close');
      },
    },
  };
</script>
