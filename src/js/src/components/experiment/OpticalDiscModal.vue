<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    has-modal-card
    @close="$emit('close')"
    ref="modal">
    <div class="modal-card optical-disc-card">
      <header class="modal-card-head">
        <p class="modal-card-title">
          {{ current ? 'Change the CD-ROM' : 'Insert a CD-ROM' }}
          for {{ vmName }}
        </p>
      </header>
      <section class="modal-card-body">
        <p v-if="current" class="optical-disc-current">
          Inserted:
          <span class="is-family-monospace" :title="current">
            {{ insertedIsoLabel }}
          </span>
        </p>
        <p
          v-if="loading"
          class="optical-disc-state optical-disc-loading"
          role="status">
          <span class="capture-spinner"></span>
          Loading ISO images…
        </p>
        <p
          v-else-if="error"
          class="optical-disc-state has-text-danger"
          role="alert">
          {{ error }}
        </p>
        <p v-else-if="!isos.length" class="optical-disc-state">
          There are no ISO images to insert. Upload an .iso file on the
          <router-link :to="{ name: 'disks' }">Disks</router-link> page, then
          try again.
        </p>
        <b-field v-else label="ISO image" label-for="optical-disc-iso">
          <b-select
            id="optical-disc-iso"
            :compat-fallthrough="false"
            :model-value="disc"
            @update:model-value="(value) => $emit('update:disc', value)"
            class="optical-disc-select"
            placeholder="Select an ISO image"
            expanded>
            <option
              v-for="iso in isoOptions.inside"
              :key="iso.value"
              :value="iso.value"
              :disabled="iso.disabled">
              {{ iso.label }}
            </option>
            <optgroup v-if="isoOptions.outside.length" :label="OUTSIDE_GROUP">
              <option
                v-for="iso in isoOptions.outside"
                :key="iso.value"
                :value="iso.value"
                :disabled="iso.disabled">
                {{ iso.label }}
              </option>
            </optgroup>
          </b-select>
        </b-field>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="$refs.modal.cancel('x')">
          Cancel
        </button>
        <button
          v-if="current"
          class="button is-danger is-outlined optical-disc-eject"
          @click="$emit('eject')">
          Eject
        </button>
        <button
          class="button is-success optical-disc-insert"
          :disabled="!disc || disc === current"
          @click="$emit('insert')">
          {{ current ? 'Change' : 'Insert' }}
        </button>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  import {
    OUTSIDE_GROUP,
    diskLabel,
    diskOptions,
    findDisk,
  } from '@/utils/diskChain.js';

  // The CD-ROM picker: the ISO image in a VM's CD-ROM drive, and the ISO
  // images to insert in its place. The page lists the images, and sends the
  // request on insert or eject.
  export default {
    name: 'OpticalDiscModal',
    setup() {
      return { OUTSIDE_GROUP };
    },
    props: {
      active: { type: Boolean, default: false },
      vmName: { type: String, default: null },
      // the ISO inserted when it opened, '' for none
      current: { type: String, default: '' },
      // the ISO picked
      disc: { type: String, default: null },
      // the ISO images to pick from, while loading and after
      isos: { type: Array, default: () => [] },
      loading: { type: Boolean, default: false },
      error: { type: String, default: null },
    },
    emits: ['close', 'eject', 'insert', 'update:active', 'update:disc'],

    computed: {
      // the ISO images by label, those outside the minimega files directory
      // apart
      isoOptions() {
        return diskOptions(this.isos);
      },
      // the ISO inserted when the picker opened, by its label, or its full
      // path when the list does not hold it
      insertedIsoLabel() {
        return diskLabel(findDisk(this.isos, this.current)) || this.current;
      },
    },
  };
</script>

<style scoped>
  /* a small spinner while the picker lists ISO images */
  .capture-spinner {
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    vertical-align: -0.1em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: capture-spin 0.6s linear infinite;
  }

  @keyframes capture-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .optical-disc-card {
    width: 32rem;
  }

  .optical-disc-current {
    margin-bottom: 0.75rem;
  }

  .optical-disc-state {
    margin: 0;
  }

  .optical-disc-loading {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
</style>
