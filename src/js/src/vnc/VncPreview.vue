<!-- The live console of the VM details modal (M3a): a 16:10 view-only
     console with the banners of the VM. "Take control" gives it keyboard and
     pointer input; the chord Ctrl+] then Tab leaves it. A click on the screen
     in view-only mode opens the console page. While the console is open, the
     tile of the VM in this tab is not live. Without a console, it shows the
     screenshot. -->
<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    ref,
    watch,
  } from 'vue';
  import { useRouter } from 'vue-router';

  import { attachChord, CHORD_HINT } from './chord.js';
  import { connectionLabel } from './connectionStatus.js';
  import { consoleHref } from './consoleUrl.js';
  import { sharedLiveTilePool } from './liveTilePool.js';
  import { handleConsoleLinkClick } from './openConsole.js';
  import { useVncSession } from './useVncSession.js';
  import { consoleAvailable, vmPaused } from './vmScreen.js';
  import VncBanner from './VncBanner.vue';
  import VncConsole from './VncConsole.vue';

  const props = defineProps({
    experiment: { type: String, required: true },
    // the VM row: name, state, running and busy
    vm: { type: Object, required: true },
    // the screenshot or placeholder to show without a live screen
    screenshot: { type: String, default: '' },
  });

  const router = useRouter();
  const key = computed(() => `${props.experiment}/${props.vm.name}`);
  const paused = computed(() => vmPaused(props.vm));
  const enabled = computed(() => consoleAvailable(props.experiment, props.vm));

  const session = useVncSession({
    experiment: () => props.experiment,
    vm: () => props.vm.name,
    enabled,
  });
  watch(paused, (value) => (session.paused.value = value), {
    immediate: true,
  });

  // one connection for each VM in the tab: the tile of the VM lets go
  let release = null;
  watch(
    [key, enabled],
    ([vmKey, on]) => {
      release?.();
      release = on ? sharedLiveTilePool().claim(vmKey) : null;
    },
    { immediate: true },
  );

  const screen = ref(null);
  const screenConsole = ref(null);
  const controlButton = ref(null);
  const drawn = ref(false);
  const control = ref(false);
  let chord = null;

  watch(session.connection, () => {
    drawn.value = false;
    control.value = false;
  });

  onMounted(() => {
    chord = attachChord(screen.value, {
      onLeave: () => controlButton.value?.focus(),
    });
  });

  onBeforeUnmount(() => {
    chord?.detach();
    release?.();
    release = null;
  });

  function onConnect() {
    session.connected();
    drawn.value = true;
  }

  function onDisconnect() {
    drawn.value = false;
    control.value = false;
    session.disconnected();
  }

  async function toggleControl() {
    control.value = !control.value;
    if (control.value) {
      await nextTick();
      screenConsole.value?.focus();
    }
  }

  const vms = computed(() => [key.value]);
  const href = computed(() => consoleHref(router, vms.value));
  const banners = computed(() => session.info.value?.banners ?? {});
  const status = computed(() =>
    connectionLabel({
      state: session.state.value,
      retrySeconds: session.retrySeconds.value,
    }),
  );
  // the reason, when the label does not say it already
  const message = computed(() =>
    session.message.value === status.value ? '' : session.message.value,
  );
</script>

<template>
  <div class="vnc-preview">
    <VncBanner v-if="enabled" :banner="banners.top ?? null" position="top" />
    <div ref="screen" class="vnc-preview-screen">
      <img
        v-show="!drawn"
        class="vnc-preview-shot"
        :src="screenshot"
        alt="VM screenshot" />
      <VncConsole
        v-if="enabled"
        v-show="drawn"
        ref="screenConsole"
        :connection="session.connection.value"
        :label="`Console of VM ${vm.name} in experiment ${experiment}`"
        :view-only="!control"
        @connect="onConnect"
        @disconnect="onDisconnect"
        @securityfailure="(reason) => session.securityFailure(reason)" />
      <a
        v-if="enabled && !control"
        class="vnc-preview-link"
        :href="href"
        @click="(event) => handleConsoleLinkClick(event, vms)"
        ><span class="is-sr-only">Open the console of VM {{ vm.name }}</span></a
      >
      <p v-if="enabled && !drawn" class="vnc-preview-status" role="status">
        {{ status }}<template v-if="message">: {{ message }}</template>
      </p>
      <div v-if="drawn" class="vnc-preview-bar">
        <span class="vnc-preview-mode">{{
          control ? 'Keyboard' : 'View only'
        }}</span>
        <span v-if="control" class="vnc-preview-hint">{{ CHORD_HINT }}</span>
        <button
          ref="controlButton"
          type="button"
          class="button is-small vnc-preview-control"
          :aria-pressed="String(control)"
          @click="toggleControl">
          <b-icon icon="keyboard" size="is-small" />
          <span>{{ control ? 'View only' : 'Take control' }}</span>
        </button>
      </div>
    </div>
    <VncBanner
      v-if="enabled"
      :banner="banners.bottom ?? null"
      position="bottom" />
  </div>
</template>

<style scoped>
  .vnc-preview {
    display: grid;
    gap: 4px;
    margin-bottom: 0.75rem;
  }

  .vnc-preview-screen {
    position: relative;
    display: flex;
    aspect-ratio: 16 / 10;
    overflow: hidden;
    border-radius: 8px;
    background-color: #000;
  }

  .vnc-preview-shot {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  /* the scaled screen fits; noVNC's own scroll box would be a scroll region
     that a keyboard cannot reach while the console is view only */
  :deep(.vnc-screen > div) {
    overflow: hidden !important;
  }

  .vnc-preview-link {
    position: absolute;
    inset: 0;
  }

  .vnc-preview-link:focus-visible {
    outline: 3px solid #62c0d7;
    outline-offset: -3px;
  }

  /* white on rgba(0, 0, 0, 0.75) over any screen is at least 9.9:1 */
  .vnc-preview-status {
    position: absolute;
    left: 0.5rem;
    bottom: 0.5rem;
    margin: 0 !important;
    padding: 0.2rem 0.6rem;
    border-radius: 4px;
    background-color: rgba(0, 0, 0, 0.75);
    color: #fff;
    font-size: 0.85rem;
  }

  .vnc-preview-bar {
    position: absolute;
    right: 0.5rem;
    bottom: 0.5rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.2rem 0.3rem 0.2rem 0.6rem;
    border-radius: 4px;
    background-color: rgba(0, 0, 0, 0.75);
    color: #fff;
    font-size: 0.85rem;
  }

  .vnc-preview-mode {
    font-weight: 700;
  }
</style>
