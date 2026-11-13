<!-- The console page /vnc?vm=exp/vm&vm=…&layout=… (M1, M2a). It shows one to
     four VM consoles in one of the layouts, with the narrow top bar that
     acts on the pane that has keyboard input. The user can hide the top bar;
     a button at the same place as Hide toolbar then shows it again. There is no
     status bar and no leave-page warning. A pane says when its VM restarts
     for a redeploy or a disk reset, and when its VM was killed. -->
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

  import {
    createVmSnapshot,
    redeployVm,
    startVmActionsJob,
  } from '@/api/vms.js';
  import { captureSupport } from '@/components/experiment/experimentCaptures.js';
  import { minuteStamp } from '@/components/experiment/timestamp.js';
  import { useVmCaptures } from '@/components/experiment/useVmCaptures.js';
  import VmAnnotationsModal from '@/components/experiment/VmAnnotationsModal.vue';
  import { VM_ACTION_CONFIRMS } from '@/components/experiment/vmActions.js';
  import { saveScreenshot } from '@/components/experiment/vmTools.js';
  import VMLabelsModal from '@/components/VMLabelsModal.vue';
  import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
  import { usePhenixStore } from '@/store.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { useJobStore } from '@/utils/jobs.js';
  import {
    confirmOperation,
    notifyOutcome,
    outcomeFromJob,
    outcomeFromSettled,
  } from '@/utils/operations.js';
  import { pageTitle } from '@/utils/pageTitle.js';
  import {
    loadVncSettings,
    loadVncToolbarHidden,
    saveVncSettings,
    saveVncToolbarHidden,
  } from '@/utils/prefs.js';
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
  import VncForwardsPanel from '@/vnc/VncForwardsPanel.vue';
  import VncKeysMenu from '@/vnc/VncKeysMenu.vue';
  import VncPane from '@/vnc/VncPane.vue';
  import VncSettingsPanel from '@/vnc/VncSettingsPanel.vue';
  import VncToolbar from '@/vnc/VncToolbar.vue';
  import VncVmModals from '@/vnc/VncVmModals.vue';

  // the VM actions that open a dialog of VncVmModals
  const DIALOG_ACTIONS = [
    'cdrom',
    'portForward',
    'memorySnapshot',
    'commit',
    'redeploy',
  ];

  // what a pane says while its VM restarts for the action
  const RESTARTS = {
    redeploy: 'Redeploy restarts the VM.',
    resetDisk: 'Reset disk restarts the VM.',
  };
  // after the action, how many times and how far apart, in milliseconds,
  // the page tries to connect the console again
  const RESTART_TRIES = 6;
  const RESTART_RETRY_MS = 5000;
  // the session states of a connection that is under way
  const CONNECTING = new Set(['loading', 'connecting', 'checking']);

  // the session storage key: the chord toast shows once each browser session
  const CHORD_TOAST_KEY = 'phenix.vnc.chordHintShown';

  const VM_ACTION_DONE = {
    start: 'Started',
    pause: 'Paused',
    restart: 'Restarted',
    shutdown: 'Shut down',
    kill: 'Killed',
    resetDisk: 'Reset the disk of',
  };

  const route = useRoute();
  const router = useRouter();
  const store = usePhenixStore();
  const buefy = getCurrentInstance()?.proxy?.$buefy;

  const page = ref(null);
  const toolbar = ref(null);
  const showToolbarButton = ref(null);
  const vmModals = ref(null);
  // the user hid the top bar; the browser keeps the choice
  const toolbarHidden = ref(loadVncToolbarHidden());
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
        // the action that restarts the VM (see RESTARTS), or null
        restarting: null,
        restartTimer: null,
        // Kill on this page removed the VM
        killed: false,
        stopped: false,
        reload: row.reload,
        reloadCaptures: row.reloadCaptures,
        stop() {
          controller.stopped = true;
          clearTimeout(controller.restartTimer);
          release();
          session.dispose();
          row.dispose();
          scope.stop();
        },
      });
      // a closed or new connection has no key down in QEMU
      watch(session.connection, () => (controller.held = []));
      watch(session.state, (state) => {
        if (state !== 'connected' || !controller.restarting) return;
        clearTimeout(controller.restartTimer);
        controller.restarting = null;
      });
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

  // Sends one request for each name, then tells in one summary how they
  // went, offering to send again those that may work if retried.
  async function fanOut(names, verbPast, send, noun = 'VM') {
    const results = await Promise.allSettled(names.map(send));
    notifyOutcome(buefy, {
      verbPast,
      noun,
      items: outcomeFromSettled(names, results),
      onRetry: (retry) => fanOut(retry, verbPast, send, noun),
    });
  }

  const {
    capturePending,
    captureAllPending,
    handlePcap,
    captureAllInterfaces,
    stopAllCaptures,
  } = useVmCaptures({ fanOut, experimentOf: (vm) => vm.experiment });

  // the VM of a pane as the capture functions take it
  const captureTarget = (controller) => ({
    ...controller.row,
    name: controller.vm,
    experiment: controller.experiment,
  });

  // the action whose button spins for the pane, or null
  function pendingOf(controller) {
    if (!controller) return null;
    if (controller.pending) return controller.pending;
    if (capturePending.value[`${controller.vm}/0`]) return 'captureIface';
    if (captureAllPending.value[controller.vm]) {
      return (controller.row?.captures ?? []).length
        ? 'stopCaptures'
        : 'captureAll';
    }
    return null;
  }

  // the text of a pane while its VM restarts, or ''
  const restartText = (controller) => RESTARTS[controller.restarting] ?? '';

  // Connects the console again after a redeploy or a disk reset restarts the
  // VM: a few tries, RESTART_RETRY_MS apart, until the console connects.
  function reconnectAfterRestart(controller, tries = RESTART_TRIES) {
    clearTimeout(controller.restartTimer);
    if (controller.stopped) return;
    const state = controller.session.state;
    if (state === 'connected' || tries <= 0) {
      controller.restarting = null;
      return;
    }
    if (!CONNECTING.has(state)) controller.session.retryNow();
    controller.restartTimer = setTimeout(
      () => reconnectAfterRestart(controller, tries - 1),
      RESTART_RETRY_MS,
    );
  }

  async function snapshot(controller) {
    if (controller.pending) return;
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
    toolbar.value?.closePanels();
    // a dialog takes the keyboard from the fullscreen console
    await exitFullscreen().catch(() => {});
    vmModals.value?.open(name, controller);
  }

  // opens a dialog component of the experiment page for the pane's VM
  async function openModal(component, props, events = {}) {
    toolbar.value?.closePanels();
    await exitFullscreen().catch(() => {});
    buefy?.modal.open({
      component,
      trapFocus: true,
      hasModalCard: true,
      props,
      events,
    });
  }

  function openMount(controller) {
    return openModal(VMMountBrowserModal, {
      targetVm: controller.vm,
      targetExp: controller.experiment,
    });
  }

  function openLabels(controller) {
    return openModal(
      VMLabelsModal,
      {
        vmName: controller.vm,
        experiment: controller.experiment,
        tags: controller.row?.tags ?? {},
      },
      { saved: () => controller.reload() },
    );
  }

  function openAnnotations(controller) {
    return openModal(
      VmAnnotationsModal,
      {
        experiment: controller.experiment,
        vmName: controller.vm,
        running: Boolean(controller.row?.running),
      },
      { saved: () => controller.reload() },
    );
  }

  // Starts a capture on interface 0: in the format the dialog asks for when
  // minimega takes one, else at once.
  function startCapture(controller) {
    if (captureSupport(null, store.features).available) {
      return openDialog('captureIface', controller);
    }
    return captureOn(controller);
  }

  async function captureOn(controller, format) {
    await handlePcap(captureTarget(controller), 0, format);
    await controller.reloadCaptures();
  }

  async function captureAll(controller) {
    await captureAllInterfaces(captureTarget(controller));
    await controller.reloadCaptures();
  }

  async function stopCaptures(controller) {
    await stopAllCaptures(captureTarget(controller));
    await controller.reloadCaptures();
  }

  // Redeploys the VM with the settings of the dialog. The pane says that the
  // VM restarts until its console connects again.
  async function redeploy(controller, body) {
    if (controller.pending) return;
    controller.pending = 'redeploy';
    controller.restarting = 'redeploy';
    let redeployed = false;
    try {
      const { data } = await redeployVm(
        controller.experiment,
        controller.vm,
        body,
      );
      if (data?.name) controller.row = data;
      redeployed = true;
      buefy?.toast.open({
        message: escapeHTML(`Redeployed the ${controller.vm} VM`),
        type: 'is-success',
      });
    } catch (err) {
      useErrorNotification(err);
    } finally {
      controller.pending = null;
      if (redeployed) reconnectAfterRestart(controller);
      else controller.restarting = null;
    }
  }

  // after an add, the port forwards panel opens again with a fresh list
  const forwardsRefresh = ref(0);
  function dialogDone(action, controller) {
    if (action !== 'portForward') return;
    forwardsRefresh.value += 1;
    if (controller === active.value) toolbar.value?.openPanel('forwards');
  }

  // asks before an action of VM_ACTION_CONFIRMS but Start, which does not ask
  // for one VM; resolves true for the others
  async function confirmAction(name, controller) {
    const ask = name !== 'start' && VM_ACTION_CONFIRMS[name];
    if (!ask) return true;
    // a confirm dialog takes the keyboard from the fullscreen console
    await exitFullscreen().catch(() => {});
    return confirmOperation(buefy, { ...ask([controller.vm]), noun: 'VM' });
  }

  // the actions that need no confirm and no job, by name
  const DIRECT_ACTIONS = {
    screenshot,
    mount: openMount,
    labels: openLabels,
    annotations: openAnnotations,
    captureIface: startCapture,
    captureAll,
    stopCaptures,
  };

  async function runAction(name, controller = active.value) {
    if (!controller || pendingOf(controller)) return;
    if (DIRECT_ACTIONS[name]) return DIRECT_ACTIONS[name](controller);
    if (DIALOG_ACTIONS.includes(name)) return openDialog(name, controller);
    if (!(await confirmAction(name, controller))) return;
    if (name === 'snapshot') return snapshot(controller);
    return runJob(name, controller);
  }

  // Takes a power action in a background job; a retry does not ask again.
  // A pane shows that Reset disk restarts the VM, and that Kill removed it.
  async function runJob(name, controller) {
    if (controller.pending) return;
    controller.pending = name;
    if (RESTARTS[name]) controller.restarting = name;
    let succeeded = null;
    try {
      const job = await startVmActionsJob(controller.experiment, name, [
        controller.vm,
      ]);
      const done = await useJobStore().whenDone(job.id);
      succeeded = (done?.items ?? []).find(
        (item) => item.status === 'succeeded',
      );
      if (succeeded?.result) controller.row = succeeded.result;
      if (succeeded && name === 'kill') controller.killed = true;
      notifyOutcome(buefy, {
        verbPast: VM_ACTION_DONE[name],
        noun: 'VM',
        items: outcomeFromJob(done),
        onRetry: () => runJob(name, controller),
      });
    } catch (err) {
      useErrorNotification(err);
    } finally {
      controller.pending = null;
      if (controller.restarting === name) {
        if (succeeded) reconnectAfterRestart(controller);
        else controller.restarting = null;
      }
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

  // Hides or shows the top bar. Focus that was on a control of the bar moves
  // to the control that takes its place. The panes fill the space: noVNC
  // scales each screen when its element changes size.
  function setToolbarHidden(hidden, { focus = false } = {}) {
    if (hidden) toolbar.value?.closePanels();
    toolbarHidden.value = hidden;
    saveVncToolbarHidden(hidden);
    if (!focus) return;
    nextTick(() => {
      if (hidden) showToolbarButton.value?.focus();
      else toolbar.value?.focusBar({ last: true });
    });
  }

  // the chord Tab goes to the top bar, or to Show toolbar when it is hidden
  function leaveConsole(backward) {
    if (toolbarHidden.value) showToolbarButton.value?.focus();
    else toolbar.value?.focusBar({ last: backward });
  }

  function openAddPanel() {
    setToolbarHidden(false);
    nextTick(() => toolbar.value?.openPanel('add'));
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

  // the last pane left fills the page
  function close(key) {
    const keys = vms.value.filter((vm) => vm !== key);
    show(keys, keys.length === 1 ? 'single' : layout.value);
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
  <div ref="page" :class="['vnc-page', { 'is-toolbar-hidden': toolbarHidden }]">
    <b-tooltip
      v-if="toolbarHidden"
      label="Show the toolbar (Ctrl+] then T)"
      type="is-dark"
      position="is-left"
      class="vnc-show-toolbar-tip">
      <button
        ref="showToolbarButton"
        type="button"
        class="button is-small vnc-show-toolbar"
        aria-label="Show toolbar"
        @click="setToolbarHidden(false, { focus: true })">
        <b-icon icon="chevron-down" size="is-small" />
      </button>
    </b-tooltip>
    <VncToolbar
      v-show="!toolbarHidden"
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
      :pending="pendingOf(active)"
      :fullscreen="inFullscreen"
      @action="runAction"
      @layout="(name) => show(vms, name)"
      @fullscreen="fullscreen(page)"
      @exit-fullscreen="exitFullscreen().catch(() => {})"
      @reconnect="active?.session.retryNow()"
      @panel-close="focusActive"
      @details="refreshScreen"
      @hide="setToolbarHidden(true, { focus: true })">
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
      <template #forwards="{ open }">
        <VncForwardsPanel
          v-if="active"
          :experiment="active.experiment"
          :vm="active.vm"
          :vm-row="active.row"
          :open="open"
          :refresh="forwardsRefresh"
          @add="runAction('portForward')" />
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
        :restarting="restartText(controller)"
        :killed="controller.killed"
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
        @chord-leave="leaveConsole"
        @chord-pane="focusPane"
        @chord-exit-fullscreen="exitFullscreen"
        @chord-toggle-toolbar="setToolbarHidden(!toolbarHidden)" />
      <div v-for="n in emptySlots" :key="`empty-${n}`" class="vnc-empty-pane">
        <p>Empty pane</p>
        <b-button size="is-small" icon-left="plus" @click="openAddPanel">
          Add VM
        </b-button>
      </div>
    </div>
    <div class="is-sr-only" aria-live="polite">{{ live }}</div>
    <VncVmModals
      ref="vmModals"
      @done="dialogDone"
      @redeploy="redeploy"
      @capture="captureOn" />
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
    position: relative;
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
      // the right padding keeps room for Hide toolbar: a small button of
      // 2.5em at 0.75rem, and the gap
      padding: 0.3rem calc(0.6rem + 1.875rem + 0.4rem) 0.3rem 0.6rem;
      background: $vnc-bar;
    }

    // Hide toolbar and Show toolbar share this place at the top right, also
    // when the bar wraps. Hide toolbar stays last in the focus order.
    .vnc-hide-toolbar-tip {
      position: absolute;
      top: 0.3rem;
      right: 0.6rem;
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

    .vnc-pane-status a {
      color: $vnc-ring;
      text-decoration: underline;
    }

    .vnc-addresses {
      margin: 0 0 0.4rem;
      font-size: 0.8rem;

      li {
        display: flex;
        gap: 0.4rem;
        align-items: center;
        margin-bottom: 0.2rem;
      }
    }

    .vnc-address {
      font-family: monospace;
    }

    .vnc-vm-title {
      margin-top: 0.6rem;
    }

    // the Actions menu matches the panels of the bar
    .vnc-actions-menu {
      .dropdown-content {
        background: $vnc-panel;
        border: 1px solid $vnc-line;
      }

      .dropdown-divider {
        background: $vnc-line;
      }

      .dropdown-item {
        padding-right: 1rem;
        color: #fff;
        white-space: nowrap;

        &:hover,
        &:focus-visible {
          color: #fff;
          background: $vnc-ground;
        }

        &:focus-visible {
          outline: 2px solid $vnc-ring;
        }
      }

      .vnc-actions-item--danger,
      .vnc-actions-item--danger:hover,
      .vnc-actions-item--danger:focus-visible {
        color: #ffb3b3;
      }

      // the tooltip's trigger fills the row, so the whole row shows it
      .vnc-actions-tip,
      .vnc-actions-tip .tooltip-trigger {
        display: flex;
        gap: 0.5rem;
        align-items: center;
        width: 100%;
      }
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

    // Show toolbar and Hide toolbar share this place at the top right
    .vnc-show-toolbar-tip {
      position: absolute;
      top: 0.3rem;
      right: 0.6rem;
      z-index: 20;
    }

    // dim until the pointer or focus is on it, so it hides little of a screen
    .vnc-show-toolbar {
      opacity: 0.6;

      &:hover,
      &:focus-visible {
        opacity: 1;
      }
    }

    // the header of the top right pane leaves room for Show toolbar
    &.is-toolbar-hidden {
      .vnc-layout--rows > .vnc-pane:first-child,
      .vnc-layout--cols > .vnc-pane:nth-child(2),
      .vnc-layout--main-2 > .vnc-pane:nth-child(2),
      .vnc-layout--grid > .vnc-pane:nth-child(2) {
        .vnc-pane-header {
          padding-right: 3rem;
        }
      }
    }
  }

  .vnc-page:fullscreen {
    background: $vnc-ground;
  }
</style>
