<!-- The console page /vnc?vm=exp/vm&vm=…&layout=… (M1, M2a). It shows one to
     four VM consoles in one of the layouts, with the narrow top bar that
     acts on the pane that has keyboard input. There is no status bar and no
     leave-page warning. -->
<script setup>
  import {
    computed,
    effectScope,
    getCurrentInstance,
    nextTick,
    onBeforeUnmount,
    onMounted,
    reactive,
    ref,
    shallowReactive,
    watch,
  } from 'vue';
  import { useRoute, useRouter } from 'vue-router';
  import { ToastProgrammatic as Toast } from 'buefy';

  import { createVmSnapshot, startVmActionsJob } from '@/api/vms.js';
  import { minuteStamp } from '@/components/experiment/timestamp.js';
  import { saveScreenshot } from '@/components/experiment/vmTools.js';
  import { usePhenixStore } from '@/store.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { useJobStore } from '@/utils/jobs.js';
  import {
    confirmOperation,
    notifyOutcome,
    outcomeFromJob,
  } from '@/utils/operations.js';
  import { pageTitle } from '@/utils/pageTitle.js';
  import { loadVncSettings, saveVncSettings } from '@/utils/prefs.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { CHORD_HINT } from '@/vnc/chord.js';
  import { announcement } from '@/vnc/connectionStatus.js';
  import {
    LAYOUT_SLOTS,
    consoleLocation,
    defaultLayout,
    layoutFits,
    parseConsoleQuery,
    splitVmKey,
  } from '@/vnc/consoleUrl.js';
  import {
    enterFullscreen,
    exitFullscreen,
    keepNoticesInFullscreen,
  } from '@/vnc/fullscreen.js';
  import { holdEvents, keyEvents, releaseEvents } from '@/vnc/keysyms.js';
  import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
  import {
    answerConsoleQueries,
    detachOpener,
    openConsole,
  } from '@/vnc/openConsole.js';
  import { useVmRow } from '@/vnc/useVmRow.js';
  import { useVncSession } from '@/vnc/useVncSession.js';
  import VncAddVm from '@/vnc/VncAddVm.vue';
  import VncClipboardPanel from '@/vnc/VncClipboardPanel.vue';
  import VncKeysMenu from '@/vnc/VncKeysMenu.vue';
  import VncPane from '@/vnc/VncPane.vue';
  import VncSettingsPanel from '@/vnc/VncSettingsPanel.vue';
  import VncToolbar from '@/vnc/VncToolbar.vue';
  import VncVmModals from '@/vnc/VncVmModals.vue';

  // the VM actions that open a dialog of VncVmModals
  const DIALOG_ACTIONS = ['cdrom', 'portForward', 'memorySnapshot'];

  // the session storage key: the chord toast shows once each browser session
  const CHORD_TOAST_KEY = 'phenix.vnc.chordHintShown';

  const VM_ACTION_DONE = {
    start: 'Started',
    pause: 'Paused',
    restart: 'Restarted',
    shutdown: 'Shut down',
  };

  const route = useRoute();
  const router = useRouter();
  const store = usePhenixStore();
  const buefy = getCurrentInstance()?.proxy?.$buefy;

  const page = ref(null);
  const toolbar = ref(null);
  const vmModals = ref(null);
  // an element of the page is full screen
  const inFullscreen = ref(false);
  const paneRefs = new Map();

  const parsed = computed(() => parseConsoleQuery(route.query));
  const vms = computed(() => parsed.value.vms);
  const layout = computed(() => parsed.value.layout);
  const split = computed(
    () => vms.value.length > 1 || layout.value !== 'single',
  );
  const emptySlots = computed(() =>
    vms.value.length
      ? Math.max(0, LAYOUT_SLOTS[layout.value] - vms.value.length)
      : 0,
  );

  // The URL is the source of the panes and the layout. A bad value is
  // dropped from it, so a reload or a copied link shows the same panes.
  function show(keys, nextLayout = layout.value) {
    const fits = layoutFits(nextLayout, keys.length);
    return router.replace(
      consoleLocation(keys, fits ? nextLayout : defaultLayout(keys.length)),
    );
  }

  watch(
    () => route.fullPath,
    () => {
      const fixed = consoleLocation(vms.value, layout.value);
      if (router.resolve(fixed).fullPath !== route.fullPath) {
        router.replace(fixed);
      }
    },
    { immediate: true },
  );

  // The session, VM row and input state of each pane, by exp/vm. A pane
  // keeps them while it moves to another place.
  const controllers = shallowReactive(new Map());

  function createController(key) {
    const { experiment, vm } = splitVmKey(key);
    const scope = effectScope(true);
    return scope.run(() => {
      const session = useVncSession({ experiment, vm });
      const row = useVmRow(experiment, vm);
      // one connection for each VM in the tab: the tile of the VM lets go
      const release = sharedLiveTilePool().claim(key);
      watch(
        row.vm,
        (value) => {
          if (value) {
            session.paused.value =
              (value.state ?? '').toUpperCase() === 'PAUSED';
          }
        },
        { immediate: true },
      );
      const controller = reactive({
        key,
        experiment,
        vm,
        session,
        row: row.vm,
        held: [],
        viewOnly: false,
        clipboard: '',
        // the VM action that runs on this VM, or null
        pending: null,
        stop() {
          release();
          session.dispose();
          row.dispose();
          scope.stop();
        },
      });
      // a closed or new connection has no key down in QEMU
      watch(session.connection, () => (controller.held = []));
      return controller;
    });
  }

  watch(
    vms,
    (keys) => {
      for (const [key, controller] of controllers) {
        if (!keys.includes(key)) {
          controller.stop();
          controllers.delete(key);
          paneRefs.delete(key);
        }
      }
      for (const key of keys) {
        if (!controllers.has(key)) controllers.set(key, createController(key));
      }
    },
    { immediate: true },
  );

  const panes = computed(() => vms.value.map((key) => controllers.get(key)));

  // the pane that the top bar acts on, and the pane whose console has focus
  const activeKey = ref(vms.value[0] ?? null);
  const keyboardKey = ref(null);
  watch(vms, (keys) => {
    if (!keys.includes(activeKey.value)) activeKey.value = keys[0] ?? null;
    if (!keys.includes(keyboardKey.value)) keyboardKey.value = null;
  });
  const active = computed(() =>
    activeKey.value ? controllers.get(activeKey.value) : null,
  );

  const statusOf = (controller) => ({
    state: controller.session.state,
    message: controller.session.message,
    retrySeconds: controller.session.retrySeconds,
  });

  // the settings of the View panel, kept for each user
  const settings = reactive(loadVncSettings(store.username));
  watch(settings, (value) => saveVncSettings(store.username, value));

  // Resize VM needs vga virtio; other panes fit their screens
  const scalingOf = (controller) =>
    settings.scaling === 'remote' &&
    !controller.session.info?.display?.remoteResize
      ? 'fit'
      : settings.scaling;

  const screen = ref({ width: 0, height: 0, scale: null });
  function refreshScreen() {
    const pane = active.value && paneRefs.get(active.value.key);
    screen.value = pane?.screenSize() ?? { width: 0, height: 0, scale: null };
  }

  const details = computed(() => {
    const controller = active.value;
    if (!controller) return {};
    return {
      ...screen.value,
      scaling: scalingOf(controller),
      quality: settings.quality,
      compression: settings.compression,
      clipboardMode: controller.session.info?.clipboard?.mode ?? null,
    };
  });

  const inputReason = computed(() => {
    const controller = active.value;
    if (!controller) return 'No VM to send to';
    if (controller.viewOnly) return 'Turn off View only to send input';
    if (controller.session.state !== 'connected') {
      return 'The console is not connected';
    }
    return null;
  });

  // the aria-live region tells each change of a pane state
  const live = ref('');
  watch(
    () => panes.value.map((c) => c && `${c.key}:${c.session.state}`),
    (now, before) => {
      const changed = panes.value[now.findIndex((e, i) => e !== before[i])];
      if (changed) live.value = announcement(changed.vm, statusOf(changed));
    },
  );

  // after each navigation too: router.afterEach sets the route title
  watch(
    () => [vms.value.join(', '), route.fullPath],
    ([names]) => {
      document.title = pageTitle(names, 'VNC console');
    },
    { immediate: true, flush: 'post' },
  );

  function setPaneRef(key, pane) {
    if (pane) paneRefs.set(key, pane);
    else paneRefs.delete(key);
  }

  function activate(key) {
    activeKey.value = key;
  }

  function chordToastShown() {
    try {
      if (sessionStorage.getItem(CHORD_TOAST_KEY)) return true;
      sessionStorage.setItem(CHORD_TOAST_KEY, '1');
    } catch {
      // a session that cannot keep it shows the toast each time
    }
    return false;
  }

  function setKeyboard(key, on) {
    if (on) {
      keyboardKey.value = key;
      activeKey.value = key;
      if (!chordToastShown()) {
        new Toast().open({ message: escapeHTML(CHORD_HINT), type: 'is-info' });
      }
    } else if (keyboardKey.value === key) {
      keyboardKey.value = null;
    }
  }

  function focusPane(n) {
    const key = vms.value[n - 1];
    if (!key) return;
    activeKey.value = key;
    paneRefs.get(key)?.focusConsole();
  }

  // a console that is not connected leaves focus on the panel button
  function focusActive() {
    nextTick(() => {
      const controller = active.value;
      if (controller?.session.state !== 'connected') return;
      paneRefs.get(controller.key)?.focusConsole();
    });
  }

  const can = (controller, resource, verb) =>
    roleAllowed(resource, verb, `${controller.experiment}/${controller.vm}`);

  async function snapshot(controller) {
    // a confirm dialog takes the keyboard from the fullscreen console
    await exitFullscreen().catch(() => {});
    const confirmed = await confirmOperation(buefy, {
      title: 'Create a VM Snapshot',
      message: escapeHTML(
        `This will create a snapshot of the ${controller.vm} VM.`,
      ),
      confirmText: 'Create',
      type: 'is-success',
      noun: 'VM',
    });
    if (!confirmed) return;
    controller.pending = 'snapshot';
    try {
      await createVmSnapshot(
        controller.experiment,
        controller.vm,
        minuteStamp(),
      );
      buefy?.toast.open({
        message: escapeHTML(`Snapshotted the ${controller.vm} VM`),
        type: 'is-success',
      });
    } catch (err) {
      useErrorNotification(err);
    } finally {
      controller.pending = null;
    }
  }

  async function screenshot(controller) {
    controller.pending = 'screenshot';
    try {
      await saveScreenshot(buefy, controller.experiment, controller.vm);
    } finally {
      controller.pending = null;
    }
  }

  async function openDialog(name, controller) {
    // a dialog takes the keyboard from the fullscreen console
    await exitFullscreen().catch(() => {});
    vmModals.value?.open(name, controller);
  }

  async function runAction(name, controller = active.value) {
    if (!controller || controller.pending) return;
    if (name === 'snapshot') return snapshot(controller);
    if (name === 'screenshot') return screenshot(controller);
    if (DIALOG_ACTIONS.includes(name)) return openDialog(name, controller);
    controller.pending = name;
    try {
      const job = await startVmActionsJob(controller.experiment, name, [
        controller.vm,
      ]);
      const done = await useJobStore().whenDone(job.id);
      const succeeded = (done?.items ?? []).find(
        (item) => item.status === 'succeeded' && item.result,
      );
      if (succeeded) controller.row = succeeded.result;
      notifyOutcome(buefy, {
        verbPast: VM_ACTION_DONE[name],
        noun: 'VM',
        items: outcomeFromJob(done),
        onRetry: () => runAction(name, controller),
      });
    } catch (err) {
      useErrorNotification(err);
    } finally {
      controller.pending = null;
    }
  }

  function sendKey(name) {
    const pane = active.value && paneRefs.get(active.value.key);
    if (!pane || inputReason.value) return;
    if (name === 'ctrlAltDel') pane.sendCtrlAltDel();
    else pane.sendKeys(keyEvents(name));
  }

  function holdKey(name) {
    const controller = active.value;
    const pane = controller && paneRefs.get(controller.key);
    if (!pane || inputReason.value) return;
    const pressed = !controller.held.includes(name);
    pane.sendKeys(holdEvents(name, pressed));
    controller.held = pressed
      ? [...controller.held, name]
      : controller.held.filter((held) => held !== name);
  }

  function releaseKeys() {
    const controller = active.value;
    const pane = controller && paneRefs.get(controller.key);
    if (!pane || inputReason.value) return;
    pane.sendKeys(releaseEvents());
    controller.held = [];
  }

  function paste(text) {
    const pane = active.value && paneRefs.get(active.value.key);
    if (pane && !inputReason.value) pane.paste(text);
  }

  function fullscreen(el) {
    if (!el) return;
    enterFullscreen(el).catch((err) =>
      console.warn('the browser refused full screen', err),
    );
  }

  function addVm(key) {
    toolbar.value?.closePanels();
    if (vms.value.includes(key)) return;
    activeKey.value = key;
    show([...vms.value, key]);
  }

  function swap(index, other) {
    const keys = [...vms.value];
    [keys[index], keys[other]] = [keys[other], keys[index]];
    show(keys);
  }

  function close(key) {
    show(vms.value.filter((vm) => vm !== key));
  }

  function openAlone(key) {
    openConsole(router.resolve(consoleLocation([key])).href, [key]);
  }

  function followFullscreen() {
    inFullscreen.value = Boolean(document.fullscreenElement);
  }

  let stopAnswering = () => {};
  let stopNotices = () => {};
  onMounted(() => {
    detachOpener();
    stopAnswering = answerConsoleQueries(() => vms.value);
    stopNotices = keepNoticesInFullscreen();
    document.addEventListener('fullscreenchange', followFullscreen);
    followFullscreen();
  });

  onBeforeUnmount(() => {
    stopAnswering();
    stopNotices();
    document.removeEventListener('fullscreenchange', followFullscreen);
    for (const controller of controllers.values()) controller.stop();
    controllers.clear();
  });
</script>

<template>
  <div ref="page" class="vnc-page">
    <VncToolbar
      ref="toolbar"
      :experiment="active?.experiment ?? ''"
      :vm="active?.vm ?? ''"
      :vm-row="active?.row ?? null"
      :split="split"
      :keyboard="!split && keyboardKey !== null"
      :status="active ? statusOf(active) : undefined"
      :details="details"
      :layout="layout"
      :pane-count="vms.length"
      :pending="active?.pending ?? null"
      :fullscreen="inFullscreen"
      @action="runAction"
      @layout="(name) => show(vms, name)"
      @fullscreen="fullscreen(page)"
      @exit-fullscreen="exitFullscreen().catch(() => {})"
      @reconnect="active?.session.retryNow()"
      @panel-close="focusActive"
      @details="refreshScreen">
      <template #keys>
        <VncKeysMenu
          :held="active?.held ?? []"
          :disabled-reason="inputReason"
          @send="sendKey"
          @hold="holdKey"
          @release="releaseKeys" />
      </template>
      <template #clipboard>
        <VncClipboardPanel
          :mode="active?.session.info?.clipboard?.mode ?? null"
          :received="active?.clipboard ?? ''"
          :disabled-reason="inputReason"
          @paste="paste" />
      </template>
      <template #view>
        <VncSettingsPanel
          v-model:scaling="settings.scaling"
          v-model:quality="settings.quality"
          v-model:compression="settings.compression"
          :view-only="active?.viewOnly ?? false"
          :remote-resize="Boolean(active?.session.info?.display?.remoteResize)"
          @update:view-only="(on) => active && (active.viewOnly = on)"
          @fullscreen="
            fullscreen(active && paneRefs.get(active.key)?.element())
          " />
      </template>
      <template #add="{ open }">
        <VncAddVm v-if="open" :shown="vms" @add="addVm" />
      </template>
    </VncToolbar>

    <p v-if="!vms.length" class="vnc-no-vm">
      No VM to show. Use Add VM to open the console of a running VM.
    </p>
    <div v-else :class="['vnc-panes', `vnc-layout--${layout}`]">
      <VncPane
        v-for="(controller, index) in panes"
        :key="controller.key"
        :ref="(pane) => setPaneRef(controller.key, pane)"
        :experiment="controller.experiment"
        :vm="controller.vm"
        :index="index"
        :pane-count="vms.length"
        :split="split"
        :active="split && controller.key === activeKey"
        :keyboard="controller.key === keyboardKey"
        :connection="controller.session.connection"
        :status="statusOf(controller)"
        :info="controller.session.info"
        :vm-row="controller.row"
        :view-only="controller.viewOnly"
        :scaling="scalingOf(controller)"
        :quality="settings.quality"
        :compression="settings.compression"
        :can-resume="can(controller, 'vms/start', 'update')"
        @activate="activate(controller.key)"
        @keyboard="(on) => setKeyboard(controller.key, on)"
        @connect="controller.session.connected()"
        @disconnect="controller.session.disconnected()"
        @securityfailure="
          (reason) => controller.session.securityFailure(reason)
        "
        @clipboard="(text) => (controller.clipboard = text)"
        @reconnect="controller.session.retryNow()"
        @resume="runAction('start', controller)"
        @swap="(other) => swap(index, other)"
        @close="close(controller.key)"
        @open-alone="openAlone(controller.key)"
        @fullscreen="fullscreen(paneRefs.get(controller.key)?.element())"
        @chord-leave="(backward) => toolbar?.focusBar({ last: backward })"
        @chord-pane="focusPane"
        @chord-exit-fullscreen="exitFullscreen" />
      <div v-for="n in emptySlots" :key="`empty-${n}`" class="vnc-empty-pane">
        <p>Empty pane</p>
        <b-button
          size="is-small"
          icon-left="plus"
          @click="toolbar?.openPanel('add')">
          Add VM
        </b-button>
      </div>
    </div>
    <div class="is-sr-only" aria-live="polite">{{ live }}</div>
    <VncVmModals ref="vmModals" />
  </div>
</template>

<style lang="scss">
  // Not scoped: these rules style the top bar, the panels and the panes,
  // all inside .vnc-page. Text is 7:1 or more on each surface.
  $vnc-ground: #1a1d22;
  $vnc-bar: #20252b;
  $vnc-panel: #2b3038;
  $vnc-line: #6b7280;
  $vnc-ring: #62c0d7;

  .vnc-page {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    width: 100%;
    min-height: 0;
    color: #fff;
    background: $vnc-ground;

    .vnc-toolbar {
      position: relative;
      z-index: 20;
      display: flex;
      flex: none;
      flex-wrap: wrap;
      gap: 0.4rem;
      align-items: center;
      padding: 0.3rem 0.6rem;
      background: $vnc-bar;
    }

    .vnc-brand {
      font-weight: 700;
    }

    .vnc-acting {
      color: #e5e7eb;
    }

    .vnc-keyboard-tag {
      text-transform: uppercase;
    }

    .vnc-fill {
      flex: 1 1 auto;
    }

    .vnc-vm-actions,
    .vnc-layouts {
      display: flex;
      gap: 0.25rem;
      align-items: center;
    }

    .vnc-panel-wrap,
    .vnc-pane-menu {
      position: relative;
    }

    .vnc-panel {
      position: absolute;
      top: calc(100% + 4px);
      left: 0;
      z-index: 30;
      width: max-content;
      min-width: 14rem;
      max-width: 24rem;
      padding: 0.75rem;
      background: $vnc-panel;
      border: 1px solid $vnc-line;
      border-radius: 4px;
      box-shadow: 0 6px 18px rgba(0, 0, 0, 0.5);
    }

    .vnc-panel--end {
      right: 0;
      left: auto;
    }

    // as wide as its longest row of buttons, which stays on one line
    .vnc-panel--wide {
      max-width: none;
    }

    .vnc-panel-row--single {
      flex-wrap: nowrap;
    }

    .vnc-panel-title {
      margin-bottom: 0.3rem;
      font-size: 0.75rem;
      font-weight: 700;
      text-transform: uppercase;
    }

    .vnc-panel-row {
      display: flex;
      flex-wrap: wrap;
      gap: 0.25rem;
      align-items: center;
      margin-bottom: 0.5rem;
    }

    .vnc-panel-label {
      font-size: 0.8rem;
    }

    .vnc-panel-hint,
    .vnc-panel-note,
    .vnc-detail {
      margin-bottom: 0.4rem;
      font-size: 0.8rem;
    }

    .vnc-panel-warning {
      margin-bottom: 0.4rem;
      font-size: 0.8rem;
      font-weight: 700;
      color: #ffd166;
    }

    .vnc-panel-field {
      display: flex;
      flex-direction: column;
      margin-bottom: 0.5rem;
      font-size: 0.8rem;
    }

    .vnc-clipboard-text {
      display: block;
      width: 100%;
      min-width: 28rem;
      margin-bottom: 0.5rem;
    }

    .vnc-layout-button {
      display: grid;
      gap: 2px;
      width: 28px;
      height: 22px;
      padding: 2px;
      cursor: pointer;
      background: transparent;
      border: 1px solid $vnc-line;
      border-radius: 3px;

      i {
        background: #9ca3af;
      }

      &[aria-pressed='true'] {
        border-color: $vnc-ring;

        i {
          background: $vnc-ring;
        }
      }

      &[aria-disabled='true'] {
        cursor: not-allowed;
        opacity: 0.45;
      }

      &:focus-visible {
        outline: 2px solid $vnc-ring;
      }
    }

    .vnc-layout-icon--cols {
      grid-template-columns: 1fr 1fr;
    }

    .vnc-layout-icon--rows {
      grid-template-rows: 1fr 1fr;
    }

    .vnc-layout-icon--main-2 {
      grid-template-rows: 1fr 1fr;
      grid-template-columns: 2fr 1fr;

      i:first-child {
        grid-row: span 2;
      }
    }

    .vnc-layout-icon--grid {
      grid-template-rows: 1fr 1fr;
      grid-template-columns: 1fr 1fr;
    }

    .vnc-status {
      display: inline-flex;
      gap: 6px;
      align-items: center;
      padding: 2px 10px;
      font-size: 0.85rem;
      color: #fff;
      cursor: pointer;
      background: transparent;
      border: 1px solid $vnc-line;
      border-radius: 999px;

      &:focus-visible {
        outline: 2px solid $vnc-ring;
      }
    }

    .vnc-dot {
      width: 8px;
      height: 8px;
      background: #9ca3af;
      border-radius: 50%;

      &.is-ok {
        background: #48c78e;
      }

      &.is-busy {
        background: $vnc-ring;
      }

      &.is-wait {
        background: #ffd166;
      }

      &.is-error {
        background: #f87171;
      }
    }

    .vnc-panes {
      display: grid;
      flex: 1 1 auto;
      gap: 3px;
      min-height: 0;
      padding: 3px;
    }

    .vnc-layout--single {
      grid-template: minmax(0, 1fr) / minmax(0, 1fr);
    }

    .vnc-layout--cols {
      grid-template: minmax(0, 1fr) / repeat(2, minmax(0, 1fr));
    }

    .vnc-layout--rows {
      grid-template: repeat(2, minmax(0, 1fr)) / minmax(0, 1fr);
    }

    .vnc-layout--main-2 {
      grid-template: repeat(2, minmax(0, 1fr)) / minmax(0, 2fr) minmax(0, 1fr);

      > :first-child {
        grid-row: span 2;
      }
    }

    .vnc-layout--grid {
      grid-template: repeat(2, minmax(0, 1fr)) / repeat(2, minmax(0, 1fr));
    }

    .vnc-pane {
      display: flex;
      flex-direction: column;
      min-width: 0;
      min-height: 0;
      background: #000;
      border: 2px solid transparent;

      &.is-active {
        border-color: $vnc-ring;
      }

      &.is-bell {
        border-color: #ffd166;
      }

      &:fullscreen {
        border: 0;
      }
    }

    .vnc-pane-header {
      display: flex;
      flex: none;
      gap: 0.4rem;
      align-items: center;
      padding: 2px 8px;
      font-size: 0.8rem;
      background: $vnc-bar;
    }

    .vnc-pane-menu-button {
      padding: 0 4px;
      color: #fff;
      cursor: pointer;
      background: transparent;
      border: 0;

      &:focus-visible {
        outline: 2px solid $vnc-ring;
      }
    }

    .vnc-pane-details {
      display: inline-flex;
      align-items: center;
      padding: 0 4px;
      color: #fff;

      &:focus-visible {
        outline: 2px solid $vnc-ring;
      }
    }

    .vnc-pane-menu-list {
      position: absolute;
      top: 100%;
      right: 0;
      z-index: 25;
      display: flex;
      flex-direction: column;
      min-width: 11rem;
      padding: 0.25rem 0;
      background: $vnc-panel;
      border: 1px solid $vnc-line;
      border-radius: 4px;

      button {
        padding: 0.3rem 0.75rem;
        color: #fff;
        text-align: left;
        cursor: pointer;
        background: transparent;
        border: 0;

        &:hover,
        &:focus-visible {
          background: #3b414b;
        }
      }
    }

    .vnc-pane-screen {
      position: relative;
      display: flex;
      flex: 1 1 auto;
      min-height: 0;
    }

    .vnc-pane-status {
      position: absolute;
      inset: 0;
      display: flex;
      flex-direction: column;
      gap: 0.5rem;
      align-items: center;
      justify-content: center;
      padding: 1rem;
      text-align: center;
      background: rgba(0, 0, 0, 0.85);
    }

    .vnc-pane-status-label {
      font-weight: 700;
    }

    .vnc-paused {
      position: absolute;
      top: 8px;
      right: 8px;
      display: flex;
      gap: 0.5rem;
      align-items: center;
      padding: 4px 8px;
      font-weight: 700;
      background: $vnc-bar;
      border-radius: 4px;
    }

    .vnc-empty-pane {
      display: flex;
      flex-direction: column;
      gap: 0.5rem;
      align-items: center;
      justify-content: center;
      border: 1px dashed $vnc-line;
    }

    .vnc-no-vm {
      padding: 2rem;
      text-align: center;
    }
  }

  .vnc-page:fullscreen {
    background: $vnc-ground;
  }
</style>
