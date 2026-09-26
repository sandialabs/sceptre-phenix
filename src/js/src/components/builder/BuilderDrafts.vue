<!--
  Drafts landing.

  Three tabbed lists (my drafts, drafts shared with me, published diagrams)
  plus the three ways to start: a blank diagram, Import (the server converts
  a topology or experiment config) and Upload (a Builder document), the
  command palette, the settings, and a link to the documentation. The view
  reads the lists again whenever they come back into view, so there is no
  Refresh button.

  The ways to start make a draft, so a role that cannot create drafts does
  not get them. While a draft is being made or opened (busy), the buttons
  that would make or open another are aria-disabled: they keep focus, and a
  second click does nothing.
-->
<template>
  <section ref="rootEl" aria-labelledby="drafts-title">
    <div class="builder-drafts__header">
      <h1 id="drafts-title">Builder Flow</h1>

      <div class="builder-drafts__actions">
        <template v-if="canCreate">
          <button
            type="button"
            class="builder-button builder-button--primary"
            data-testid="drafts-blank"
            :aria-disabled="busy || undefined"
            @click="busy || $emit('blank')">
            <builder-icon name="plus" :size="14" />
            Blank diagram
          </button>
          <!-- The test ids predate the names: drafts-generate is Import and
               drafts-import is Upload, after the dialogs they open. -->
          <button
            type="button"
            class="builder-button"
            data-testid="drafts-generate"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="busy || $emit('generate')">
            <builder-icon name="layout" :size="14" />
            Import
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="drafts-import"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="busy || $emit('import')">
            <builder-icon name="upload" :size="14" />
            Upload
          </button>
        </template>
        <!-- The key caps show the palette's key; aria-keyshortcuts says it
             to screen readers. -->
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-commands"
          aria-haspopup="dialog"
          :aria-keyshortcuts="ariaShortcuts('palette.open')"
          @click="$emit('commands')">
          <builder-icon name="command" :size="14" />
          Commands
          <builder-keycaps v-if="paletteKey" :spec="paletteKey" />
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-settings"
          aria-haspopup="dialog"
          :aria-keyshortcuts="ariaShortcuts('settings.open')"
          @click="$emit('settings')">
          <builder-icon name="settings" :size="14" />
          Settings
        </button>
        <a
          class="builder-button builder-help-link"
          :href="HELP_URL"
          target="_blank"
          rel="noopener"
          data-testid="drafts-help">
          <builder-icon name="help" :size="14" />
          Help
          <span class="builder-visually-hidden">(opens in a new tab)</span>
        </a>
      </div>
    </div>
    <p
      v-if="!canCreate"
      class="builder-drafts__note"
      data-testid="drafts-view-only">
      Your role can open drafts and published diagrams, but not create drafts.
    </p>

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
      :tabindex="tab.items.length ? undefined : 0"
      :hidden="active !== tab.id">
      <!-- A refresh keeps the cards on screen, so focus on them survives. -->
      <p v-if="loading && !tab.items.length" role="status">Loading…</p>
      <p v-else-if="!tab.items.length">{{ tab.empty }}</p>

      <ul v-else class="builder-cards" :data-testid="`drafts-list-${tab.id}`">
        <li
          v-for="item in tab.items"
          :key="`${item.owner || 'published'}-${item.id}`"
          class="builder-card builder-panel">
          <h2>{{ itemLabel(item) }}</h2>
          <p class="builder-card__meta">
            <span v-if="item.owner">Owner: {{ item.owner }}</span>
            <span v-if="stamp(item)">
              · {{ stamp(item).verb }} {{ stamp(item).time }}
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
              :aria-label="`Open ${cardName(item)}`"
              :aria-disabled="busy || undefined"
              @click="busy || $emit('open', item)">
              Open
            </button>
            <button
              v-if="tab.id === 'mine' && canDelete"
              type="button"
              class="builder-button builder-button--danger"
              :aria-label="`Delete ${cardName(item)}`"
              aria-haspopup="dialog"
              @click="confirming = item">
              Delete
            </button>
          </div>
        </li>
      </ul>
    </div>

    <builder-confirm
      v-if="confirming"
      id="draft-delete"
      :title="`Delete draft ${cardName(confirming)}?`"
      message="The draft and its whole history are removed from the server. This cannot be undone."
      confirm-label="Delete draft"
      @cancel="confirming = null"
      @confirm="confirmDelete" />
  </section>
</template>

<script setup>
  import { computed, nextTick, ref, watch } from 'vue';

  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderKeycaps from './BuilderKeycaps.vue';

  import { ariaShortcuts, commandKeys } from '@/builder/commands.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { HELP_URL } from '@/builder/help.js';
  import { rowTarget } from '@/builder/roving.js';

  const props = defineProps({
    mine: { type: Array, default: () => [] },
    shared: { type: Array, default: () => [] },
    published: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false },
    // What the role may do: make drafts (Blank, Import, Upload) and delete
    // its own.
    canCreate: { type: Boolean, default: true },
    canDelete: { type: Boolean, default: true },
    // A draft is being made or opened.
    busy: { type: Boolean, default: false },
  });

  const emit = defineEmits([
    'open',
    'delete',
    'blank',
    'import',
    'generate',
    'commands',
    'settings',
  ]);

  const rootEl = ref(null);
  const active = ref('mine');

  // The first key of the command palette, shown on its button.
  const paletteKey = computed(() => commandKeys('palette.open')[0] || '');

  function itemLabel(item) {
    return item.name || item.title || item.target || item.id;
  }

  // Drafts are listed with when they last changed, published diagrams with
  // when they were published.
  function stamp(item) {
    const updated = formatTimestamp(item.updatedAt || item.updated);
    if (updated) {
      return { verb: 'Updated', time: updated };
    }

    const published = formatTimestamp(item.createdAt);

    return published ? { verb: 'Published', time: published } : null;
  }

  // Card buttons name the time too, so drafts that share a title can be told
  // apart (WCAG 2.4.6).
  function cardName(item) {
    const when = stamp(item);

    return when
      ? `${itemLabel(item)}, ${when.verb.toLowerCase()} ${when.time}`
      : itemLabel(item);
  }

  const tabs = computed(() => [
    {
      id: 'mine',
      label: 'My Drafts',
      items: props.mine,
      empty: props.canCreate
        ? 'You have no drafts yet. Start from a blank diagram.'
        : 'You have no drafts.',
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

  // APG tabs pattern with automatic activation.
  function onTabKeydown(event, tab) {
    const ids = tabs.value.map((item) => item.id);
    const next = rowTarget(event.key, ids.indexOf(tab.id), ids.length);

    if (next === undefined) {
      return;
    }

    event.preventDefault();
    active.value = ids[next];
    focusTab(active.value);
  }

  function focusTab(id) {
    document.getElementById(`tab-${id}`)?.focus();
  }

  function focusActiveTab() {
    focusTab(active.value);
  }

  // For the command palette's Show commands: selects a tab and focuses it,
  // as the arrow keys do.
  function showTab(id) {
    if (tabs.value.some((tab) => tab.id === id)) {
      active.value = id;
      nextTick(() => focusTab(id));
    }
  }

  /**
   * Focuses the Open button of a listed draft or diagram, switching to its
   * tab first.
   *
   * @param {string} id
   * @returns {Promise<boolean>} whether the button took focus
   */
  async function focusDraft(id) {
    const tab = tabs.value.find((entry) =>
      entry.items.some((item) => item.id === id),
    );
    if (!id || !tab) {
      return false;
    }

    active.value = tab.id;
    await nextTick();
    const button = rootEl.value?.querySelector(
      `[data-testid="draft-open-${CSS.escape(id)}"]`,
    );
    button?.focus();

    return Boolean(button) && document.activeElement === button;
  }

  // A deleted draft and its history cannot be recovered, so Delete asks
  // first (WCAG 3.3.4).
  const confirming = ref(null);

  function confirmDelete() {
    const item = confirming.value;

    confirming.value = null;
    requestDelete(item);
  }

  // Deleting a card removes the focused Delete button. Once the list no
  // longer holds the draft, focus moves to the card that took its place, or
  // to the tab when none is left (WCAG 2.4.3). A delete that failed leaves
  // focus alone. The draft is named as on its card, with its time, since
  // most drafts share a title.
  let deleting = null;

  function requestDelete(item) {
    deleting = {
      id: item.id,
      index: props.mine.findIndex((entry) => entry.id === item.id),
    };
    emit('delete', item, cardName(item));
  }

  watch(
    () => props.mine,
    async (items) => {
      const pending = deleting;
      deleting = null;
      if (!pending || items.some((item) => item.id === pending.id)) {
        return;
      }

      await nextTick();
      // The user has moved on while the delete was in flight.
      if (document.activeElement && document.activeElement !== document.body) {
        return;
      }

      const next = items[Math.min(pending.index, items.length - 1)];
      if (!next || !(await focusDraft(next.id))) {
        focusActiveTab();
      }
    },
  );

  // activeTab is the shown tab's id, for the palette's Show commands.
  defineExpose({ activeTab: active, focusActiveTab, focusDraft, showTab });
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

  .builder-drafts__header h1 {
    font-size: 1.2rem;
    font-weight: 700;
    margin: 0;
  }

  .builder-drafts__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  .builder-drafts__note {
    margin: 0 0 0.75rem;
  }

  .builder-cards {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .builder-card h2 {
    font-weight: 700;
    margin: 0 0 0.25rem;
  }
</style>
