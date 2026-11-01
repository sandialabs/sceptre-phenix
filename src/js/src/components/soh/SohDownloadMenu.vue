<!-- Download menu for the State of Health graph: PNG, SVG or GEXF. -->
<template>
  <b-dropdown
    class="soh-download"
    position="is-bottom-left"
    :disabled="disabled"
    aria-role="menu">
    <template #trigger>
      <b-button
        type="is-light"
        icon-left="download"
        icon-right="chevron-down"
        :loading="busy"
        :disabled="disabled">
        Download
      </b-button>
    </template>
    <b-dropdown-item
      v-for="f in formats"
      :key="f.id"
      class="soh-download-item"
      aria-role="menuitem"
      @click="$emit('download', f.id)">
      <b-icon :icon="f.icon" size="is-small" />
      <span>
        <b>{{ f.label }}</b>
        <small>{{ f.hint }}</small>
      </span>
    </b-dropdown-item>
  </b-dropdown>
</template>

<script>
  const DOWNLOAD_FORMATS = [
    {
      id: 'png',
      label: 'PNG',
      icon: 'image',
      hint: 'Image of the graph, 2× size',
    },
    {
      id: 'svg',
      label: 'SVG',
      icon: 'file-code',
      hint: 'Vector image, scales cleanly',
    },
    {
      id: 'gexf',
      label: 'GEXF',
      icon: 'share-nodes',
      hint: 'Graph with VM and SOH data for Gephi',
    },
  ];

  export default {
    name: 'SohDownloadMenu',
    props: {
      disabled: { type: Boolean, default: false },
      busy: { type: Boolean, default: false },
    },
    emits: ['download'],
    computed: {
      formats: () => DOWNLOAD_FORMATS,
    },
  };
</script>

<style scoped>
  .soh-download :deep(.dropdown-menu) {
    min-width: 16rem;
  }

  .soh-download-item {
    display: flex !important;
    align-items: center;
    gap: 0.7rem;
    white-space: nowrap;
    padding-right: 1rem !important;
  }

  .soh-download-item > span:not(.icon) {
    display: flex;
    flex-direction: column;
    line-height: 1.2;
  }

  .soh-download-item small {
    color: #cfcfcf;
    font-size: 0.78rem;
  }
</style>
