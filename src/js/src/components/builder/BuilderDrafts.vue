<!--
  Drafts landing.

  Three tabbed lists (my drafts, drafts shared with me, published diagrams)
  plus the three ways to start: blank, import, generate.
-->
<template>
  <section aria-labelledby="drafts-title">
    <div class="builder-drafts__header">
      <h2 id="drafts-title">Builder Beta</h2>

      <div class="builder-drafts__actions">
        <button
          type="button"
          class="builder-button builder-button--primary"
          data-testid="drafts-blank"
          @click="$emit('blank')">
          <builder-icon name="plus" :size="14" />
          Blank diagram
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-import"
          @click="$emit('import')">
          <builder-icon name="upload" :size="14" />
          Import
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-generate"
          @click="$emit('generate')">
          <builder-icon name="layout" :size="14" />
          Generate
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-refresh"
          @click="$emit('refresh')">
          <builder-icon name="refresh" :size="14" />
          Refresh
        </button>
      </div>
    </div>

    <div class="builder-tabs" role="tablist" aria-label="Builder collections">
      <button
        v-for="tab in tabs"
        :id="`tab-${tab.id}`"
        :key="tab.id"
        type="button"
        role="tab"
        class="builder-tab"
        :aria-selected="active === tab.id"
        :aria-controls="`panel-${tab.id}`"
        :tabindex="active === tab.id ? 0 : -1"
        :data-testid="`drafts-tab-${tab.id}`"
        @click="active = tab.id"
        @keydown="onTabKeydown($event, tab)">
        {{ tab.label }}
        <span class="builder-card__meta">({{ tab.items.length }})</span>
      </button>
    </div>

    <div
      v-for="tab in tabs"
      :id="`panel-${tab.id}`"
      :key="`panel-${tab.id}`"
      role="tabpanel"
      :aria-labelledby="`tab-${tab.id}`"
      :hidden="active !== tab.id">
      <p v-if="loading" role="status">Loading…</p>
      <p v-else-if="!tab.items.length">{{ tab.empty }}</p>

      <ul v-else class="builder-cards" :data-testid="`drafts-list-${tab.id}`">
        <li
          v-for="item in tab.items"
          :key="`${item.owner || 'published'}-${item.id}`"
          class="builder-card builder-panel">
          <h3>{{ itemLabel(item) }}</h3>
          <p class="builder-card__meta">
            <span v-if="item.owner">Owner: {{ item.owner }}</span>
            <span v-if="item.updatedAt || item.updated">
              · Updated {{ item.updatedAt || item.updated }}
            </span>
            <span v-if="item.nodeCount != null">
              · {{ item.nodeCount }} nodes
            </span>
          </p>
          <p v-if="item.description" class="builder-card__meta">
            {{ item.description }}
          </p>

          <div class="builder-drafts__actions">
            <button
              type="button"
              class="builder-button"
              :data-testid="`draft-open-${item.id}`"
              :aria-label="`Open ${itemLabel(item)}`"
              @click="$emit('open', item)">
              Open
            </button>
            <button
              v-if="tab.id === 'mine'"
              type="button"
              class="builder-button builder-button--danger"
              :aria-label="`Delete ${itemLabel(item)}`"
              @click="$emit('delete', item)">
              Delete
            </button>
          </div>
        </li>
      </ul>
    </div>
  </section>
</template>

<script setup>
  import { computed, ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  const props = defineProps({
    mine: { type: Array, default: () => [] },
    shared: { type: Array, default: () => [] },
    published: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false },
  });

  defineEmits(['open', 'delete', 'blank', 'import', 'generate', 'refresh']);

  const active = ref('mine');

  function itemLabel(item) {
    return item.name || item.title || item.target || item.id;
  }

  const tabs = computed(() => [
    {
      id: 'mine',
      label: 'My Drafts',
      items: props.mine,
      empty: 'You have no drafts yet. Start from a blank diagram.',
    },
    {
      id: 'shared',
      label: 'Shared Drafts',
      items: props.shared,
      empty: 'No drafts have been shared with you.',
    },
    {
      id: 'published',
      label: 'Published Diagrams',
      items: props.published,
      empty: 'Nothing has been published yet.',
    },
  ]);

  function onTabKeydown(event, tab) {
    const ids = tabs.value.map((item) => item.id);
    const index = ids.indexOf(tab.id);

    if (event.key === 'ArrowRight') {
      event.preventDefault();
      active.value = ids[(index + 1) % ids.length];
      focusTab(active.value);
    }

    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      active.value = ids[(index - 1 + ids.length) % ids.length];
      focusTab(active.value);
    }
  }

  function focusTab(id) {
    document.getElementById(`tab-${id}`)?.focus();
  }
</script>

<style scoped>
  .builder-drafts__header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .builder-drafts__header h2 {
    font-size: 1.2rem;
    font-weight: 700;
    margin: 0;
  }

  .builder-drafts__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  .builder-cards {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .builder-card h3 {
    font-weight: 700;
    margin: 0 0 0.25rem;
  }
</style>
