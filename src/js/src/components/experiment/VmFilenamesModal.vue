<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    has-modal-card
    @close="$emit('close')"
    ref="modal">
    <div class="modal-card" style="width: auto">
      <header class="modal-card-head">
        <p class="modal-card-title">{{ title }}</p>
      </header>
      <section class="modal-card-body">
        <div v-if="vms.length > 0">
          <div v-for="(vmI, index) in vms" :key="index">
            <div>
              <hr
                v-if="parseInt(index) > 0"
                style="color: #595959; background-color: #595959" />
              {{ prompt }} {{ vmI.name }} VM with filename: <br /><br />
              <b-field
                :type="vmI.nameErrType"
                :message="vmI.nameErrMsg"
                autofocus>
                <b-input
                  :aria-label="`${kind} filename for VM ${vmI.name}`"
                  type="text"
                  :model-value="vmI.filename"
                  @update:model-value="
                    (value) => $emit('update:filename', index, value)
                  "
                  focus></b-input>
              </b-field>
            </div>
          </div>
        </div>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="$refs.modal.cancel('x')">
          Cancel
        </button>
        <button
          class="button is-success"
          :disabled="!valid"
          @click="$emit('create')">
          Create
        </button>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  // The dialog that names the file a VM action writes for each VM, such as a
  // backing image or memory snapshot. The page checks the names (setting each
  // VM's nameErrType and nameErrMsg) and takes the action on create.
  export default {
    name: 'VmFilenamesModal',
    props: {
      active: { type: Boolean, default: false },
      title: { type: String, required: true },
      // what each file is, starting each field's label
      kind: { type: String, required: true },
      // what the dialog asks before each VM's name
      prompt: { type: String, required: true },
      // each VM: { name, filename, nameErrType, nameErrMsg }
      vms: { type: Array, default: () => [] },
      // whether every filename is allowed
      valid: { type: Boolean, default: true },
    },
    emits: [
      'close',
      'create',
      'update:active',
      // (index, filename): a VM's filename was typed
      'update:filename',
    ],
  };
</script>
