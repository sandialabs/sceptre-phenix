<template>
  <b-field class="file-selection-actions" grouped>
    <b-field v-if="canDownload">
      <b-button
        class="file-download-selected"
        type="is-light"
        icon-left="download"
        :loading="downloading"
        :disabled="busy && !downloading"
        @click="$emit('download')">
        Download ({{ count }})
      </b-button>
    </b-field>
    <b-field v-if="canDelete">
      <b-button
        class="file-delete-selected"
        type="is-danger"
        icon-left="trash"
        :loading="deleting"
        :disabled="busy && !deleting"
        @click="$emit('delete')">
        Delete ({{ count }})
      </b-button>
    </b-field>
    <b-field>
      <b-tooltip label="clear the selection" type="is-dark" position="is-right">
        <b-button
          class="file-clear-selection"
          type="is-light"
          icon-left="window-close"
          aria-label="clear the selection"
          :disabled="busy"
          @click="$emit('clear')">
        </b-button>
      </b-tooltip>
    </b-field>
  </b-field>
</template>

<script>
  // The Files tab's toolbar while files are checked, like the VMs tab's
  // multi-VM toolbar; it sits above the table, as the row with the search is
  // full on the Files tab.
  export default {
    name: 'FileSelectionActions',
    props: {
      count: { type: Number, required: true },
      canDownload: { type: Boolean, default: false },
      canDelete: { type: Boolean, default: false },
      downloading: { type: Boolean, default: false },
      deleting: { type: Boolean, default: false },
    },
    emits: ['download', 'delete', 'clear'],
    computed: {
      busy() {
        return this.downloading || this.deleting;
      },
    },
  };
</script>
