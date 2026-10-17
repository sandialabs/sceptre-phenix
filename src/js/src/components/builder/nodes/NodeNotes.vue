<!--
  The notes of a device or a switch (see nodeNotes in model.js), in a small
  card below the node's box. The card has one line per note, and cuts each
  note off with an ellipsis after three lines. It shows at most five notes,
  and then how many more there are (see nodeNotes.js, which also estimates
  the card's height for the layouts). Its left border is the node's outline
  color, or the theme's border without one. Its text has the theme's color
  on the theme's surface, so it reads in both themes.

  The card is inside Vue Flow's wrapper, beside the node's box. For this
  reason, it moves and is selected with the node. But it is not part of the
  box that the document sizes and that the handles sit on. The Show node
  notes setting hides the card. The node's description says the notes (see
  nodeInfo.js), so the card itself is hidden from assistive technology.
-->
<template>
  <div
    v-if="builderSettings.showNodeNotes && notes.length"
    class="builder-node-notes"
    :class="[`builder-node-notes--${kind}`, { 'is-selected': selected }]"
    :style="colorStyle"
    aria-hidden="true"
    data-testid="node-notes">
    <p
      v-for="(note, index) in shown.shown"
      :key="index"
      class="builder-node-notes__note"
      data-testid="node-note">
      {{ note }}
    </p>
    <p
      v-if="shown.more"
      class="builder-node-notes__more"
      data-testid="node-notes-more">
      +{{ shown.more }} more
    </p>
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import { shownNotes } from '@/builder/nodeNotes.js';
  import { builderSettings } from '@/builder/settings.js';

  const props = defineProps({
    // device or switch, which sets the space above the card.
    kind: { type: String, required: true },
    // The node's notes, from nodeNotes.
    notes: { type: Array, required: true },
    selected: { type: Boolean, default: false },
    // The node's colors as custom properties (see nodeColors.js), for the
    // outline color of the card's accent.
    colorStyle: { type: Object, default: undefined },
  });

  const shown = computed(() => shownNotes(props.notes));
</script>
