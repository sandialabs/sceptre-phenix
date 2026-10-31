<!-- The notice of the newer snapshots that the next change removes (see
     newerHistory.js). It is not modal and not a live region. -->
<template>
  <div
    v-if="newer"
    ref="rootEl"
    class="builder-panel builder-notice builder-newer-history"
    data-testid="builder-newer-history">
    <p>
      <builder-icon name="warning" :size="14" />
      <span data-testid="newer-history-text">{{ text }}</span>
    </p>
    <div class="builder-newer-history__actions">
      <builder-busy-button
        ref="latestButton"
        data-testid="newer-history-latest"
        :busy="working === 'latest'"
        :disabled="Boolean(working)"
        @click="run('latest')">
        Return to latest
      </builder-busy-button>
      <builder-busy-button
        v-if="!newer.saved"
        data-testid="newer-history-save"
        :busy="working === 'save'"
        :disabled="Boolean(working)"
        @click="run('save')">
        Save newer history as a new draft
      </builder-busy-button>
    </div>
  </div>
</template>

<script setup>
  import { computed, nextTick, ref, watch } from 'vue';

  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderIcon from './BuilderIcon.vue';

  import { formatTimestamp } from '@/builder/format.js';
  import { newerNotice } from '@/builder/newerHistory.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { usePhenixStore } from '@/store.js';

  const emit = defineEmits(['lost-focus']);

  const store = useBuilderStore();
  const phenix = usePhenixStore();

  const rootEl = ref(null);
  const latestButton = ref(null);
  // The action in progress: 'latest', 'save' or ''.
  const working = ref('');

  const newer = computed(() => store.newerHistory);
  const text = computed(() =>
    newer.value
      ? newerNotice(newer.value, {
          me: phenix.username || '',
          time: (value) => formatTimestamp(value),
        })
      : '',
  );

  // The draft's head, so the list is read again at most once for each.
  const headKey = computed(() => {
    const head = store.draftHead;

    return head
      ? `${store.draftId}:${head.snapshotId}:${head.cursor}:${head.snapshots}`
      : '';
  });

  // The draft while the server has snapshots after its cursor, or ''. An
  // undo hides the notice until its answer comes, but does not change this.
  const behindKey = computed(() => {
    const head = store.draftHead;

    return head && head.snapshots - 1 - head.cursor > 0 ? store.draftId : '';
  });

  let readFor = '';
  // The behindKey the notice was said for.
  let announcedFor = '';

  // Said once, when the notice shows, and again only for another draft or
  // after the draft has no newer snapshots. When the editor does not know
  // who saved the newest snapshot, the list is read first, so that is said
  // too.
  watch(
    [newer, behindKey],
    async ([now, behind]) => {
      if (!behind) {
        announcedFor = '';
      }

      if (!now) {
        return;
      }

      if (!now.newest && readFor !== headKey.value) {
        readFor = headKey.value;
        await store.readNewerHistory();
      }

      if (newer.value && behindKey.value !== announcedFor) {
        announcedFor = behindKey.value;
        store.announce(text.value, { slot: 'newer' });
      }
    },
    { immediate: true },
  );

  async function run(action) {
    if (working.value) {
      return;
    }

    working.value = action;

    try {
      if (action === 'latest') {
        await store.returnToLatest();
      } else {
        await store.saveNewerAsDraft();
      }
    } finally {
      working.value = '';
    }

    await nextTick();

    const focused = document.activeElement;

    if (rootEl.value?.contains(focused)) {
      return;
    }

    // The Save button is gone, so focus goes to Return to latest. A notice
    // that closed leaves focus to the view.
    if (rootEl.value && (!focused || focused === document.body)) {
      latestButton.value?.focus();
    } else if (!focused || focused === document.body) {
      emit('lost-focus');
    }
  }
</script>

<style scoped>
  .builder-newer-history__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
</style>
