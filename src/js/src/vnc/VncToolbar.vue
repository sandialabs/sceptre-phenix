<!-- The narrow top bar of the console page (M2a). It acts on the pane that
     has keyboard input: links to its experiment and to the details of its
     VM, the VM actions that the role may take, the Keys, Clipboard and View
     panels (M6a), the layout picker, Add VM, full screen, and the
     connection indicator and Hide toolbar at the right end. The page fills
     the panels through the keys, clipboard, view and add slots. -->
<script setup>
  import { computed, onBeforeUnmount, onMounted, ref } from 'vue';

  import { vmDetailsRoute } from '@/components/experiment/experimentTabs.js';
  import { vmButtonStates } from '@/components/experiment/vmActions.js';
  import { usePhenixStore } from '@/store.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { CHORD_HINT } from './chord.js';
  import {
    CLIPBOARD_MODES,
    ENCODING,
    connectionLabel,
    connectionTone,
    detailsLine,
    scalingText,
    showsReconnect,
  } from './connectionStatus.js';
  import {
    LAYOUTS,
    LAYOUT_LABELS,
    LAYOUT_SLOTS,
    MAX_PANES,
  } from './consoleUrl.js';
  import { keyboardCaptureNote } from './fullscreen.js';

  const props = defineProps({
    experiment: { type: String, default: '' },
    vm: { type: String, default: '' },
    // the VM row of the pane (GET /experiments/{exp}/vms/{vm}), or null
    vmRow: { type: Object, default: null },
    // split view: the bar says which pane it acts on
    split: { type: Boolean, default: false },
    // the pane has keyboard input: the KEYBOARD tag shows (single view)
    keyboard: { type: Boolean, default: false },
    // { state, message, retrySeconds } of the VNC session of the pane
    status: {
      type: Object,
      default: () => ({ state: 'idle', message: '', retrySeconds: null }),
    },
    // { width, height, scale, scaling, quality, compression, clipboardMode }
    details: { type: Object, default: () => ({}) },
    layout: { type: String, default: 'single' },
    paneCount: { type: Number, default: 0 },
    // the VM action that runs, or null
    pending: { type: String, default: null },
    // the page is full screen: the full screen button exits it
    fullscreen: { type: Boolean, default: false },
  });

  const emit = defineEmits([
    'action',
    'layout',
    'fullscreen',
    'exit-fullscreen',
    'reconnect',
    'panel-close',
    'details',
    'hide',
  ]);

  const root = ref(null);
  const open = ref(null);

  // wide: the panel is as wide as its rows of buttons
  const PANELS = [
    { name: 'keys', label: 'Keys', icon: 'keyboard', caret: true, wide: true },
    { name: 'clipboard', label: 'Clipboard', icon: 'clipboard', wide: true },
    { name: 'view', label: 'View', icon: 'gear', caret: true },
  ];

  const hasVm = computed(() => Boolean(props.experiment && props.vm));

  // the experiment page and the VM details on it, for a role that may open it
  const experimentLink = computed(() =>
    props.experiment && roleAllowed('experiments', 'get', props.experiment)
      ? { name: 'experiment', params: { id: props.experiment } }
      : null,
  );
  const detailsLink = computed(() =>
    hasVm.value && experimentLink.value
      ? vmDetailsRoute(props.experiment, props.vm)
      : null,
  );

  const can = (resource, verb) =>
    roleAllowed(resource, verb, `${props.experiment}/${props.vm}`);

  // the power actions, the snapshots, the screenshot, the CD-ROM and the
  // port forward; those the role may not take are left out
  const actions = computed(() => {
    const row = props.vmRow;
    if (!row || !hasVm.value) return [];
    const names = [
      row.running ? 'pause' : 'start',
      'restart',
      'shutdown',
      'snapshot',
      'memorySnapshot',
      'screenshot',
      'cdrom',
      'portForward',
    ];
    return vmButtonStates(names, row, {
      can,
      features: usePhenixStore().features ?? [],
    }).filter((action) => action.permitted);
  });

  const layouts = computed(() =>
    LAYOUTS.map((name) => {
      const fits = LAYOUT_SLOTS[name] >= props.paneCount;
      return {
        name,
        label: LAYOUT_LABELS[name],
        pressed: props.layout === name,
        disabled: !fits,
        tip: fits
          ? LAYOUT_LABELS[name]
          : 'Cannot use this layout: close a pane first',
      };
    }),
  );

  const addDisabled = computed(() => props.paneCount >= MAX_PANES);

  const label = computed(() => connectionLabel(props.status));
  const tone = computed(() => connectionTone(props.status.state));
  const reconnect = computed(() => showsReconnect(props.status.state));
  const fullscreenNote = keyboardCaptureNote();
  const fullscreenTip = computed(() =>
    props.fullscreen
      ? 'Exit full screen'
      : (fullscreenNote ?? 'Show the console page full screen'),
  );

  const capitalized = (text) => text.charAt(0).toUpperCase() + text.slice(1);
  const SCALING_NAMES = {
    fit: 'Fit',
    actual: 'Actual size',
    remote: 'Resize VM',
  };

  const tooltip = computed(() =>
    props.details.scaling ? detailsLine(props.details) : label.value,
  );

  // the rows of the indicator popover
  const rows = computed(() => {
    const d = props.details;
    if (!d.scaling) return [];
    const list = [];
    if (d.width && d.height) {
      list.push(['Resolution', `${d.width}×${d.height}`]);
    }
    const scaled = d.scaling === 'fit' && d.scale ? `, ${scalingText(d)}` : '';
    list.push(['Scaling', `${SCALING_NAMES[d.scaling] ?? 'Fit'}${scaled}`]);
    list.push(['Encoding', ENCODING]);
    list.push(['Quality', String(d.quality)]);
    if (d.compression != null)
      list.push(['Compression', String(d.compression)]);
    const mode = CLIPBOARD_MODES[d.clipboardMode];
    if (mode) list.push(['Copy/paste', capitalized(mode)]);
    return list;
  });

  function toggle(name) {
    if (name === 'add' && addDisabled.value) return;
    open.value = open.value === name ? null : name;
    if (open.value === 'status') emit('details');
  }

  // Focus goes back to the panel button, and the page then moves it to the
  // console when the console can take it.
  function escape(event) {
    if (!open.value) return;
    open.value = null;
    event.currentTarget
      .querySelector('.vnc-panel-button, .vnc-status')
      ?.focus({ preventScroll: true });
    emit('panel-close');
  }

  function closePanels() {
    open.value = null;
  }

  /**
   * Opens one panel, as its button does.
   *
   * @param {'keys'|'clipboard'|'view'|'add'|'status'} name
   */
  function openPanel(name) {
    if (open.value !== name) toggle(name);
  }

  function onPointerDown(event) {
    if (open.value && !root.value?.contains(event.target)) closePanels();
  }

  onMounted(() => document.addEventListener('pointerdown', onPointerDown));
  onBeforeUnmount(() =>
    document.removeEventListener('pointerdown', onPointerDown),
  );

  /**
   * Moves focus into the bar: to its first button, or its last one.
   *
   * @param {{ last?: boolean }} [options]
   */
  function focusBar({ last = false } = {}) {
    const buttons = [...(root.value?.querySelectorAll('button') ?? [])].filter(
      (button) => !button.closest('.vnc-panel'),
    );
    (last ? buttons.at(-1) : buttons[0])?.focus();
  }

  defineExpose({ closePanels, focusBar, openPanel });
</script>

<template>
  <div
    ref="root"
    class="vnc-toolbar"
    role="group"
    aria-label="Console controls">
    <span class="vnc-brand">phēnix</span>
    <b-tooltip
      v-if="experimentLink"
      :label="`Open the page of experiment ${experiment} in a new tab`"
      type="is-dark"
      position="is-bottom">
      <b-button
        tag="router-link"
        :to="experimentLink"
        target="_blank"
        rel="noopener"
        size="is-small"
        class="vnc-experiment-link"
        icon-left="flask"
        :aria-label="`Experiment ${experiment}`" />
    </b-tooltip>
    <span class="vnc-target">
      <template v-if="hasVm">
        <span v-if="split" class="vnc-acting">acting on </span
        ><strong>{{ experiment }} / {{ vm }}</strong>
      </template>
      <template v-else>No VM</template>
    </span>
    <b-tooltip
      v-if="detailsLink && !split"
      :label="`Open the details of VM ${vm} on the experiment page in a new tab`"
      type="is-dark"
      position="is-bottom">
      <b-button
        tag="router-link"
        :to="detailsLink"
        target="_blank"
        rel="noopener"
        size="is-small"
        class="vnc-details-link"
        icon-left="circle-info"
        :aria-label="`Details of VM ${vm}`" />
    </b-tooltip>
    <b-tooltip
      v-if="keyboard"
      :label="CHORD_HINT"
      type="is-dark"
      position="is-bottom">
      <span class="tag is-info vnc-keyboard-tag">Keyboard</span>
    </b-tooltip>
    <span class="vnc-fill"></span>

    <div
      v-if="actions.length"
      class="vnc-vm-actions"
      role="group"
      aria-label="VM actions">
      <b-tooltip
        v-for="action in actions"
        :key="action.name"
        :label="action.reason || action.description"
        type="is-dark"
        position="is-bottom"
        multilined>
        <b-button
          size="is-small"
          :icon-left="action.icon"
          :aria-label="action.label"
          :aria-disabled="action.disabled ? 'true' : undefined"
          :loading="pending === action.name"
          :aria-busy="pending === action.name ? 'true' : undefined"
          @click="action.disabled || pending || emit('action', action.name)" />
      </b-tooltip>
    </div>

    <div
      v-for="panel in PANELS"
      :key="panel.name"
      class="vnc-panel-wrap"
      @keydown.esc="escape">
      <b-button
        size="is-small"
        class="vnc-panel-button"
        :icon-left="panel.icon"
        :icon-right="panel.caret ? 'caret-down' : undefined"
        :aria-expanded="String(open === panel.name)"
        :aria-controls="`vnc-panel-${panel.name}`"
        @click="toggle(panel.name)">
        {{ panel.label }}
      </b-button>
      <div
        v-show="open === panel.name"
        :id="`vnc-panel-${panel.name}`"
        :class="['vnc-panel', { 'vnc-panel--wide': panel.wide }]"
        role="dialog"
        :aria-label="panel.label">
        <slot :name="panel.name" :open="open === panel.name"></slot>
      </div>
    </div>

    <div class="vnc-layouts" role="group" aria-label="Layout">
      <b-tooltip
        v-for="item in layouts"
        :key="item.name"
        :label="item.tip"
        type="is-dark"
        position="is-bottom">
        <button
          type="button"
          :class="['vnc-layout-button', `vnc-layout-icon--${item.name}`]"
          :aria-label="item.label"
          :aria-pressed="String(item.pressed)"
          :aria-disabled="item.disabled ? 'true' : undefined"
          @click="item.disabled || item.pressed || emit('layout', item.name)">
          <i v-for="n in LAYOUT_SLOTS[item.name]" :key="n"></i>
        </button>
      </b-tooltip>
    </div>

    <div class="vnc-panel-wrap" @keydown.esc="escape">
      <b-tooltip
        :label="
          addDisabled
            ? 'Cannot add a VM: the console shows four VMs'
            : 'Add a VM in a new pane'
        "
        type="is-dark"
        position="is-bottom">
        <b-button
          size="is-small"
          class="vnc-panel-button"
          icon-left="plus"
          aria-label="Add VM"
          :aria-expanded="String(open === 'add')"
          aria-controls="vnc-panel-add"
          :aria-disabled="addDisabled ? 'true' : undefined"
          @click="toggle('add')">
          Add VM
        </b-button>
      </b-tooltip>
      <div
        v-show="open === 'add'"
        id="vnc-panel-add"
        class="vnc-panel vnc-panel--end"
        role="dialog"
        aria-label="Add VM">
        <slot name="add" :open="open === 'add'"></slot>
      </div>
    </div>

    <b-tooltip :label="fullscreenTip" type="is-dark" position="is-bottom">
      <b-button
        size="is-small"
        class="vnc-fullscreen-button"
        :icon-left="fullscreen ? 'compress' : 'expand'"
        :aria-label="fullscreen ? 'Exit full screen' : 'Full screen'"
        @click="fullscreen ? emit('exit-fullscreen') : emit('fullscreen')" />
    </b-tooltip>

    <div class="vnc-panel-wrap" @keydown.esc="escape">
      <b-tooltip :label="tooltip" type="is-dark" position="is-left">
        <button
          type="button"
          class="vnc-status"
          :aria-expanded="String(open === 'status')"
          aria-controls="vnc-panel-status"
          @pointerenter="emit('details')"
          @focus="emit('details')"
          @click="toggle('status')">
          <span :class="['vnc-dot', `is-${tone}`]" aria-hidden="true"></span
          >{{ label }}
        </button>
      </b-tooltip>
      <div
        v-show="open === 'status'"
        id="vnc-panel-status"
        class="vnc-panel vnc-panel--end"
        role="dialog"
        aria-label="Connection">
        <h2 class="vnc-panel-title">{{ label }}</h2>
        <p
          v-if="status.message && status.message !== label"
          class="vnc-panel-note">
          {{ status.message }}
        </p>
        <p v-for="[name, value] in rows" :key="name" class="vnc-detail">
          <strong>{{ name }}:</strong> {{ value }}
        </p>
        <b-button
          v-if="reconnect"
          size="is-small"
          type="is-link"
          icon-left="sync-alt"
          @click="emit('reconnect')">
          Reconnect
        </b-button>
      </div>
    </div>

    <b-tooltip
      label="Hide the toolbar. Ctrl+] then T shows it again."
      type="is-dark"
      position="is-left">
      <b-button
        size="is-small"
        class="vnc-hide-toolbar"
        icon-left="chevron-up"
        aria-label="Hide toolbar"
        @click="emit('hide')" />
    </b-tooltip>
  </div>
</template>
