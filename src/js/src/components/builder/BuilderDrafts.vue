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
  second click does nothing. The Open of the one being opened says so.

  A draft the server can no longer read (damaged) is listed on its tab too,
  with why it cannot be opened, and Delete when the user may delete it.
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
          <p
            v-if="item.damaged"
            class="builder-drafts__damaged"
            :data-testid="`draft-damaged-${item.id}`">
            <builder-icon name="warning" :size="14" />
            This draft cannot be read, so it cannot be opened. A newer version
            of phenix may have saved it.
          </p>

          <div class="builder-drafts__actions">
            <!-- While it opens: a turning ring and Opening… (see
                 builder.css; reduced motion stops the ring turning). Both
                 labels hold the button's width; the hidden one is not
                 named. -->
            <button
              v-if="!item.damaged"
              type="button"
              class="builder-button"
              :data-testid="`draft-open-${item.id}`"
              :aria-label="`${isOpening(item) ? 'Opening' : 'Open'} ${cardName(item)}`"
              :aria-disabled="busy || undefined"
              :aria-busy="isOpening(item) || undefined"
              @click="busy || $emit('open', item, itemLabel(item))">
              <span
                v-if="isOpening(item)"
                class="builder-toolbar__spinner"
                aria-hidden="true"></span>
              <span class="builder-button__swap">
                <span :class="{ 'is-off': isOpening(item) }">Open</span>
                <span :class="{ 'is-off': !isOpening(item) }">Opening…</span>
              </span>
            </button>
            <button
              v-if="mayDelete(tab, item)"
              type="button"
              class="builder-button builder-button--danger"
              :data-testid="`draft-delete-${item.id}`"
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

<script>
  /**
   * Names a listed draft or published diagram apart from every other one,
   * for the view to say which it is opening.
   *
   * @param {{owner?: string, id: string}} item
   * @returns {string}
   */
  export function cardKey(item) {
    return `${item?.owner || ''}/${item?.id || ''}`;
  }
</script>

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
    // The draft or published diagram being opened, as cardKey names it.
    opening: { type: String, default: '' },
    // The drafts the server can no longer read: the user's own (mine) and
    // other users' (shared), each with whether the user may delete it.
    damaged: {
      type: Object,
      default: () => ({ mine: [], shared: [] }),
    },
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

  // A damaged draft whose title cannot be read is not named by its id.
  function itemLabel(item) {
    return (
      item.name ||
      item.title ||
      item.target ||
      (item.damaged ? 'Damaged draft' : item.id)
    );
  }

  function isOpening(item) {
    return Boolean(props.opening) && props.opening === cardKey(item);
  }

  // Delete is on the user's own drafts, if the role may delete them, and on
  // a damaged draft whenever the server says the user may delete it.
  function mayDelete(tab, item) {
    return item.damaged
      ? Boolean(item.canDelete)
      : tab.id === 'mine' && props.canDelete;
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

  // Damaged drafts follow the readable ones on their tab.
  const tabs = computed(() => [
    {
      id: 'mine',
      label: 'My Drafts',
      items: [...props.mine, ...(props.damaged.mine || [])],
      empty: props.canCreate
        ? 'You have no drafts yet. Start from a blank diagram.'
        : 'You have no drafts.',
    },
    {
      id: 'shared',
      label: 'Shared Drafts',
      items: [...props.shared, ...(props.damaged.shared || [])],
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
   * Focuses the Open button of a listed draft or diagram, or the Delete
   * button of a damaged draft, switching to its tab first.
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
      `[data-testid="draft-open-${CSS.escape(id)}"], [data-testid="draft-delete-${CSS.escape(id)}"]`,
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

  // Deleting a card removes the focused Delete button, and a card with focus
  // on it can leave its list when the lists are read again (deleted
  // elsewhere, say). Once its tab no longer holds the draft, focus moves to
  // the card that took its place, or to the tab when none is left (WCAG
  // 2.4.3). A delete that failed leaves focus alone. The draft is named as
  // on its card, with its time, since most drafts share a title.
  let deleting = null;

  function requestDelete(item) {
    const tab = tabs.value.find((entry) => entry.items.includes(item));

    deleting = {
      id: item.id,
      tab: tab?.id || active.value,
      index: tab ? tab.items.indexOf(item) : 0,
    };
    emit('delete', item, cardName(item));
  }

  // The card focus is on, as requestDelete records one. Read before the
  // lists re-render.
  function focusedCard() {
    const card = document.activeElement?.closest?.('.builder-card');
    const button = card?.querySelector(
      '[data-testid^="draft-open-"], [data-testid^="draft-delete-"]',
    );
    if (!button || !rootEl.value?.contains(card)) {
      return null;
    }

    return {
      id: button.dataset.testid.replace(/^draft-(open|delete)-/, ''),
      tab: active.value,
      index: [...card.parentElement.children].indexOf(card),
    };
  }

  watch(
    () => [props.mine, props.shared, props.damaged, props.published],
    async () => {
      const pending = deleting || focusedCard();
      deleting = null;
      const items =
        tabs.value.find((entry) => entry.id === pending?.tab)?.items || [];
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

  /* The warning sign is decorative: the text says the same. */
  .builder-drafts__damaged {
    display: flex;
    align-items: flex-start;
    gap: 0.35rem;
    margin: 0 0 0.4rem;
  }

  .builder-drafts__damaged > :first-child {
    flex: none;
    margin-top: 0.15em;
  }

  .builder-button[aria-busy='true'] {
    cursor: progress;
  }
</style>
