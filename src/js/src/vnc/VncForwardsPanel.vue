<!-- The Port forwards panel of the console top bar: the port forwards of
     the VM (GET forwards), each with Remove (DELETE forwards), Add port
     forward, which asks the page for the add dialog, and a link to the
     Tunneler page. It reads the list when it opens and when `refresh`
     changes. An action that the role or the VM state rules out is greyed
     out with the reason in its tooltip. -->
<script setup>
  import { computed, getCurrentInstance, ref, watch } from 'vue';

  import { deleteVmForward, listVmForwards } from '@/api/vms.js';
  import {
    notPermittedReason,
    vmActionState,
  } from '@/components/experiment/vmActions.js';
  import { usePhenixStore } from '@/store.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { roleAllowed } from '@/utils/rbac.js';

  const props = defineProps({
    experiment: { type: String, required: true },
    vm: { type: String, required: true },
    // the VM row of the pane, or null
    vmRow: { type: Object, default: null },
    // the panel is open: it reads the list
    open: { type: Boolean, default: false },
    // a new value reads the list again, as after an add
    refresh: { type: Number, default: 0 },
  });

  const emit = defineEmits(['add']);

  const buefy = getCurrentInstance()?.proxy?.$buefy;

  // the port forwards, null until they load
  const forwards = ref(null);
  const failed = ref(false);
  // the keys of the forwards that Remove waits on
  const removing = ref([]);
  let generation = 0;

  const can = (resource, verb) =>
    roleAllowed(resource, verb, `${props.experiment}/${props.vm}`);

  const listReason = computed(() =>
    can('vms/forwards', 'list')
      ? null
      : notPermittedReason('vms/forwards', 'list'),
  );

  const add = computed(() =>
    props.vmRow
      ? vmActionState('portForward', props.vmRow, { can })
      : {
          disabled: true,
          reason: 'Cannot forward a port: the VM has not loaded',
        },
  );

  const keyOf = (forward) =>
    `${forward.dstHost}:${forward.dstPort}:${forward.owner}`;

  // why Remove is greyed out, or null; the server removes only own forwards
  function removeReason(forward) {
    if (!can('vms/forwards', 'delete')) {
      return notPermittedReason('vms/forwards', 'delete');
    }
    if (forward.owner !== usePhenixStore().username) {
      return `Only ${forward.owner}, who made this port forward, can remove it`;
    }
    return null;
  }

  async function load() {
    if (listReason.value) return;
    const mine = ++generation;
    try {
      const { data } = await listVmForwards(props.experiment, props.vm);
      if (mine !== generation) return;
      forwards.value = data?.listeners ?? [];
      failed.value = false;
    } catch (err) {
      if (mine !== generation) return;
      failed.value = true;
      useErrorNotification(err);
    }
  }

  watch(
    () => [props.experiment, props.vm],
    () => {
      generation += 1;
      forwards.value = null;
      failed.value = false;
    },
  );

  watch(
    () => [props.open, props.experiment, props.vm, props.refresh],
    () => props.open && load(),
    { immediate: true },
  );

  async function remove(forward) {
    const key = keyOf(forward);
    if (removeReason(forward) || removing.value.includes(key)) return;
    removing.value = [...removing.value, key];
    try {
      await deleteVmForward(props.experiment, props.vm, {
        host: forward.dstHost,
        dst: forward.dstPort,
      });
      buefy?.toast.open({
        message: escapeHTML(
          `Removed the port forward ${forward.srcPort} → ${forward.dstHost}:${forward.dstPort} of the ${props.vm} VM`,
        ),
        type: 'is-success',
      });
    } catch (err) {
      useErrorNotification(err);
    } finally {
      removing.value = removing.value.filter((other) => other !== key);
      await load();
    }
  }
</script>

<template>
  <div class="vnc-forwards">
    <h2 class="vnc-panel-title">Port forwards</h2>
    <p v-if="listReason" class="vnc-panel-note">{{ listReason }}</p>
    <p v-else-if="forwards === null" class="vnc-panel-note">
      {{ failed ? 'Could not list the port forwards.' : 'Loading…' }}
    </p>
    <p v-else-if="!forwards.length" class="vnc-panel-note">
      The VM has no port forwards.
    </p>
    <div v-else class="vnc-forward-scroll">
      <table class="vnc-forward-table">
        <thead>
          <tr>
            <th scope="col">Host port</th>
            <th scope="col">Destination</th>
            <th scope="col">Owner</th>
            <th scope="col"><span class="is-sr-only">Remove</span></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="forward in forwards"
            :key="keyOf(forward)"
            class="vnc-forward">
            <td class="vnc-forward-port">{{ forward.srcPort }}</td>
            <td class="vnc-forward-destination">
              {{ forward.dstHost }}:{{ forward.dstPort }}
            </td>
            <td class="vnc-forward-owner">{{ forward.owner }}</td>
            <td class="vnc-forward-action">
              <b-tooltip
                :label="removeReason(forward) || 'Remove this port forward'"
                type="is-dark"
                position="is-left"
                multilined
                append-to-body>
                <b-button
                  size="is-small"
                  class="vnc-forward-remove"
                  icon-left="trash"
                  :aria-label="`Remove the port forward ${forward.srcPort} to ${forward.dstHost}:${forward.dstPort}`"
                  :aria-disabled="removeReason(forward) ? 'true' : undefined"
                  :loading="removing.includes(keyOf(forward))"
                  @click="remove(forward)">
                  Remove
                </b-button>
              </b-tooltip>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="vnc-panel-row">
      <b-tooltip
        :label="add.reason || 'Forward a port to a port on the VM'"
        type="is-dark"
        position="is-bottom"
        multilined>
        <b-button
          size="is-small"
          class="vnc-forward-add"
          icon-left="plus"
          :aria-disabled="add.disabled ? 'true' : undefined"
          @click="add.disabled || emit('add')">
          Add port forward
        </b-button>
      </b-tooltip>
      <b-tooltip
        label="Open the Tunneler page in a new tab, with its documentation and downloads"
        type="is-dark"
        position="is-bottom"
        multilined>
        <b-button
          tag="router-link"
          :to="{ name: 'tunneler' }"
          target="_blank"
          rel="noopener"
          size="is-small"
          class="vnc-forward-tunneler"
          icon-left="download">
          Tunneler
        </b-button>
      </b-tooltip>
    </div>
    <p class="vnc-panel-hint">
      Use the phēnix tunneler on your computer to connect to a forwarded port.
    </p>
  </div>
</template>

<style scoped>
  .vnc-forward-scroll {
    max-height: min(50vh, 18rem);
    margin-bottom: 0.5rem;
    overflow-y: auto;
  }

  .vnc-forward-table {
    width: 100%;
    font-size: 0.8rem;
    border-collapse: collapse;
  }

  .vnc-forward-table th {
    position: sticky;
    top: 0;
    padding: 0 0.6rem 0.25rem 0;
    font-weight: 700;
    color: #e5e7eb;
    text-align: left;
    white-space: nowrap;
    background: #2b3038;
    border-bottom: 1px solid #6b7280;
  }

  .vnc-forward-table td {
    padding: 0.3rem 0.6rem 0.3rem 0;
    vertical-align: middle;
    border-bottom: 1px solid #3b414b;
  }

  .vnc-forward-port,
  .vnc-forward-destination {
    font-family: monospace;
    white-space: nowrap;
  }

  .vnc-forward-owner {
    max-width: 14rem;
    overflow-wrap: anywhere;
  }

  .vnc-forward-table .vnc-forward-action {
    padding-right: 0;
    text-align: right;
  }
</style>
