<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    @close="$emit('close')"
    has-modal-card
    ref="modal">
    <div class="modal-card" style="width: auto">
      <header class="modal-card-head">
        <p class="modal-card-title">
          Redeploy the
          {{ pluralWord(vms.length, 'VM') }}
        </p>
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
                  <option v-for="n in 8" :key="n" :value="String(n)">
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
                    v-for="[value, label] in MEMORY"
                    :key="value"
                    :value="value">
                    {{ label }}
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

  // the memory a VM can be redeployed with: MB, and its label
  const MEMORY = [
    ['512', '512 MB'],
    ['1024', '1 GB'],
    ['2048', '2 GB'],
    ['3072', '3 GB'],
    ['4096', '4 GB'],
    ['8192', '8 GB'],
    ['12288', '12 GB'],
    ['16384', '16 GB'],
  ];

  // The dialog that redeploys VMs with the CPUs, memory and disk picked for
  // each, and whether to replicate their injections. The page sends the
  // requests on redeploy.
  export default {
    name: 'RedeployModal',
    components: { DiskSelect },
    setup() {
      return { pluralWord, MEMORY };
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
