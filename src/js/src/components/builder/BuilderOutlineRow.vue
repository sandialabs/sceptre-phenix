<!--
  One outline row: a node's list item, with its members nested in a group's.

  Each row asks about its own id only (pressed, the tab stop, being
  renamed), so a click redraws the rows it changes rather than every row.
  The state and the handlers live in BuilderOutline, which provides them;
  BuilderOutlineList describes the lists.
-->
<template>
  <li class="builder-outline__row">
    <div
      v-if="outline.isRenaming(item.id)"
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
      :aria-pressed="outline.isPressed(item.id)"
      :tabindex="outline.isRoving(item.id) ? 0 : -1"
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
</template>

<script setup>
  import { inject } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import BuilderOutlineList from './BuilderOutlineList.vue';

  defineProps({
    item: { type: Object, required: true },
  });

  const outline = inject('builderOutline');
</script>
