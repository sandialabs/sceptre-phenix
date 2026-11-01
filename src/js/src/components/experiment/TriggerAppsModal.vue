<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    @close="$emit('close')"
    has-modal-card>
    <div class="modal-card" style="width: 25em">
      <header class="modal-card-head">
        <p class="modal-card-title">phēnix Apps</p>
      </header>
      <section class="modal-card-body">
        <div v-if="triggerable.length">
          <b-checkbox
            v-for="(a, index) in triggerable"
            :key="index"
            :native-value="a"
            :model-value="apps"
            @update:model-value="(picked) => $emit('update:apps', picked)"
            type="is-light">
            {{ a }}
          </b-checkbox>
        </div>
        <div v-else>
          <span>This experiment does not include any triggerable apps.</span>
        </div>
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

  // The dialog that triggers the experiment's apps the role picks; the page
  // sends the request on trigger.
  export default {
    name: 'TriggerAppsModal',
    setup() {
      return { roleAllowed };
    },
    props: {
      active: { type: Boolean, default: false },
      // the experiment's name, which RBAC checks the trigger permission under
      experiment: { type: String, default: '' },
      // the apps that can be triggered, and those picked
      triggerable: { type: Array, default: () => [] },
      apps: { type: Array, default: () => [] },
    },
    emits: ['close', 'trigger', 'update:active', 'update:apps'],
  };
</script>
