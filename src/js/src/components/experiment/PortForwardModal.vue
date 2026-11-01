<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    @close="$emit('close')"
    has-modal-card>
    <div class="modal-card" style="width: 30em">
      <header class="modal-card-head">
        <p class="modal-card-title">Create New Port Forward</p>
      </header>
      <section class="modal-card-body">
        <b-field label="Source Port" label-for="port-forward-src-port">
          <b-input
            type="text"
            id="port-forward-src-port"
            :compat-fallthrough="false"
            :model-value="srcPort"
            @update:model-value="
              (value) => $emit('update:srcPort', value)
            "></b-input>
        </b-field>
        <b-field label="Destination Host" label-for="port-forward-dst-host">
          <b-input
            type="text"
            id="port-forward-dst-host"
            :compat-fallthrough="false"
            :model-value="dstHost"
            @update:model-value="
              (value) => $emit('update:dstHost', value)
            "></b-input>
        </b-field>
        <b-field label="Destination Port" label-for="port-forward-dst-port">
          <b-input
            type="text"
            id="port-forward-dst-port"
            :compat-fallthrough="false"
            :model-value="dstPort"
            @update:model-value="
              (value) => $emit('update:dstPort', value)
            "></b-input>
        </b-field>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button is-success" @click="$emit('create')">
          Create
        </button>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  // The dialog that creates a port forward to a VM: the ports and host it
  // forwards between. The page sends the request on create.
  export default {
    name: 'PortForwardModal',
    props: {
      active: { type: Boolean, default: false },
      srcPort: { type: [String, Number], default: null },
      dstHost: { type: String, default: '' },
      dstPort: { type: [String, Number], default: null },
    },
    emits: [
      'close',
      'create',
      'update:active',
      'update:dstHost',
      'update:dstPort',
      'update:srcPort',
    ],
  };
</script>
