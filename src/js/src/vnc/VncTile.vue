<!-- A view-only live tile of one VM, for the VNC tab of an experiment and the
     VM Tiles page. The live tile pool decides when the tile holds a VNC
     connection. The tile shows its picture until the console draws, and the
     last frame after the pool takes the connection away. A click opens the
     console page. Key the component by its VM: the VM does not change. -->
<script setup>
  import { computed, onBeforeUnmount, ref, watch } from 'vue';
  import { useRouter } from 'vue-router';

  import { consoleHref } from './consoleUrl.js';
  import { useLiveTile } from './liveTilePool.js';
  import { handleConsoleLinkClick } from './openConsole.js';
  import { useVncSession } from './useVncSession.js';
  import { consoleAvailable, vmPaused } from './vmScreen.js';
  import VncConsole from './VncConsole.vue';

  const props = defineProps({
    experiment: { type: String, required: true },
    // the VM row: name, state, running and busy
    vm: { type: Object, required: true },
    // the screenshot or placeholder to show while no live screen shows
    picture: { type: String, required: true },
    // the text of the picture, when the tile has no console link
    alt: { type: String, required: true },
    // the text of the console link
    linkLabel: { type: String, required: true },
    // the tile width in pixels; without it the tile fills its parent
    width: { type: Number, default: null },
  });

  // live(true) when the tile starts to draw a live screen, live(false) when
  // it stops
  const emit = defineEmits(['live']);

  const router = useRouter();
  const key = `${props.experiment}/${props.vm.name}`;
  const vms = [key];

  const paused = computed(() => vmPaused(props.vm));
  // a paused VM keeps its tile live and frozen
  const available = computed(() =>
    consoleAvailable(props.experiment, props.vm),
  );

  const tile = useLiveTile(key, { available });
  const session = useVncSession({
    experiment: props.experiment,
    vm: props.vm.name,
    enabled: tile.live,
  });
  watch(paused, (value) => (session.paused.value = value), {
    immediate: true,
  });

  const screenConsole = ref(null);
  const drawn = ref(false);
  const lastFrame = ref(null);
  let disposed = false;

  function setLastFrame(url) {
    if (lastFrame.value) URL.revokeObjectURL(lastFrame.value);
    lastFrame.value = url;
  }

  // The canvas copies its pixels when toBlob is called, before the console
  // closes the connection.
  function keepLastFrame() {
    screenConsole.value?.toBlob().then((blob) => {
      if (blob && !disposed) setLastFrame(URL.createObjectURL(blob));
    });
  }

  watch(
    session.connection,
    (_, old) => {
      if (old && drawn.value) keepLastFrame();
      drawn.value = false;
    },
    { flush: 'sync' },
  );
  watch(
    () => props.picture,
    () => setLastFrame(null),
  );
  watch(available, (value) => value || setLastFrame(null));
  // the last live value the parent got: on unmount the pool closes the
  // connection first, so drawn is already false there
  let reported = false;
  watch(drawn, (value) => {
    reported = value;
    emit('live', value);
  });

  onBeforeUnmount(() => {
    disposed = true;
    setLastFrame(null);
    if (reported) emit('live', false);
  });

  function onConnect() {
    session.connected();
    drawn.value = true;
  }

  function onDisconnect() {
    drawn.value = false;
    session.disconnected();
  }

  const href = computed(() => consoleHref(router, vms));
  const shown = computed(() => lastFrame.value ?? props.picture);
  const tag = computed(() => {
    if (!available.value) return null;
    if (paused.value) return 'PAUSED';
    if (drawn.value) return 'LIVE';
    return tile.live.value ? null : 'Snapshot';
  });
  const style = computed(() =>
    props.width ? { width: `${props.width}px` } : null,
  );
</script>

<template>
  <div
    :ref="tile.el"
    :class="['vnc-tile', { 'is-live': drawn }]"
    :style="style">
    <div class="vnc-tile-screen">
      <img
        v-show="!drawn"
        class="vnc-tile-picture"
        :src="shown"
        :alt="available ? '' : alt" />
      <VncConsole
        v-if="tile.live.value"
        v-show="drawn"
        ref="screenConsole"
        :connection="session.connection.value"
        :label="`Live screen of VM ${vm.name} in experiment ${experiment}`"
        view-only
        :quality="3"
        :compression="6"
        @connect="onConnect"
        @disconnect="onDisconnect"
        @securityfailure="(reason) => session.securityFailure(reason)" />
      <a
        v-if="available"
        class="vnc-tile-link"
        :href="href"
        @click="(event) => handleConsoleLinkClick(event, vms)"
        ><span class="is-sr-only">{{ linkLabel }}</span></a
      >
      <span
        v-if="tag"
        :class="['tag', 'vnc-tile-tag', `vnc-tile-tag--${tag.toLowerCase()}`]"
        >{{ tag }}</span
      >
      <div class="vnc-tile-bar"><slot name="bar"></slot></div>
    </div>
  </div>
</template>

<style scoped>
  .vnc-tile {
    position: relative;
  }

  .vnc-tile-screen {
    position: relative;
    display: flex;
    aspect-ratio: 4 / 3;
    overflow: hidden;
    background-color: #000;
  }

  .vnc-tile-picture {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  /* the tile takes no input; a click goes to the console link */
  .vnc-tile-screen :deep(.vnc-console) {
    pointer-events: none;
  }

  /* the scaled screen fits; noVNC's own scroll box would be a scroll region
     that a keyboard cannot reach while the console is view only */
  :deep(.vnc-screen > div) {
    overflow: hidden !important;
  }

  .vnc-tile-link {
    position: absolute;
    inset: 0;
  }

  .vnc-tile-link:focus-visible {
    outline: 3px solid #62c0d7;
    outline-offset: -3px;
  }

  .vnc-tile-tag {
    position: absolute;
    right: 4px;
    bottom: 4px;
    pointer-events: none;
    font-weight: 700;
  }

  /* white on #0f5a2a is 8.3:1 */
  .vnc-tile-tag--live {
    background-color: #0f5a2a;
    color: #fff;
  }

  /* #1a1a1a on #ffd75e is 12.5:1 */
  .vnc-tile-tag--paused {
    background-color: #ffd75e;
    color: #1a1a1a;
  }

  /* white on #333 is 12.6:1 */
  .vnc-tile-tag--snapshot {
    background-color: #333;
    color: #fff;
  }

  .vnc-tile-bar {
    position: absolute;
    top: 4px;
    right: 4px;
    left: 4px;
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 4px;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.1s;
  }

  .vnc-tile-bar:empty {
    display: none;
  }

  .vnc-tile:hover .vnc-tile-bar,
  .vnc-tile:focus-within .vnc-tile-bar {
    opacity: 1;
    pointer-events: auto;
  }
</style>
