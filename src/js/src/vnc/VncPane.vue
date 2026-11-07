<!-- One pane of the console page: in split view a thin header with the VM
     name, state, a link to its details, pane number and menu; the top and bottom banners of the VM;
     the console; and the connection state over the screen when it is not
     connected. The page owns the VNC session and gives its values here. -->
<script setup>
  import { computed, onBeforeUnmount, onMounted, ref } from 'vue';

  import { vmDetailsRoute } from '@/components/experiment/experimentTabs.js';
  import { vmStateInfo } from '@/components/experiment/vmActions.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { attachChord } from './chord.js';
  import { connectionLabel, showsReconnect } from './connectionStatus.js';
  import VncBanner from './VncBanner.vue';
  import VncConsole from './VncConsole.vue';

  const props = defineProps({
    experiment: { type: String, required: true },
    vm: { type: String, required: true },
    // 0 for the first pane
    index: { type: Number, required: true },
    paneCount: { type: Number, default: 1 },
    split: { type: Boolean, default: false },
    // the top bar acts on this pane
    active: { type: Boolean, default: false },
    // the console of this pane has keyboard focus
    keyboard: { type: Boolean, default: false },
    connection: { type: Object, default: null },
    // { state, message, retrySeconds } of the VNC session
    status: { type: Object, required: true },
    // the session JSON, or null
    info: { type: Object, default: null },
    vmRow: { type: Object, default: null },
    viewOnly: { type: Boolean, default: false },
    scaling: { type: String, default: 'fit' },
    quality: { type: Number, default: 6 },
    compression: { type: Number, default: 2 },
    // the role may resume the VM
    canResume: { type: Boolean, default: false },
  });

  const emit = defineEmits([
    'activate',
    'keyboard',
    'connect',
    'disconnect',
    'securityfailure',
    'clipboard',
    'reconnect',
    'resume',
    'swap',
    'close',
    'open-alone',
    'fullscreen',
    'chord-leave',
    'chord-pane',
    'chord-exit-fullscreen',
  ]);

  const root = ref(null);
  const screenConsole = ref(null);
  const menu = ref(null);
  const menuButton = ref(null);
  const menuOpen = ref(false);
  const bell = ref(false);
  const desktopName = ref('');
  let bellTimer = null;
  let chord = null;

  const number = computed(() => props.index + 1);
  const label = computed(
    () => `Console of VM ${props.vm} in experiment ${props.experiment}`,
  );
  const state = computed(() => (props.vmRow ? vmStateInfo(props.vmRow) : null));
  // the VM details on the experiment page, for a role that may open it
  const detailsLink = computed(() =>
    roleAllowed('experiments', 'get', props.experiment)
      ? vmDetailsRoute(props.experiment, props.vm)
      : null,
  );
  const paused = computed(
    () => (props.vmRow?.state ?? '').toUpperCase() === 'PAUSED',
  );
  const connected = computed(() => props.status.state === 'connected');
  const statusLabel = computed(() => connectionLabel(props.status));
  const reconnect = computed(() => showsReconnect(props.status.state));
  const others = computed(() =>
    Array.from({ length: props.paneCount }, (_, i) => i).filter(
      (i) => i !== props.index,
    ),
  );
  const nameTitle = computed(() =>
    desktopName.value
      ? `VM ${props.vm} in experiment ${props.experiment}. Desktop: ${desktopName.value}`
      : `VM ${props.vm} in experiment ${props.experiment}`,
  );

  function onBell() {
    clearTimeout(bellTimer);
    bell.value = true;
    bellTimer = setTimeout(() => (bell.value = false), 300);
  }

  function onConnect() {
    emit('connect');
  }

  function onFocusIn(event) {
    emit('activate');
    if (event.target.closest('.vnc-console')) emit('keyboard', true);
  }

  function onFocusOut(event) {
    const into = event.relatedTarget;
    const consoleEl = root.value?.querySelector('.vnc-console');
    if (!into || !consoleEl?.contains(into)) emit('keyboard', false);
  }

  function closeMenu() {
    menuOpen.value = false;
  }

  function escapeMenu() {
    if (!menuOpen.value) return;
    closeMenu();
    menuButton.value?.focus();
  }

  // no relatedTarget: a click on a button that does not take focus (Safari)
  function onMenuFocusOut(event) {
    const into = event.relatedTarget;
    if (into && !menu.value?.contains(into)) closeMenu();
  }

  function onPointerDown(event) {
    if (menuOpen.value && !menu.value?.contains(event.target)) closeMenu();
  }

  onMounted(() => {
    document.addEventListener('pointerdown', onPointerDown);
    chord = attachChord(root.value, {
      onLeave: ({ backward }) => emit('chord-leave', backward),
      onSelectPane: (n) => emit('chord-pane', n),
      onExitFullscreen: () => emit('chord-exit-fullscreen'),
    });
  });

  onBeforeUnmount(() => {
    document.removeEventListener('pointerdown', onPointerDown);
    chord?.detach();
    clearTimeout(bellTimer);
  });

  /** Gives keyboard input to the console of the pane. */
  const focusConsole = () => screenConsole.value?.focus();

  defineExpose({
    focusConsole,
    element: () => root.value,
    sendKeys: (events) => screenConsole.value?.sendKeys(events),
    sendCtrlAltDel: () => screenConsole.value?.sendCtrlAltDel(),
    paste: (text) => screenConsole.value?.paste(text),
    screenSize: () =>
      screenConsole.value?.screenSize() ?? { width: 0, height: 0, scale: null },
  });
</script>

<template>
  <section
    ref="root"
    :class="['vnc-pane', { 'is-active': active, 'is-bell': bell }]"
    :aria-label="`Pane ${number}: ${vm}`"
    @pointerdown="emit('activate')"
    @focusin="onFocusIn"
    @focusout="onFocusOut">
    <div v-if="split" class="vnc-pane-header">
      <strong :title="nameTitle">{{ vm }}</strong
      >{{ ' '
      }}<b-tag v-if="state" :type="state.type ?? 'is-dark'" size="is-small">{{
        state.label
      }}</b-tag>
      <b-tooltip
        v-if="detailsLink"
        :label="`Open the details of VM ${vm} on the experiment page in a new tab`"
        type="is-dark"
        position="is-bottom">
        <router-link
          :to="detailsLink"
          target="_blank"
          rel="noopener"
          class="vnc-pane-details"
          :aria-label="`Details of VM ${vm}`">
          <b-icon icon="circle-info" size="is-small" />
        </router-link>
      </b-tooltip>
      <span v-if="keyboard" class="tag is-info is-small vnc-keyboard-tag"
        >Keyboard</span
      >
      <span class="vnc-fill"></span>
      <span class="vnc-pane-number">{{ number }}</span>
      <div
        ref="menu"
        class="vnc-pane-menu"
        @keydown.esc.stop="escapeMenu"
        @focusout="onMenuFocusOut">
        <button
          ref="menuButton"
          type="button"
          class="vnc-pane-menu-button"
          :aria-label="`Menu of pane ${number}`"
          :aria-expanded="String(menuOpen)"
          :aria-controls="`vnc-pane-menu-${number}`"
          @click="menuOpen = !menuOpen">
          <b-icon icon="ellipsis" size="is-small" />
        </button>
        <div
          v-if="menuOpen"
          :id="`vnc-pane-menu-${number}`"
          class="vnc-pane-menu-list"
          role="group"
          :aria-label="`Menu of pane ${number}`">
          <button
            v-for="other in others"
            :key="other"
            type="button"
            @click="
              closeMenu();
              emit('swap', other);
            ">
            Swap with pane {{ other + 1 }}
          </button>
          <button
            type="button"
            @click="
              closeMenu();
              emit('open-alone');
            ">
            Open alone
          </button>
          <button
            type="button"
            @click="
              closeMenu();
              emit('fullscreen');
            ">
            Full screen
          </button>
          <button
            type="button"
            @click="
              closeMenu();
              emit('close');
            ">
            Close
          </button>
        </div>
      </div>
    </div>
    <VncBanner :banner="info?.banners?.top" position="top" />
    <div class="vnc-pane-screen">
      <VncConsole
        ref="screenConsole"
        :connection="connection"
        :label="label"
        :view-only="viewOnly"
        :scaling="scaling"
        :quality="quality"
        :compression="compression"
        @connect="onConnect"
        @disconnect="emit('disconnect')"
        @securityfailure="emit('securityfailure', $event)"
        @clipboard="emit('clipboard', $event)"
        @bell="onBell"
        @desktopname="desktopName = $event" />
      <div v-if="!connected" class="vnc-pane-status">
        <p class="vnc-pane-status-label">{{ statusLabel }}</p>
        <p v-if="status.message && status.message !== statusLabel">
          {{ status.message }}
        </p>
        <b-button
          v-if="reconnect"
          size="is-small"
          icon-left="sync-alt"
          @click="emit('reconnect')">
          Reconnect
        </b-button>
      </div>
      <div v-else-if="paused" class="vnc-paused">
        <span>Paused</span>
        <b-button
          v-if="canResume"
          size="is-small"
          type="is-success"
          icon-left="play"
          @click="emit('resume')">
          Resume
        </b-button>
      </div>
    </div>
    <VncBanner :banner="info?.banners?.bottom" position="bottom" />
  </section>
</template>
