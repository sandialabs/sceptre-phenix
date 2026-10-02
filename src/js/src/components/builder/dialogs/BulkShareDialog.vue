<!--
  Share several drafts at once: the people listed here are added to every
  selected draft, all with the one access chosen.

  It adds, and never removes: each draft keeps the people it is shared with
  already, and someone listed here who is on a draft already gets the access
  chosen here (see mergeShareList in builder/bulk.js). The intro says so,
  as that can lower someone from Can edit to Can view. Nothing changes until
  the primary button is pressed, which is the one confirmation: a share can
  be undone.

  People are named through the user field the Share dialog has
  (BuilderUserCombobox.vue), from the users the first draft may be shared
  with: the drafts are all the user's own, so the same users go for each.
  When the users cannot be read, a username can still be typed, and the
  server checks it.

  The drafts are shared a few at a time (see shareDrafts in store.js), the
  status line saying how far it is; Cancel, Escape and a click outside do
  nothing meanwhile. When every draft was shared, the dialog closes and the
  page says so. When some were not, the form gives way to a summary of them,
  each with why, which takes focus, and Close: the page keeps those drafts
  selected, so Share selected tries them again.
-->
<template>
  <builder-dialog
    class="builder-bulk-share"
    :title="`Share ${count(items.length, 'draft')}`"
    title-id="bulk-share-title"
    describedby="bulk-share-intro"
    data-testid="bulk-share-dialog"
    @close="requestClose">
    <p id="bulk-share-intro" class="builder-hint">
      The people you add get access to every selected draft, with the access you
      choose. People who already have access keep it. If someone you add is
      already on a draft, their access becomes the one you choose here. No one
      is removed. They find the drafts under Shared Drafts.
    </p>
    <p v-if="leftOut" class="builder-hint" data-testid="bulk-share-left-out">
      {{ count(leftOut, 'damaged draft') }} in your selection cannot be shared
      and {{ leftOut === 1 ? 'is' : 'are' }} left out.
    </p>

    <template v-if="phase !== 'failed'">
      <form class="builder-bulk-share__add" novalidate @submit.prevent="add">
        <div class="builder-field builder-bulk-share__user">
          <label for="bulk-share-user">User</label>
          <builder-user-combobox
            id="bulk-share-user"
            ref="userField"
            v-model="addName"
            :users="users"
            :exclude="listed"
            :disabled="phase === 'running'"
            :invalid="Boolean(fieldError.text)"
            :describedby="userDescription"
            @update:model-value="fieldError.clear()"
            @submit="add" />
          <p
            id="bulk-share-users-note"
            class="builder-bulk-share__users-note"
            :class="{ 'builder-dialog__error': usersPhase === 'failed' }"
            role="status"
            data-testid="bulk-share-users-note">
            <template v-if="usersPhase === 'loading'">
              <span class="builder-toolbar__spinner" aria-hidden="true"></span>
              <span>Loading users…</span>
            </template>
            <template v-else-if="usersPhase === 'failed'">
              <span>{{ USERS_FAILED }}</span>
              <button
                type="button"
                class="builder-button"
                data-testid="bulk-share-users-retry"
                @click="retryUsers">
                Retry
              </button>
            </template>
          </p>
          <!-- An alert: Enter in the field leaves focus where it is, so the
               field's new description would not be read. -->
          <p
            id="bulk-share-user-error"
            class="builder-dialog__message builder-dialog__error"
            role="alert"
            data-testid="bulk-share-user-error">
            <span v-if="fieldError.text" :key="fieldError.key">{{
              fieldError.text
            }}</span>
          </p>
        </div>

        <button
          type="submit"
          class="builder-button builder-bulk-share__add-button"
          data-testid="bulk-share-add"
          :aria-disabled="phase !== 'ready' || undefined">
          <builder-icon name="plus" :size="14" />
          Add
        </button>
      </form>

      <div class="builder-field builder-bulk-share__access">
        <label for="bulk-share-access">Access</label>
        <select
          id="bulk-share-access"
          v-model="access"
          :disabled="phase === 'running'"
          data-testid="bulk-share-access">
          <option value="view">{{ ACCESS_LABELS.view }}</option>
          <option value="edit">{{ ACCESS_LABELS.edit }}</option>
        </select>
      </div>

      <h3 id="bulk-share-people-title">People to add</h3>
      <p
        v-if="!people.length"
        class="builder-bulk-share__empty"
        data-testid="bulk-share-empty">
        No one yet.
      </p>
      <ul
        v-else
        class="builder-bulk-share__people"
        aria-labelledby="bulk-share-people-title"
        data-testid="bulk-share-people">
        <li
          v-for="user in people"
          :key="user"
          class="builder-bulk-share__row"
          :data-testid="`bulk-share-row-${user}`">
          <span class="builder-bulk-share__name">{{ user }}</span>
          <button
            type="button"
            class="builder-button"
            :aria-label="`Remove ${user}`"
            :aria-disabled="phase !== 'ready' || undefined"
            :data-testid="`bulk-share-remove-${user}`"
            @click="remove(user)">
            Remove
          </button>
        </li>
      </ul>
    </template>

    <builder-bulk-summary
      v-if="result"
      ref="summary"
      :heading="result.heading"
      :items="result.items"
      :dismissible="false" />

    <!-- Who was added to the list, or removed from it. -->
    <p class="builder-visually-hidden" role="status">
      <span v-if="note.text" :key="note.key">{{ note.text }}</span>
    </p>

    <div class="builder-bulk-share__footer">
      <p
        class="builder-bulk-share__progress"
        role="status"
        data-testid="bulk-share-status">
        <template v-if="phase === 'running'">
          <span class="builder-toolbar__spinner" aria-hidden="true"></span>
          <span>Sharing {{ progress.done }} of {{ progress.total }}…</span>
        </template>
      </p>
      <div class="builder-dialog__actions">
        <button
          v-if="phase === 'failed'"
          type="button"
          class="builder-button"
          data-testid="bulk-share-close"
          @click="$emit('close')">
          Close
        </button>
        <template v-else>
          <button
            type="button"
            class="builder-button"
            data-testid="bulk-share-cancel"
            :aria-disabled="phase === 'running' || undefined"
            @click="requestClose">
            Cancel
          </button>
          <button
            type="button"
            class="builder-button builder-button--primary"
            data-testid="bulk-share-submit"
            :aria-disabled="phase !== 'ready' || !people.length || undefined"
            @click="share">
            {{
              phase === 'running'
                ? 'Sharing…'
                : `Share ${count(items.length, 'draft')}`
            }}
          </button>
        </template>
      </div>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref } from 'vue';

  import BuilderBulkSummary from '../BuilderBulkSummary.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import { cardKey } from '../BuilderDrafts.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderUserCombobox from '../BuilderUserCombobox.vue';
  import { useMessage } from './message.js';

  import { count, describeNames } from '@/builder/announce.js';
  import { MAX_SHARES } from '@/builder/api.js';
  import { bulkFailureHeading } from '@/builder/bulk.js';
  import { findUser } from '@/builder/combobox.js';
  import {
    ACCESS_LABELS,
    accessLabel,
    validateBulkAdd,
  } from '@/builder/share.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { usePhenixStore } from '@/store.js';

  const props = defineProps({
    // The drafts to share, as listed: all the user's own.
    items: { type: Array, required: true },
    // What each draft's card calls it, by cardKey, for the summary.
    names: { type: Object, default: () => ({}) },
    // How many damaged drafts of the selection are left out.
    leftOut: { type: Number, default: 0 },
  });

  // shared: the drafts that were shared, for the page to unselect.
  const emit = defineEmits(['close', 'shared']);

  const store = useBuilderStore();

  const USERS_FAILED =
    'Users could not be loaded. You can still type a username.';

  // ready, running, or failed: the run left some drafts as they were.
  const phase = ref('ready');
  const progress = reactive({ done: 0, total: 0 });
  // What the run left undone: { heading, items }.
  const result = ref(null);

  // The usernames to add, in the order they were added, and the one access
  // they all get.
  const people = ref([]);
  const access = ref('view');
  const addName = ref('');
  const fieldError = useMessage();
  const note = useMessage();

  const userField = ref(null);
  const summary = ref(null);

  const owner = computed(() => props.items[0]?.owner || '');

  // --- the users -------------------------------------------------------

  // The users the drafts may be shared with, once read; loading, failed or
  // ready.
  const users = ref(null);
  const usersPhase = ref('loading');

  // The owner, the user and anyone in the list already are not offered.
  const listed = computed(() => [
    owner.value,
    usePhenixStore().username,
    ...people.value,
  ]);
  // The usernames Add checks against, once they are known.
  const knownUsers = computed(() =>
    users.value ? users.value.map((user) => user.username) : null,
  );

  const userDescription = computed(() =>
    [
      usersPhase.value !== 'ready' ? 'bulk-share-users-note' : '',
      fieldError.text ? 'bulk-share-user-error' : '',
    ]
      .filter(Boolean)
      .join(' '),
  );

  async function loadUsers() {
    usersPhase.value = 'loading';

    try {
      users.value = await store.loadShareCandidates(props.items[0]);
      usersPhase.value = 'ready';
    } catch {
      usersPhase.value = 'failed';
    }
  }

  // Retry goes while the users are read, so focus moves to the field first.
  function retryUsers() {
    userField.value?.focus();
    loadUsers();
  }

  // --- the people to add -----------------------------------------------

  async function add() {
    if (phase.value !== 'ready') {
      return;
    }

    userField.value?.closeList();

    // A user chosen from the list is named by their label.
    const check = validateBulkAdd(
      findUser(users.value, addName.value)?.username ?? addName.value,
      people.value,
      owner.value,
      knownUsers.value,
      MAX_SHARES,
    );

    if (check.error) {
      fieldError.set(check.error, 'user');
      await nextTick();
      userField.value?.focus();

      return;
    }

    people.value = [...people.value, check.user];
    fieldError.clear();
    addName.value = '';
    note.set(`${check.user} added to the list.`);
    userField.value?.focus();
  }

  // The row goes, and its button with it, so focus moves to the field.
  async function remove(user) {
    if (phase.value !== 'ready') {
      return;
    }

    people.value = people.value.filter((name) => name !== user);
    note.set(`${user} removed from the list.`);
    await nextTick();
    userField.value?.focus();
  }

  // --- sharing ---------------------------------------------------------

  async function share() {
    if (phase.value !== 'ready' || !people.value.length) {
      return;
    }

    const total = props.items.length;

    phase.value = 'running';
    fieldError.clear();
    progress.done = 0;
    progress.total = total;

    const { done, failures } = await store.shareDrafts(
      props.items,
      people.value,
      access.value,
      {
        onProgress: (ended) => {
          progress.done = ended;
        },
      },
    );

    if (done.length) {
      emit('shared', done);
    }

    if (!failures.length) {
      store.announce(
        `Shared ${count(total, 'draft')} with ${describeNames(people.value)} (${accessLabel(access.value).toLowerCase()}).`,
      );
      emit('close');

      return;
    }

    result.value = {
      heading: bulkFailureHeading(
        failures.length,
        total,
        'draft',
        'drafts',
        'shared',
      ),
      items: failures.map(({ item, reason }) => ({
        key: cardKey(item),
        name: props.names[cardKey(item)] || item.title || item.id,
        reason,
      })),
    };
    phase.value = 'failed';
    await nextTick();
    summary.value?.focus();
  }

  // Cancel, Escape and a click outside do nothing while the drafts are
  // being shared: the run cannot be taken back half way.
  function requestClose() {
    if (phase.value !== 'running') {
      emit('close');
    }
  }

  onMounted(() => {
    userField.value?.focus();
    loadUsers();
  });
</script>

<style scoped>
  .builder-bulk-share h3 {
    font-size: 0.95rem;
    font-weight: 700;
    margin: 0.75rem 0 0.35rem;
  }

  .builder-bulk-share__add {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 0 0.5rem;
    align-items: start;
    margin-top: 0.75rem;
  }

  /* Add sits level with the field, under its label. */
  .builder-bulk-share__add-button {
    margin-top: 1.4rem;
    min-height: 2rem;
  }

  .builder-bulk-share__users-note {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
    margin: 0.25rem 0 0;
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-bulk-share__users-note.builder-dialog__error {
    color: var(--bx-danger);
  }

  .builder-bulk-share__users-note:empty {
    margin: 0;
  }

  .builder-bulk-share__access select {
    max-width: 12rem;
    min-height: 2rem;
  }

  .builder-bulk-share__empty {
    margin: 0;
    color: var(--bx-text-muted);
  }

  .builder-bulk-share__people {
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--bx-border);
  }

  .builder-bulk-share__row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-bulk-share__name {
    min-width: 0;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .builder-bulk-share__footer {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    margin-top: 0.75rem;
  }

  .builder-bulk-share__footer .builder-dialog__actions {
    margin-inline-start: auto;
  }

  .builder-bulk-share__progress {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0;
  }

  @media (max-width: 40rem) {
    .builder-bulk-share__add {
      grid-template-columns: minmax(0, 1fr);
    }

    .builder-bulk-share__add-button {
      justify-self: start;
      margin-top: 0;
    }
  }

  @media (pointer: coarse) {
    .builder-bulk-share .builder-button,
    .builder-bulk-share select {
      min-height: 44px;
    }
  }
</style>
