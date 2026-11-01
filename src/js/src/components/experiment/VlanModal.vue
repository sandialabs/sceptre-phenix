<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    has-modal-card>
    <div class="modal-card" style="width: 25em">
      <header class="modal-card-head">
        <p class="modal-card-title">Change the VLAN</p>
      </header>
      <section class="modal-card-body">
        Move interface {{ netIndex }} from {{ formatLowercase(fromNet) }} to a
        new one for the {{ active ? vmName : 'unknown' }} VM. <br /><br />
        <b-field>
          <b-select
            :aria-label="`New VLAN for interface ${netIndex} of VM ${vmName}`"
            :model-value="vlan"
            @update:model-value="(value) => $emit('update:vlan', value)"
            expanded>
            <option value="0">disconnect</option>
            <option v-for="(n, index) in vlans" :key="index" :value="n">
              {{ formatLowercase(n.alias) }} ({{ n.vlan }})
            </option>
          </b-select>
        </b-field>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button is-success" @click="$emit('change')">
          Change
        </button>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  import { formattingMixin } from '@/utils/formattingMixin.js';

  // The dialog that moves a VM's network interface to another of the
  // experiment's VLANs, or disconnects it. The page asks to confirm on change.
  export default {
    name: 'VlanModal',
    mixins: [formattingMixin],
    props: {
      active: { type: Boolean, default: false },
      vmName: { type: String, default: null },
      // the interface's index and the network it is on now
      netIndex: { type: Number, default: null },
      fromNet: { type: String, default: null },
      // the experiment's VLANs, each { alias, vlan }
      vlans: { type: Array, default: () => [] },
      // the VLAN picked: one of vlans, or '0' to disconnect
      vlan: { type: [Object, String], default: null },
    },
    emits: ['change', 'update:active', 'update:vlan'],
  };
</script>
