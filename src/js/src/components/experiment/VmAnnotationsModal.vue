<template>
  <div class="modal-card vm-annotations-modal">
    <header class="modal-card-head">
      <h2 class="modal-card-title">Annotations of the {{ vmName }} VM</h2>
    </header>
    <section class="modal-card-body">
      <p v-if="!rows" class="vm-annotations-loading">
        {{ failed ? 'Could not load the annotations.' : 'Loading…' }}
      </p>
      <annotation-list
        v-else
        v-model="rows"
        id-prefix="vm-annotations-modal"
        label="Annotations"
        :help="help"
        :catalog="NODE_ANNOTATIONS"
        :custom="CUSTOM_NODE_ANNOTATION" />
    </section>
    <footer class="modal-card-foot buttons is-right">
      <b-button @click="$emit('close')">Cancel</b-button>
      <b-button
        type="is-success"
        class="vm-annotations-save"
        :disabled="!rows || !valid"
        :loading="saving"
        @click="save">
        Save
      </b-button>
    </footer>
  </div>
</template>

<script>
  import { getVm, updateVm } from '@/api/vms.js';
  import AnnotationList from '@/components/AnnotationList.vue';
  import { useErrorNotification } from '@/utils/errorNotif';
  import {
    CUSTOM_NODE_ANNOTATION,
    NODE_ANNOTATIONS,
    annotationErrors,
    annotationRowsFrom,
    nodeAnnotationsPayload,
    vmAnnotationHelp,
  } from '@/utils/experimentAnnotations.js';

  // The dialog that edits the annotations of one VM of a running experiment,
  // with the editor of the VM details card. It reads them from the server
  // first, as VM lists and events do not carry them. Save replaces them
  // (PATCH /experiments/{exp}/vms/{vm}), emits saved with the VM the server
  // answers, then closes. On an error it stays open to try again.
  export default {
    name: 'VmAnnotationsModal',
    components: { AnnotationList },
    props: {
      experiment: { type: String, required: true },
      vmName: { type: String, required: true },
      running: { type: Boolean, default: false },
    },
    emits: ['close', 'saved'],
    setup() {
      return { NODE_ANNOTATIONS, CUSTOM_NODE_ANNOTATION };
    },
    data() {
      return {
        // the rows being edited, null until the annotations load
        rows: null,
        failed: false,
        saving: false,
      };
    },
    async created() {
      try {
        const { data } = await getVm(this.experiment, this.vmName);
        this.rows = annotationRowsFrom(
          data?.annotations,
          NODE_ANNOTATIONS,
          CUSTOM_NODE_ANNOTATION,
        );
      } catch (err) {
        this.failed = true;
        useErrorNotification(err);
      }
    },
    computed: {
      help() {
        return vmAnnotationHelp(this.running);
      },
      valid() {
        return annotationErrors(this.rows ?? []).every((error) => !error);
      },
    },
    methods: {
      async save() {
        if (this.saving || !this.rows || !this.valid) return;
        this.saving = true;
        try {
          const resp = await updateVm(this.experiment, this.vmName, {
            annotations: nodeAnnotationsPayload(this.rows),
          });
          this.$emit('saved', resp.data);
          this.$emit('close');
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.saving = false;
        }
      },
    },
  };
</script>

<style scoped>
  .vm-annotations-modal {
    width: min(48rem, calc(100vw - 40px));
  }
</style>
