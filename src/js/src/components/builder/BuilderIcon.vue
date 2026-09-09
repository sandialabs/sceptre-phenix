<!--
  Builder icon set.

  The beta builder ships its own inline SVG icons instead of pulling from the
  global Font Awesome library: the editor chunk stays self contained, icons
  inherit currentColor for both themes, and nodes are identified by shape as
  well as color.
-->
<template>
  <svg
    class="builder-icon"
    :class="`builder-icon--${name}`"
    viewBox="0 0 24 24"
    :width="size"
    :height="size"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    :role="title ? 'img' : 'presentation'"
    :aria-hidden="title ? undefined : 'true'"
    :aria-label="title || undefined">
    <title v-if="title">{{ title }}</title>
    <g v-html="path" />
  </svg>
</template>

<script setup>
  import { computed } from 'vue';

  const props = defineProps({
    name: { type: String, required: true },
    size: { type: [Number, String], default: 18 },
    title: { type: String, default: '' },
  });

  // Keys of the bounded server icon registry (phenix/types/builder icons.go)
  // come first; the remaining entries are UI chrome icons.
  const PATHS = {
    centos:
      '<circle cx="12" cy="12" r="8"/><path d="M12 4v16M4 12h16M6.3 6.3l11.4 11.4M17.7 6.3L6.3 17.7"/>',
    container:
      '<rect x="3" y="8" width="18" height="11" rx="1.5"/><path d="M7 8V5h10v3M7 12h4v4H7z"/>',
    external:
      '<rect x="3" y="6" width="12" height="12" rx="1.5"/><path d="M17 7h4v4M21 7l-6 6"/>',
    firewall:
      '<path d="M12 3l7 3v5c0 5-3 8-7 10-4-2-7-5-7-10V6z"/><path d="M9 11h6M12 8v6"/>',
    linux:
      '<path d="M9 4h6v6l3 6a3 3 0 0 1-3 4H9a3 3 0 0 1-3-4l3-6z"/><path d="M10 8h.01M14 8h.01"/>',
    printer:
      '<path d="M7 9V4h10v5"/><rect x="4" y="9" width="16" height="7" rx="1.5"/><path d="M7 16h10v4H7z"/>',
    redhat:
      '<path d="M4 13c2 4 6 6 10 6s6-2 6-5c0-2-1-3-3-4"/><path d="M7 10c1-3 4-5 7-5 2 0 3 1 3 3"/>',
    router:
      '<rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 17h.01M11 17h.01"/><path d="M12 10V4M9 7l3-3 3 3"/>',
    switch: '<path d="M12 3l7 4v6l-7 4-7-4V7z"/><path d="M5 9h14M12 3v14"/>',
    vlan: '<path d="M5 4h14v10l-5 6H5z"/><path d="M19 14h-5v6"/><path d="M8 8h8M8 11h5"/>',
    windows:
      '<path d="M4 6l7-1v6H4z"/><path d="M13 4.7L20 4v7h-7z"/><path d="M4 13h7v6l-7-1z"/><path d="M13 13h7v7l-7-1z"/>',
    server:
      '<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/><circle cx="7" cy="7" r="0.9"/><circle cx="7" cy="17" r="0.9"/>',
    desktop:
      '<rect x="3" y="4" width="18" height="11" rx="1.5"/><path d="M9 19h6M12 15v4"/>',
    route:
      '<circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M8.5 18h5a4 4 0 0 0 4-4V8.5"/>',
    'shield-alt': '<path d="M12 3l7 3v5c0 5-3 8-7 10-4-2-7-5-7-10V6z"/>',
    microchip:
      '<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4"/>',
    print:
      '<path d="M7 9V4h10v5"/><rect x="4" y="9" width="16" height="7" rx="1.5"/><path d="M7 16h10v4H7z"/>',
    'network-wired':
      '<path d="M12 3l7 4v6l-7 4-7-4V7z"/><path d="M5 9h14M12 3v14"/>',
    'sticky-note':
      '<path d="M5 4h14v10l-5 6H5z"/><path d="M19 14h-5v6"/><path d="M8 8h8M8 11h5"/>',
    'object-group':
      '<rect x="3" y="4" width="18" height="16" rx="2" stroke-dasharray="4 3"/><rect x="6" y="7" width="5" height="5" rx="1"/><rect x="13" y="12" width="5" height="5" rx="1"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    trash:
      '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/><path d="M10 11v6M14 11v6"/>',
    undo: '<path d="M9 7H5V3"/><path d="M5 7a8 8 0 1 1-1 8"/>',
    redo: '<path d="M15 7h4V3"/><path d="M19 7a8 8 0 1 0 1 8"/>',
    copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h8"/>',
    paste:
      '<path d="M9 4h6v3H9z"/><path d="M8 5H6a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V6a1 1 0 0 0-1-1h-2"/>',
    save: '<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v6h8V4M8 20v-6h8v6"/>',
    download: '<path d="M12 4v11M8 11l4 4 4-4"/><path d="M5 19h14"/>',
    upload: '<path d="M12 20V9M8 13l4-4 4 4"/><path d="M5 4h14"/>',
    layout:
      '<circle cx="12" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="19" r="2"/><path d="M12 7v5M12 12H6v5M12 12h6v5"/>',
    link: '<path d="M10 14a4 4 0 0 0 6 0l2-2a4 4 0 0 0-6-6l-1 1"/><path d="M14 10a4 4 0 0 0-6 0l-2 2a4 4 0 0 0 6 6l1-1"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M19 5l-1.5 1.5M6.5 17.5L5 19"/>',
    moon: '<path d="M20 14a8 8 0 1 1-10-10 7 7 0 0 0 10 10z"/>',
    system:
      '<rect x="3" y="5" width="18" height="12" rx="2"/><path d="M8 21h8"/>',
    warning: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>',
    check: '<path d="M5 13l4 4 10-10"/>',
    close: '<path d="M6 6l12 12M18 6L6 18"/>',
    search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
    refresh:
      '<path d="M4 12a8 8 0 0 1 13.7-5.6L20 8"/><path d="M20 4v4h-4"/><path d="M20 12a8 8 0 0 1-13.7 5.6L4 16"/><path d="M4 20v-4h4"/>',
    publish: '<path d="M12 19V6M7 11l5-5 5 5"/><path d="M5 21h14"/>',
    image:
      '<rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="M5 17l5-4 4 3 3-2 2 3"/>',
    document:
      '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 16h6"/>',
    group:
      '<rect x="3" y="4" width="18" height="16" rx="2" stroke-dasharray="4 3"/>',
    ungroup:
      '<rect x="3" y="4" width="8" height="8" rx="1.5"/><rect x="13" y="12" width="8" height="8" rx="1.5"/>',
    keyboard:
      '<rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M8 14h8"/>',
  };

  const path = computed(() => PATHS[props.name] || PATHS.server);
</script>

<style scoped>
  .builder-icon {
    flex: none;
    vertical-align: middle;
  }
</style>
