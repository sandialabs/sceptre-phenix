<!-- The Add VM panel of the console top bar: picks a running VM of any
     experiment to show in a new pane. -->
<script setup>
  import { computed, onMounted, ref } from 'vue';

  import { listAllVms } from '@/api/vms.js';
  import { useErrorNotification } from '@/utils/errorNotif';

  const props = defineProps({
    // the VMs that the console shows, as exp/vm
    shown: { type: Array, default: () => [] },
  });

  const emit = defineEmits(['add']);

  const vms = ref([]);
  const loading = ref(true);
  const search = ref('');

  const choices = computed(() => {
    const words = search.value.trim().toLowerCase();
    return vms.value
      .filter((vm) => vm.running)
      .map((vm) => `${vm.experiment}/${vm.name}`)
      .filter((key) => !props.shown.includes(key))
      .filter((key) => !words || key.toLowerCase().includes(words))
      .sort((a, b) => a.localeCompare(b));
  });

  onMounted(async () => {
    try {
      const { data } = await listAllVms();
      vms.value = data?.vms ?? [];
    } catch (err) {
      useErrorNotification(err);
    } finally {
      loading.value = false;
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
  </div>
</template>
