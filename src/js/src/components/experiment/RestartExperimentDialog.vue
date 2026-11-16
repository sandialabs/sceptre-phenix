<!--
The confirm dialog of an experiment restart: the options of the stop, then
those of the start without the dry run. It emits restart with the request
body, or cancel.
-->
<template>
  <experiment-action-dialog
    :title="title"
    :message="message"
    :note="note"
    icon="exclamation-triangle"
    confirm-text="Restart"
    confirm-type="is-warning"
    :valid="valid"
    @confirm="submit"
    @cancel="cancel">
    <stop-options-fields v-model="form" legend="Stop options" />
    <start-options-fields v-model="form" legend="Start options" />
  </experiment-action-dialog>
</template>

<script>
  import ExperimentActionDialog from '@/components/experiment/ExperimentActionDialog.vue';
  import StartOptionsFields from '@/components/experiment/StartOptionsFields.vue';
  import StopOptionsFields from '@/components/experiment/StopOptionsFields.vue';
  import { startOptionsValid } from '@/utils/startOptions.js';
  import { RESTART_DEFAULTS, restartRequestBody } from '@/utils/stopOptions.js';

  export default {
    name: 'RestartExperimentDialog',
    components: {
      ExperimentActionDialog,
      StartOptionsFields,
      StopOptionsFields,
    },
    props: {
      title: { type: String, required: true },
      // plain text, as are the other props
      message: { type: String, required: true },
      // which of a selection the restart leaves out, and why
      note: { type: String, default: '' },
    },
    // restart(body): the request body, {} for the defaults
    emits: ['close', 'restart', 'cancel'],
    data() {
      return { form: { ...RESTART_DEFAULTS } };
    },
    computed: {
      valid() {
        return startOptionsValid(this.form);
      },
    },
    methods: {
      submit() {
        this.$emit('restart', restartRequestBody(this.form));
        this.$emit('close');
      },
      cancel() {
        this.$emit('cancel');
        this.$emit('close');
      },
    },
  };
</script>
