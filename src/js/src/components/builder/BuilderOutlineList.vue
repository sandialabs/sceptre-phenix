<!--
  One level of the outline.

  Each row is a node, a toggle button whose pressed state is the selection. A
  group's members are a list nested in the group's list item, so screen
  readers announce the nesting level. Connections have no rows: a node's name
  counts them, and the canvas, the Inspector's Connection points and the
  command palette's Disconnect list them.
  Each row is its own component (BuilderOutlineRow.vue). BuilderOutline
  holds the state and the handlers, and provides them.

  Every list has role="list": WebKit, and so VoiceOver, drops the list role
  from a list styled with list-style: none, and with it the nesting level.

  Accepted deviation from the APG tree view: the rows keep their arrow keys
  (a roving tab stop) without a composite role such as tree. That keeps the
  nested lists and the pressed state native. The cost is in screen reader
  browse mode (NVDA, JAWS): there the arrow keys move the virtual cursor, not
  the focus, so F2 and Delete act on the row that has focus, which may not be
  the row being read. Activating a row (Enter in browse mode, or a click)
  focuses it, so the keys pressed next act on the row just activated.
-->
<template>
  <ul role="list">
    <builder-outline-row v-for="item in items" :key="item.id" :item="item" />
  </ul>
</template>

<script setup>
  import BuilderOutlineRow from './BuilderOutlineRow.vue';

  defineProps({
    items: { type: Array, required: true },
  });
</script>
