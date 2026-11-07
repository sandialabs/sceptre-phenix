<!-- The Add VM panel of the console top bar: picks a running VM of any
     experiment to show in a new pane. It shows the VMs that were last read
     at once, and reads them again each time it opens. -->
<script setup>
  import { computed, onMounted, ref } from 'vue';

  import { listAllVms } from '@/api/vms.js';
  import { usePhenixStore } from '@/store.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import {
    cacheRunningVms,
    cachedRunningVms,
    runningVmKeys,
  } from './vmListCache.js';

  const props = defineProps({
    // the VMs that the console shows, as exp/vm
    shown: { type: Array, default: () => [] },
  });

  const emit = defineEmits(['add']);

  const username = usePhenixStore().username;
  const cached = cachedRunningVms(username);
  const running = ref(cached ?? []);
  // no list yet; with a cached list, refreshing shows the read instead
  const loading = ref(cached === null);
  const refreshing = ref(cached !== null);
  const search = ref('');

  const choices = computed(() => {
    const words = search.value.trim().toLowerCase();
    return running.value
      .filter((key) => !props.shown.includes(key))
      .filter((key) => !words || key.toLowerCase().includes(words));
  });

  onMounted(async () => {
    try {
      const { data } = await listAllVms();
      running.value = runningVmKeys(data?.vms);
      cacheRunningVms(username, running.value);
    } catch (err) {
      useErrorNotification(err);
    } finally {
      loading.value = false;
      refreshing.value = false;
    }
  });

  function pick(key) {
    if (!key) return;
    search.value = '';
    emit('add', key);
  }
</script>

<template>
  <div class="vnc-add-vm">
    <h2 class="vnc-panel-title">Add a VM</h2>
    <b-autocomplete
      v-model="search"
      aria-label="Running VM to add"
      :data="choices"
      :loading="loading"
      open-on-focus
      keep-first
      clear-on-select
      size="is-small"
      placeholder="experiment/VM"
      @select="pick">
      <template #empty>
        {{ loading ? 'Loading the VMs' : 'No other running VM' }}
      </template>
    </b-autocomplete>
    <p class="vnc-panel-hint vnc-add-vm-status" role="status">
      <template v-if="refreshing">
        <b-icon icon="sync-alt" size="is-small" custom-class="fa-spin" />
        Refreshing the list of VMs
      </template>
    </p>
  </div>
</template>
