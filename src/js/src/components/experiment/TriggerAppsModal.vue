<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    @close="$emit('close')"
    has-modal-card>
    <div class="modal-card" style="width: 25em">
      <header class="modal-card-head">
        <h2 class="modal-card-title">phēnix Apps</h2>
      </header>
      <section class="modal-card-body">
        <b-field label="Stage" label-for="trigger-apps-stage">
          <b-select
            id="trigger-apps-stage"
            :compat-fallthrough="false"
            :model-value="stage"
            expanded
            @update:model-value="pickStage">
            <option v-for="s in stages" :key="s" :value="s">{{ s }}</option>
          </b-select>
        </b-field>
        <p v-if="stage !== 'running'" class="trigger-stage-warning" role="note">
          <b-icon icon="exclamation-triangle" size="is-small" />
          {{ stageWarning }}
        </p>
        <div v-if="triggerable.length">
          <b-checkbox
            v-for="a in triggerable"
            :key="a"
            :native-value="a"
            :model-value="apps"
            @update:model-value="(picked) => $emit('update:apps', picked)"
            type="is-light">
            {{ a }}
          </b-checkbox>
        </div>
        <p v-else class="trigger-apps-none">
          This experiment does not include any apps to trigger at the
          {{ stage }} stage.
        </p>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <div v-if="roleAllowed('experiments/trigger', 'create', experiment)">
          <b-tooltip label="start selected apps" type="is-light is-left">
            <b-button
              v-if="apps.length > 0"
              class="button is-success"
              @click="$emit('trigger')"
              >Trigger Apps</b-button
            >
            <b-button v-else disabled class="button is-success"
              >Trigger Apps</b-button
            >
          </b-tooltip>
        </div>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import { TRIGGER_STAGES, triggerableApps } from './experimentHeader.js';

  // The dialog that triggers the experiment's apps the role picks at the
  // stage it picks; the page sends the request on trigger.
  export default {
    name: 'TriggerAppsModal',
    setup() {
      return { roleAllowed, stages: TRIGGER_STAGES };
    },
    props: {
      active: { type: Boolean, default: false },
      // the experiment's name, which RBAC checks the trigger permission under
      experiment: { type: String, default: '' },
      // the experiment's apps, and those picked
      available: { type: Array, default: () => [] },
      apps: { type: Array, default: () => [] },
      // the stage the apps run at
      stage: { type: String, default: 'running' },
    },
    emits: ['close', 'trigger', 'update:active', 'update:apps', 'update:stage'],
    computed: {
      triggerable() {
        return triggerableApps(this.available, this.stage);
      },
      stageWarning() {
        return this.stage === 'configure'
          ? 'The configure stage runs on the running experiment and rewrites its spec.'
          : `The ${this.stage} stage runs on the running experiment and can change it.`;
      },
    },
    methods: {
      // a stage that does not offer an app picked drops it
      pickStage(stage) {
        this.$emit('update:stage', stage);
        const offered = triggerableApps(this.available, stage);
        const kept = this.apps.filter((app) => offered.includes(app));
        if (kept.length !== this.apps.length) this.$emit('update:apps', kept);
      },
    },
  };
</script>

<style scoped>
  .trigger-stage-warning {
    display: flex;
    align-items: baseline;
    gap: 0.4rem;
    margin-bottom: 0.75rem;
  }
</style>
