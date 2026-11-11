<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    @close="$emit('close')"
    has-modal-card
    ref="modal">
    <div class="modal-card" style="width: auto">
      <header class="modal-card-head">
        <h2 class="modal-card-title">
          Redeploy the
          {{ pluralWord(vms.length, 'VM') }}
        </h2>
      </header>
      <section class="modal-card-body">
        <div v-if="vms.length > 0">
          <div v-for="(vmI, index) in vms" :key="index">
            <div>
              <hr v-if="parseInt(index) > 0" />
              Modify current settings and redeploy
              {{ vmI.name }} <br /><br />
              CPUs:
              <b-tooltip label="menu for assigning CPUs" type="is-dark">
                <b-select
                  :aria-label="`CPUs for VM ${vmI.name}`"
                  :model-value="vmI.cpus"
                  @update:model-value="(v) => $emit('edit', index, 'cpus', v)"
                  expanded>
                  <option
                    v-for="n in choicesWith(CPU_CHOICES, vmI.cpus)"
                    :key="n"
                    :value="String(n)">
                    {{ n }}
                  </option>
                </b-select>
              </b-tooltip>
              Memory:
              <b-tooltip label="menu for assigning memory" type="is-dark">
                <b-select
                  :aria-label="`Memory for VM ${vmI.name}`"
                  :model-value="vmI.ram"
                  @update:model-value="(v) => $emit('edit', index, 'ram', v)"
                  expanded>
                  <option
                    v-for="n in choicesWith(RAM_CHOICES, vmI.ram)"
                    :key="n"
                    :value="String(n)">
                    {{ settingText('ram', n) }}
                  </option>
                </b-select>
              </b-tooltip>
              <br /><br />
              Disk:
              <DiskSelect
                :model-value="vmI.disk"
                @update:model-value="(v) => $emit('edit', index, 'disk', v)"
                :disks="disks"
                :label="`Disk for VM ${vmI.name}`" />
              <br /><br />
              Replicate Original Injection(s):
              <b-tooltip label="menu for replicating injections" type="is-dark">
                <b-select
                  :aria-label="`Replicate original injections for VM ${vmI.name}`"
                  :model-value="vmI.inject"
                  @update:model-value="(v) => $emit('edit', index, 'inject', v)"
                  expanded>
                  <option value="true">Yes</option>
                  <option value="false">No</option>
                </b-select>
              </b-tooltip>
            </div>
          </div>
        </div>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="$refs.modal.cancel('x')">
          Cancel
        </button>
        <button class="button is-success" @click="$emit('redeploy')">
          Redeploy
        </button>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  import { pluralWord } from '@/utils/plural.js';
  import DiskSelect from './DiskSelect.vue';
  import {
    CPU_CHOICES,
    RAM_CHOICES,
    choicesWith,
    settingText,
  } from './vmSettings.js';

  // The dialog that redeploys VMs with the CPUs, memory and disk picked for
  // each, and whether to replicate their injections. The page sends the
  // requests on redeploy.
  export default {
    name: 'RedeployModal',
    components: { DiskSelect },
    setup() {
      return { pluralWord, CPU_CHOICES, RAM_CHOICES, choicesWith, settingText };
    },
    props: {
      active: { type: Boolean, default: false },
      // each VM to redeploy: { name, cpus, ram, disk, inject }
      vms: { type: Array, default: () => [] },
      // the disk images to offer, null until they have loaded
      disks: { type: Array, default: null },
    },
    emits: [
      'close',
      // (index, field, value): a VM's setting was picked
      'edit',
      'redeploy',
      'update:active',
    ],
  };
</script>
