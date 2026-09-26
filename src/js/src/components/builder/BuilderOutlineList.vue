<!--
  One level of the outline.

  Each row is a node, a toggle button whose pressed state is the selection. A
  group's members are a list nested in the group's list item, so screen
  readers announce the nesting level. Connections have no rows: a node's name
  counts them, and the canvas, the Inspector's Connection points and the
  command palette's Disconnect list them.
  The state and the handlers live in BuilderOutline, which provides them.

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
    <li v-for="item in items" :key="item.id" class="builder-outline__row">
      <div
        v-if="outline.renamingId === item.id"
        class="builder-outline__item"
        :data-testid="`outline-item-${item.id}`">
        <builder-icon :name="outline.iconFor(item)" :size="14" />
        <label class="builder-visually-hidden" :for="`rename-${item.id}`">
          Rename {{ item.label }}
        </label>
        <input
          :id="`rename-${item.id}`"
          v-model="outline.renameValue"
          class="builder-outline__rename"
          type="text"
          @keydown="outline.onRenameKeydown"
          @keydown.enter.prevent="outline.commitRename(item, true)"
          @keydown.esc.prevent="outline.cancelRename(item)"
          @change="outline.onRenameChange(item, $event)"
          @blur="outline.commitRename(item, false)" />
      </div>
      <button
        v-else
        :id="outline.rowId(item.id)"
        type="button"
        class="builder-outline__item"
        :data-testid="`outline-item-${item.id}`"
        :aria-label="item.accessibleName"
        :aria-pressed="outline.isSelected(item.id)"
        :tabindex="item.id === outline.rovingId ? 0 : -1"
        @click="outline.onRowClick(item, $event)"
        @keydown="outline.onRowKeydown(item, $event)"
        @focus="outline.onRowFocus(item)">
        <builder-icon :name="outline.iconFor(item)" :size="14" />
        <span class="builder-outline__label" :title="item.label">{{
          item.label
        }}</span>
        <!-- A device from an included topology says so; its name adds
             which topology, and that it is read only. -->
        <span class="builder-outline__kind">{{
          item.includedFrom ? 'included' : item.kind
        }}</span>
      </button>

      <builder-outline-list
        v-if="item.children.length"
        :items="item.children"
        class="builder-outline__members"
        :aria-label="`Members of ${item.label}`" />
    </li>
  </ul>
</template>

<script setup>
  import { inject } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import BuilderOutlineList from './BuilderOutlineList.vue';

  defineProps({
    items: { type: Array, required: true },
  });

  const outline = inject('builderOutline');
</script>
