<!-- An editable list of experiment or VM annotations: a menu adds one of the
annotations phenix knows (with a value input suited to it) or a custom key and
value. -->
<template>
  <div
    class="annotation-list"
    role="group"
    :aria-labelledby="`${idPrefix}-label`">
    <field-label :id="`${idPrefix}-label`" :label="label" :help="help" />
    <div
      v-for="(row, index) in modelValue"
      :key="row.id"
      class="annotation-row"
      :class="`annotation-${row.type}`">
      <code v-if="row.known" class="annotation-key">{{ row.key }}</code>
      <b-input
        v-else
        class="annotation-key"
        size="is-small"
        placeholder="key"
        :aria-label="`Annotation ${index + 1} key`"
        :model-value="row.key"
        @update:model-value="update(index, { key: $event })" />
      <b-select
        v-if="row.type == 'boolean'"
        class="annotation-value"
        size="is-small"
        :aria-label="`${row.key} value`"
        :model-value="row.value"
        @update:model-value="update(index, { value: $event })">
        <option :value="true">true</option>
        <option :value="false">false</option>
      </b-select>
      <b-input
        v-else
        class="annotation-value"
        size="is-small"
        :type="row.type == 'password' ? 'password' : 'text'"
        :password-reveal="row.type == 'password'"
        :placeholder="row.placeholder || 'value'"
        :aria-label="`${row.key || `Annotation ${index + 1}`} value`"
        :model-value="row.value"
        @update:model-value="update(index, { value: $event })" />
      <button
        type="button"
        class="button is-small is-light annotation-remove"
        :aria-label="`Remove ${row.key || 'annotation'}`"
        @click="remove(index)">
        <b-icon icon="trash" size="is-small" />
      </button>
      <p v-if="errors[index]" class="help is-danger annotation-help">
        {{ errors[index] }}
      </p>
      <p v-else class="help annotation-help">{{ row.description }}</p>
    </div>
    <b-dropdown
      class="annotation-add"
      aria-role="menu"
      append-to-body
      scrollable
      :max-height="MENU_HEIGHT"
      :position="menuUp ? 'is-top-right' : 'is-bottom-right'">
      <template #trigger>
        <b-button
          size="is-small"
          type="is-light"
          icon-left="plus"
          icon-right="chevron-down"
          @click="placeMenu">
          Add annotation
        </b-button>
      </template>
      <b-dropdown-item
        v-for="entry in available"
        :key="entry.key"
        class="annotation-add-item"
        aria-role="menuitem"
        @click="add(entry)">
        <b>{{ entry.key }}</b>
        <small>{{ entry.summary }}</small>
      </b-dropdown-item>
      <hr v-if="available.length" class="dropdown-divider" />
      <b-dropdown-item
        class="annotation-add-item"
        aria-role="menuitem"
        @click="add(custom)">
        <b>Custom annotation</b>
        <small>{{ custom.summary }}</small>
      </b-dropdown-item>
    </b-dropdown>
  </div>
</template>

<script>
  import FieldLabel from '@/components/FieldLabel.vue';
  import {
    annotationErrors,
    annotationRow,
  } from '@/utils/experimentAnnotations.js';

  // the menu's height at most, in pixels
  const MENU_HEIGHT = 360;

  export default {
    name: 'AnnotationList',
    components: { FieldLabel },
    props: {
      modelValue: { type: Array, required: true },
      idPrefix: { type: String, required: true },
      label: { type: String, required: true },
      help: { type: String, required: true },
      // the annotations phenix knows, offered by the menu
      catalog: { type: Array, required: true },
      // the entry for a custom annotation
      custom: { type: Object, required: true },
      // keys the list may not hold, and why
      managed: { type: Object, default: () => ({}) },
    },
    emits: ['update:modelValue'],
    setup() {
      return { MENU_HEIGHT };
    },
    data() {
      return {
        // the menu opens upward when there is no room for it below
        menuUp: false,
      };
    },
    computed: {
      // known annotations not in the list yet
      available() {
        const keys = new Set(this.modelValue.map((row) => row.key.trim()));
        return this.catalog.filter((entry) => !keys.has(entry.key));
      },
      errors() {
        return annotationErrors(this.modelValue, this.managed);
      },
    },
    methods: {
      // Runs before the menu opens: the card cannot scroll a menu added to the
      // page body into view.
      placeMenu(event) {
        const { top, bottom } = event.currentTarget.getBoundingClientRect();
        const below = window.innerHeight - bottom;
        this.menuUp = below < MENU_HEIGHT && top > below;
      },
      add(entry) {
        this.$emit('update:modelValue', [
          ...this.modelValue,
          annotationRow(entry),
        ]);
      },
      update(index, change) {
        const rows = [...this.modelValue];
        rows[index] = { ...rows[index], ...change };
        this.$emit('update:modelValue', rows);
      },
      remove(index) {
        this.$emit(
          'update:modelValue',
          this.modelValue.filter((_, i) => i != index),
        );
      },
    },
  };
</script>

<style scoped>
  .annotation-list + .annotation-list {
    margin-top: 1rem;
  }

  .annotation-row {
    display: grid;
    grid-template-columns: 13rem minmax(0, 1fr) auto;
    align-items: center;
    gap: 0.25rem 0.5rem;
    margin-bottom: 0.5rem;
  }

  code.annotation-key {
    padding: 0.3em 0.6em;
    overflow-wrap: anywhere;
    font-size: 0.8rem;
    color: inherit;
    background-color: rgba(255, 255, 255, 0.1);
  }

  .annotation-help {
    grid-column: 1 / -1;
    margin-top: 0;
  }

  .annotation-add-item {
    display: flex !important;
    flex-direction: column;
    padding-right: 1rem !important;
  }
</style>
