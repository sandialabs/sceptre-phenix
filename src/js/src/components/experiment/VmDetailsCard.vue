<template>
  <div class="modal-card vm-card">
    <header
      class="modal-card-head vm-card-head"
      :class="[stateClass, { 'has-description': vm.description }]">
      <div class="vm-card-heading">
        <div class="vm-card-title-row">
          <p class="modal-card-title vm-card-title">
            {{ vm.name || 'unknown' }}
          </p>
          <b-tag :type="state.type" rounded class="vm-state-tag">
            <b-icon :icon="state.icon" size="is-small" />
            <span>{{ state.label }}</span>
          </b-tag>
          <b-tag v-if="vm.dnb" rounded type="is-dark">do not boot</b-tag>
        </div>
        <div class="vm-card-meta">
          <span v-if="vm.host" title="Host">
            <b-icon icon="server" size="is-small" />{{ vm.host }}
          </span>
          <span v-if="vm.running" title="Uptime">
            <b-icon icon="clock" size="is-small" />up
            {{ formatUptime(vm.uptime) }}
          </span>
          <span v-if="vm.delayed_start" title="Delayed start">
            <b-icon icon="hourglass-half" size="is-small" />starts after
            {{ vm.delayed_start }}
          </span>
        </div>
      </div>
      <p
        v-if="vm.description"
        class="vm-card-description"
        :title="vm.description">
        {{ vm.description }}
      </p>
      <!-- tooltips on the page body, which the card would clip -->
      <div class="vm-card-links">
        <template v-if="canViewExperiment">
          <b-tooltip
            label="view soh"
            type="is-dark"
            position="is-bottom"
            append-to-body>
            <router-link
              class="button is-light is-small vm-card-soh"
              aria-label="view soh"
              :to="{ name: 'soh', params: { id: experiment } }">
              <b-icon icon="heartbeat" />
            </router-link>
          </b-tooltip>
          <b-tooltip
            label="view scorch"
            type="is-dark"
            position="is-bottom"
            append-to-body>
            <router-link
              class="button is-light is-small vm-card-scorch"
              aria-label="view scorch"
              :to="{ name: 'scorchruns', params: { id: experiment } }">
              <b-icon icon="fire" />
            </router-link>
          </b-tooltip>
        </template>
        <b-tooltip
          label="VM documentation"
          type="is-dark"
          position="is-left"
          append-to-body>
          <a
            class="button is-light is-small vm-card-docs"
            aria-label="VM documentation"
            :href="docsPage('vms')"
            target="_blank"
            rel="noopener">
            <b-icon icon="book" />
          </a>
        </b-tooltip>
      </div>
    </header>

    <section class="modal-card-body vm-card-body">
      <div v-if="vm.busy" class="vm-busy">
        <span>Busy with another action</span>
        <b-progress
          size="is-small"
          type="is-warning"
          show-value
          :value="vm.percent || 0"
          format="percent" />
      </div>

      <div v-if="power.length" class="vm-section vm-power">
        <p class="vm-section-title">Power</p>
        <div class="vm-power-buttons">
          <b-tooltip
            v-for="action in power"
            :key="action.name"
            :label="action.reason || action.description || action.label"
            type="is-dark"
            multilined>
            <b-button
              :class="['vm-action', `vm-action-${action.name}`]"
              :type="action.type"
              :outlined="action.outlined"
              :icon-left="action.icon"
              :disabled="action.disabled"
              expanded
              @click="$emit('action', action.name)">
              {{ action.label }}
            </b-button>
          </b-tooltip>
        </div>
      </div>

      <div class="columns vm-columns">
        <div class="column is-5">
          <div v-if="noScreen" class="vm-no-screen">
            <b-icon :icon="state.icon" size="is-large" />
            <p>No screen: the VM is {{ state.label.toLowerCase() }}</p>
          </div>
          <figure v-else class="vm-screenshot">
            <a
              v-if="vncHref"
              :href="vncHref"
              target="_blank"
              title="Open the VM's console in a new tab">
              <img :src="screenshot" alt="VM screenshot" />
              <span class="vm-screenshot-hint">
                <b-icon icon="up-right-from-square" size="is-small" />
                Open console
              </span>
            </a>
            <img v-else :src="screenshot" alt="VM screenshot" />
          </figure>

          <div v-if="tools.length" class="vm-section">
            <p class="vm-section-title">More actions</p>
            <div class="vm-tool-buttons">
              <b-tooltip
                v-for="action in tools"
                :key="action.name"
                :label="action.reason || action.description || action.label"
                type="is-dark"
                multilined>
                <b-button
                  :class="['vm-action', `vm-action-${action.name}`]"
                  type="is-light"
                  size="is-small"
                  :icon-left="action.icon"
                  :disabled="action.disabled"
                  expanded
                  @click="$emit('action', action.name)">
                  {{ action.label }}
                </b-button>
              </b-tooltip>
            </div>
          </div>
        </div>

        <div class="column">
          <div class="vm-stats">
            <div class="vm-stat" :class="agentClass">
              <b-icon icon="plug" />
              <div>
                <p class="vm-stat-value">
                  {{ vm.ccActive ? 'Active' : 'None' }}
                </p>
                <p class="vm-stat-label">miniccc agent</p>
              </div>
            </div>
            <div class="vm-stat">
              <b-icon icon="microchip" />
              <div>
                <p class="vm-stat-value">{{ vm.cpus ?? '?' }}</p>
                <p class="vm-stat-label">
                  {{ pluralWord(vm.cpus, 'vCPU') }}
                </p>
              </div>
            </div>
            <div class="vm-stat">
              <b-icon icon="memory" />
              <div>
                <p class="vm-stat-value">
                  {{ vm.ram ? formatRAM(vm.ram) : '?' }}
                </p>
                <p class="vm-stat-label">Memory</p>
              </div>
            </div>
            <div class="vm-stat">
              <b-icon icon="network-wired" />
              <div>
                <p class="vm-stat-value">{{ interfaces.length }}</p>
                <p class="vm-stat-label">
                  {{ pluralWord(interfaces.length, 'Interface') }}
                </p>
              </div>
            </div>
          </div>

          <div class="vm-section">
            <p class="vm-section-title">
              Storage
              <b-tooltip
                v-if="canListDisks"
                class="vm-section-link"
                label="Go to the Disks page"
                type="is-dark"
                position="is-left">
                <router-link
                  class="button is-light is-small vm-card-disks"
                  aria-label="Go to the Disks page"
                  :to="{ name: 'disks' }">
                  <b-icon icon="hdd" size="is-small" />
                </router-link>
              </b-tooltip>
            </p>
            <div class="vm-kv">
              <span class="vm-kv-key">Disk</span>
              <span class="vm-kv-value vm-mono" :title="vm.disk">
                {{
                  diskLabel(disk) || (vm.disk && getBaseName(vm.disk)) || 'none'
                }}
              </span>
              <DiskWarning
                v-if="disk && disk.outsideFilesDir"
                reason="outside"
                :files-dir="filesDir" />
              <b-tooltip
                :label="
                  vm.snapshot
                    ? 'Disk writes go to a temporary overlay; the image is unchanged'
                    : 'Disk writes go straight to the disk image'
                "
                type="is-dark"
                multilined>
                <b-tag
                  :type="vm.snapshot ? 'is-success' : 'is-danger'"
                  class="vm-snapshot-tag">
                  snapshot {{ vm.snapshot ? 'on' : 'off' }}
                </b-tag>
              </b-tooltip>
            </div>
            <ol
              v-if="backingChain && backingChain.length"
              class="vm-backing-chain"
              aria-label="Backing image chain">
              <li
                v-for="image in backingChain"
                :key="image"
                class="vm-mono"
                :title="`backed by ${image}`">
                <span class="vm-chain-arrow" aria-hidden="true">&darr;</span>
                <span class="vm-chain-name">{{ image }}</span>
              </li>
            </ol>
            <div v-if="vm.cdRom" class="vm-kv vm-cdrom">
              <span class="vm-kv-key">CD-ROM</span>
              <span class="vm-kv-value vm-mono" :title="vm.cdRom">
                {{ diskLabel(cdRomDisk) || getBaseName(vm.cdRom) }}
              </span>
              <DiskWarning
                v-if="cdRomDisk && cdRomDisk.outsideFilesDir"
                reason="outside"
                :files-dir="filesDir" />
            </div>
          </div>

          <div class="vm-section">
            <p class="vm-section-title">
              Network
              <span v-if="captureCount" class="vm-capture-count">
                <span class="vm-rec-dot"></span>
                {{ plural(captureCount, 'capture') }} running
              </span>
              <span class="vm-section-actions">
                <b-tooltip
                  v-for="action in captureActions"
                  :key="action.name"
                  :label="action.reason || action.description"
                  type="is-dark"
                  position="is-top"
                  append-to-body
                  multilined>
                  <b-button
                    size="is-small"
                    :class="`vm-${action.name}`"
                    :type="action.buttonType"
                    :icon-left="action.buttonIcon"
                    :disabled="action.disabled || captureAllPending"
                    :loading="action.loading"
                    :aria-label="action.label"
                    @click="$emit('action', action.name)" />
                </b-tooltip>
                <b-tooltip
                  v-if="canOpenWebShark"
                  :label="webSharkTab.label"
                  type="is-dark"
                  position="is-top"
                  append-to-body
                  multilined>
                  <b-button
                    tag="router-link"
                    :to="webSharkTab.to"
                    size="is-small"
                    type="is-light"
                    class="vm-webshark-tab"
                    icon-left="search"
                    :aria-label="webSharkTab.label" />
                </b-tooltip>
              </span>
            </p>
            <p v-if="!interfaces.length" class="vm-empty">No interfaces</p>
            <div
              v-for="iface in interfaces"
              :key="iface.index"
              class="vm-iface"
              :class="{
                'is-capturing': iface.capturing,
                'is-disconnected': iface.disconnected,
                'has-webshark': canOpenWebShark,
              }">
              <span class="vm-iface-index">{{ iface.index }}</span>
              <div class="vm-iface-net">
                <b-tooltip
                  v-if="canChangeVlan"
                  label="change vlan"
                  type="is-dark">
                  <a class="vm-iface-name" @click="$emit('change-vlan', iface)">
                    {{ iface.network }}
                  </a>
                </b-tooltip>
                <span v-else class="vm-iface-name">{{ iface.network }}</span>
                <b-tag v-if="iface.vlan" type="is-info">
                  VLAN {{ iface.vlan }}
                </b-tag>
              </div>
              <span class="vm-iface-ip vm-mono">{{ iface.ip || '—' }}</span>
              <span class="vm-iface-tap vm-mono" title="tap">
                {{ iface.tap || '' }}
              </span>
              <span class="vm-iface-cap">
                <b-tooltip
                  v-if="canCapture && !iface.disconnected"
                  :label="
                    iface.capturing
                      ? 'stop packet capture'
                      : 'start packet capture'
                  "
                  type="is-dark">
                  <b-button
                    size="is-small"
                    :class="[
                      'vm-capture',
                      iface.capturing ? 'vm-capture-stop' : 'vm-capture-start',
                    ]"
                    :type="iface.capturing ? 'is-danger' : 'is-light'"
                    :icon-left="iface.capturing ? 'stop' : captureIcon"
                    :loading="capturePending.includes(iface.index)"
                    :aria-busy="capturePending.includes(iface.index)"
                    :aria-label="
                      iface.capturing
                        ? 'stop packet capture'
                        : 'start packet capture'
                    "
                    @click="$emit('capture', iface.index)" />
                </b-tooltip>
                <b-tooltip
                  v-else-if="iface.capturing"
                  label="packet capture running"
                  type="is-dark">
                  <span class="vm-rec-dot"></span>
                </b-tooltip>
                <!-- not the capture icon, which starts captures here -->
                <b-tooltip
                  v-if="canOpenWebShark && iface.capturing"
                  label="Open in WebShark"
                  type="is-dark">
                  <b-button
                    tag="router-link"
                    :to="webSharkLink(iface)"
                    size="is-small"
                    type="is-light"
                    class="vm-webshark"
                    icon-left="search"
                    aria-label="Open in WebShark" />
                </b-tooltip>
              </span>
            </div>
          </div>

          <div class="vm-section">
            <p class="vm-section-title">
              Labels
              <a class="vm-section-link" @click="$emit('edit-labels')">
                <b-icon icon="edit" size="is-small" /> view/edit
              </a>
            </p>
            <b-taglist v-if="tags.length">
              <div
                v-for="[key, value] in tags"
                :key="key"
                class="tags has-addons vm-label">
                <span class="tag is-dark">{{ key }}</span>
                <span class="tag is-primary">{{ value }}</span>
              </div>
            </b-taglist>
            <p v-else class="vm-empty">No labels</p>
          </div>

          <div v-if="annotationsLoaded" class="vm-section vm-annotations">
            <!-- while editing, the list's own label, with its help, heads it -->
            <p v-if="!annotationRows" class="vm-section-title">
              Annotations
              <a
                v-if="canEditAnnotations"
                class="vm-section-link vm-edit-annotations"
                @click="editAnnotations">
                <b-icon icon="edit" size="is-small" /> edit
              </a>
            </p>
            <template v-if="annotationRows">
              <annotation-list
                v-model="annotationRows"
                id-prefix="vm-annotations"
                label="Annotations"
                :help="annotationHelp"
                :catalog="NODE_ANNOTATIONS"
                :custom="CUSTOM_NODE_ANNOTATION" />
              <div class="vm-annotation-buttons">
                <b-button size="is-small" @click="annotationRows = null">
                  Cancel
                </b-button>
                <b-button
                  size="is-small"
                  type="is-success"
                  class="vm-save-annotations"
                  :disabled="!annotationsValid"
                  :loading="annotationsSaving"
                  @click="saveAnnotations">
                  Save
                </b-button>
              </div>
            </template>
            <div v-else-if="annotations.length" class="vm-annotation-list">
              <template v-for="[key, value] in annotations" :key="key">
                <code class="vm-annotation-key" :title="key">{{ key }}</code>
                <span
                  v-if="isSecret(key) && !revealed.includes(key)"
                  class="vm-kv-value vm-mono">
                  ••••••
                  <a
                    class="vm-reveal"
                    :aria-label="`Show ${key}`"
                    @click="revealed = [...revealed, key]">
                    <b-icon icon="eye" size="is-small" />
                  </a>
                </span>
                <span
                  v-else
                  class="vm-kv-value vm-mono"
                  :title="annotationText(value)">
                  {{ annotationText(value) }}
                </span>
              </template>
            </div>
            <p v-else class="vm-empty">No annotations</p>
          </div>

          <div class="vm-lists">
            <div v-if="canListSnapshots" class="vm-section">
              <p class="vm-section-title">Snapshots</p>
              <p v-if="!snapshotList.length" class="vm-empty">None yet</p>
              <div v-for="snap in snapshotList" :key="snap" class="vm-list-row">
                <b-icon icon="camera" size="is-small" />
                <span class="vm-mono vm-list-text" :title="snap">{{
                  snap
                }}</span>
                <b-tooltip
                  v-if="canRestoreSnapshot"
                  label="restore this snapshot"
                  type="is-dark">
                  <b-button
                    size="is-small"
                    type="is-success"
                    icon-left="play-circle"
                    aria-label="restore this snapshot"
                    @click="$emit('restore-snapshot', snap)" />
                </b-tooltip>
              </div>
            </div>

            <div v-if="canListForwards" class="vm-section">
              <p class="vm-section-title">
                Port forwards
                <a
                  v-if="forwardAction.permitted && !forwardAction.disabled"
                  class="vm-section-link"
                  @click="$emit('action', 'portForward')">
                  <b-icon icon="plus" size="is-small" /> add
                </a>
              </p>
              <p v-if="!forwards.length" class="vm-empty">None</p>
              <div
                v-for="(forward, index) in forwards"
                :key="index"
                class="vm-list-row">
                <b-icon icon="arrow-right" size="is-small" />
                <span class="vm-mono vm-list-text">
                  {{ forward.srcPort }} → {{ forward.dstHost }}:{{
                    forward.dstPort
                  }}
                </span>
                <b-tag type="is-dark">{{ forward.owner }}</b-tag>
                <b-tooltip
                  v-if="forward.canDelete && canDeleteForward"
                  label="delete this port forward"
                  type="is-dark">
                  <b-button
                    size="is-small"
                    type="is-danger"
                    outlined
                    icon-left="trash"
                    aria-label="delete this port forward"
                    @click="$emit('delete-forward', forward)" />
                </b-tooltip>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<script>
  import AnnotationList from '@/components/AnnotationList.vue';
  import DiskWarning from '@/components/DiskWarning.vue';
  import {
    CUSTOM_NODE_ANNOTATION,
    NODE_ANNOTATIONS,
    annotationErrors,
    annotationRowsFrom,
    annotationText,
    nodeAnnotationsPayload,
  } from '@/utils/experimentAnnotations.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { docsPage } from '@/utils/docs.js';
  import { diskLabel } from '@/utils/diskChain.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { plural, pluralWord } from '@/utils/plural.js';
  import { webSharkInstalled } from '@/utils/webshark.js';
  import {
    CAPTURE_ICON,
    POWER_ACTIONS,
    TOOL_ACTIONS,
    vmActionState,
    vmActionStates,
    vmInterfaces,
    vmStateInfo,
  } from './vmActions.js';

  // The body of the running experiment's VM details modal. It shows the VM
  // and asks the page, by event, to take any action on it.
  export default {
    name: 'VmDetailsCard',
    components: { AnnotationList, DiskWarning },
    mixins: [formattingMixin],
    setup() {
      return {
        captureIcon: CAPTURE_ICON,
        plural,
        pluralWord,
        annotationText,
        diskLabel,
        NODE_ANNOTATIONS,
        CUSTOM_NODE_ANNOTATION,
      };
    },
    props: {
      vm: { type: Object, default: () => ({}) },
      // experiment/vm, the name RBAC checks this VM's permissions against
      fullName: { type: String, default: '' },
      // the experiment's name, for the links to its SOH and SCORCH pages
      experiment: { type: String, default: '' },
      // indexes of the interfaces whose capture button is waiting on the server
      capturePending: { type: Array, default: () => [] },
      // whether starting or stopping all of the VM's captures is under way
      captureAllPending: { type: Boolean, default: false },
      // the images backing the VM's disk, nearest first (see utils/diskChain.js);
      // null while unknown
      backingChain: { type: Array, default: null },
      // the VM's disk and the ISO image in its CD-ROM drive in the disk list,
      // and the minimega files directory it shows; null and empty while
      // unknown
      disk: { type: Object, default: null },
      cdRomDisk: { type: Object, default: null },
      filesDir: { type: String, default: '' },
      screenshot: { type: String, default: '' },
      // the VNC console's URL, when the VM has one to open
      vncHref: { type: String, default: null },
      // the VM's snapshot names; false until they have loaded
      snapshots: { type: [Array, Boolean], default: false },
      forwards: { type: Array, default: () => [] },
      features: { type: Array, default: () => [] },
    },
    emits: [
      'action',
      'capture',
      'change-vlan',
      'delete-forward',
      'edit-labels',
      'restore-snapshot',
      // (annotations, done): done(true) once they are saved
      'save-annotations',
    ],

    data() {
      return {
        // the annotations being edited, or null when not editing
        annotationRows: null,
        annotationsSaving: false,
        // secret annotations the user chose to show
        revealed: [],
      };
    },

    computed: {
      opts() {
        return {
          can: (resource, verb) => this.can(resource, verb),
          features: this.features,
        };
      },
      state() {
        return vmStateInfo(this.vm);
      },
      stateClass() {
        return this.state.type ? `has-state-${this.state.type}` : '';
      },
      // a VM that is not running has no screen to show, so the card shows its
      // state icon and "No screen" instead
      noScreen() {
        const vm = this.vm;
        const delayed = vm.delayed_start && vm.state == 'BUILDING';
        return !vm.running && !vm.busy && !vm.external && !delayed;
      },
      agentClass() {
        return this.vm.ccActive ? 'is-positive' : 'is-muted';
      },
      // the actions the role may take; the ones the VM's state rules out stay
      // visible, disabled, with the reason in their tooltip
      power() {
        return vmActionStates(POWER_ACTIONS, this.vm, this.opts).filter(
          (a) => a.permitted,
        );
      },
      tools() {
        return vmActionStates(TOOL_ACTIONS, this.vm, this.opts).filter(
          (a) => a.permitted,
        );
      },
      forwardAction() {
        return vmActionState('portForward', this.vm, this.opts);
      },
      interfaces() {
        return vmInterfaces(this.vm);
      },
      captureCount() {
        return this.interfaces.filter((i) => i.capturing).length;
      },
      // Capture all interfaces and Stop all packet captures, for the Network
      // box; each stays visible, disabled with the reason, when it can't run
      captureActions() {
        const capturing = (this.vm.captures ?? []).length > 0;
        return [
          { name: 'captureAll', buttonType: 'is-success', buttonIcon: 'play' },
          { name: 'stopCaptures', buttonType: 'is-danger', buttonIcon: 'stop' },
        ]
          .map((button) => ({
            ...vmActionState(button.name, this.vm, this.opts),
            ...button,
            // the one the pending request is for: stopping while any run
            loading:
              this.captureAllPending &&
              (button.name == 'stopCaptures') == capturing,
          }))
          .filter((action) => action.permitted);
      },
      // The WebShark tab: on the VM's first running capture, or on the
      // experiment, where its saved captures are
      webSharkTab() {
        const ifaces = (this.vm.captures ?? [])
          .map((c) => c.interface)
          .sort((a, b) => a - b);
        const query = { exp: this.experiment };
        if (!ifaces.length) {
          return {
            to: { name: 'webshark', query },
            label: "Open the WebShark tab on this experiment's captures",
          };
        }
        return {
          to: {
            name: 'webshark',
            query: { ...query, vm: this.vm.name, iface: String(ifaces[0]) },
          },
          label:
            ifaces.length == 1
              ? "Open this VM's running capture in the WebShark tab"
              : `Open this VM's capture of interface ${ifaces[0]} in the WebShark tab`,
        };
      },
      tags() {
        return Object.entries(this.vm.tags ?? {}).sort(([a], [b]) =>
          a.localeCompare(b),
        );
      },
      // false until the VM's own details, which carry its annotations, have
      // loaded
      annotationsLoaded() {
        const a = this.vm.annotations;
        return a != null && typeof a == 'object';
      },
      annotations() {
        return Object.entries(this.vm.annotations ?? {}).sort(([a], [b]) =>
          a.localeCompare(b),
        );
      },
      canEditAnnotations() {
        return this.can('vms', 'patch');
      },
      annotationsValid() {
        return annotationErrors(this.annotationRows ?? []).every((e) => !e);
      },
      annotationHelp() {
        return this.vm.running
          ? "Saved to this experiment's copy of the topology. Apps read most " +
              'annotations when the experiment starts, so most changes take ' +
              'effect the next time it starts.'
          : "Saved to this experiment's copy of the topology, where apps read " +
              'them when the experiment starts.';
      },
      snapshotList() {
        return Array.isArray(this.snapshots) ? this.snapshots : [];
      },
      live() {
        return this.vm.running && !this.vm.busy;
      },
      canCapture() {
        return this.live && this.can('vms/captures', 'create');
      },
      // whether running captures get an Open in WebShark button
      canOpenWebShark() {
        return (
          !!this.experiment &&
          webSharkInstalled(this.features) &&
          this.can('vms/captures', 'list')
        );
      },
      canViewExperiment() {
        return (
          !!this.experiment &&
          roleAllowed('experiments', 'get', this.experiment)
        );
      },
      canListDisks() {
        return roleAllowed('disks', 'list');
      },
      canChangeVlan() {
        return this.live && this.can('vms', 'patch');
      },
      canListSnapshots() {
        return this.can('vms/snapshots', 'list');
      },
      canRestoreSnapshot() {
        return !this.vm.busy && this.can('vms/snapshots', 'update');
      },
      canListForwards() {
        return this.can('vms/forwards', 'list');
      },
      canDeleteForward() {
        return this.can('vms/forwards', 'delete');
      },
    },

    watch: {
      // another VM's details: nothing of this one's stays open or shown
      'vm.name'() {
        this.annotationRows = null;
        this.revealed = [];
      },
    },

    methods: {
      docsPage,
      can(resource, verb) {
        return roleAllowed(resource, verb, this.fullName);
      },
      isSecret(key) {
        return NODE_ANNOTATIONS.some(
          (entry) => entry.key == key && entry.type == 'password',
        );
      },
      editAnnotations() {
        this.annotationRows = annotationRowsFrom(
          this.vm.annotations,
          NODE_ANNOTATIONS,
          CUSTOM_NODE_ANNOTATION,
        );
      },
      saveAnnotations() {
        this.annotationsSaving = true;
        this.$emit(
          'save-annotations',
          nodeAnnotationsPayload(this.annotationRows),
          (saved) => {
            this.annotationsSaving = false;
            if (saved) this.annotationRows = null;
          },
        );
      },
      // the WebShark page for an interface's running capture
      webSharkLink(iface) {
        return {
          name: 'webshark',
          query: {
            exp: this.experiment,
            vm: this.vm.name,
            iface: String(iface.index),
          },
        };
      },
    },
  };
</script>

<style scoped>
  .vm-card {
    width: min(68rem, calc(100vw - 2rem));
  }

  .vm-card-head {
    align-items: flex-start;
    gap: 1rem;
    border-top: 6px solid #8a8a8a;
    background-color: #4f4f4f;
  }

  .vm-card-head.has-state-is-success {
    border-top-color: hsl(141, 53%, 53%);
  }
  .vm-card-head.has-state-is-warning {
    border-top-color: hsl(48, 100%, 67%);
  }
  .vm-card-head.has-state-is-danger {
    border-top-color: hsl(348, 86%, 61%);
  }
  .vm-card-head.has-state-is-info {
    border-top-color: hsl(204, 86%, 53%);
  }

  .vm-card-heading {
    flex: 1;
    min-width: 0;
  }

  /* the description between the name and the links */
  .vm-card-head.has-description .vm-card-heading {
    flex: 0 1 auto;
    max-width: 45%;
  }

  .vm-card-description {
    flex: 1 1 0;
    min-width: 8rem;
    align-self: center;
    margin: 0 !important;
    padding: 0.4rem 0.75rem;
    border: 1px solid #626262;
    border-radius: 6px;
    background-color: #454545;
    color: #e8e8e8;
    font-size: 0.9rem;
    font-style: italic;
    line-height: 1.35;
    overflow-wrap: anywhere;
    /* at most three lines; the full text is its title */
    display: -webkit-box;
    -webkit-line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .vm-card-title-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }

  .vm-card-title {
    margin: 0 !important;
    flex: 0 1 auto;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  /* the icon and label centered in the pill: b-tag wraps them in a span
     that would otherwise sit them on the text baseline, and Bulma pulls a
     tag's first icon left */
  .vm-state-tag {
    font-weight: 600;
    padding: 0 0.7em;
  }

  :deep(.vm-state-tag > span) {
    display: inline-flex;
    align-items: center;
    gap: 0.35em;
    line-height: 1;
  }

  .vm-state-tag .icon {
    margin: 0 !important;
    width: 1em;
    height: 1em;
  }

  .vm-card-links {
    display: flex;
    flex: none;
    gap: 0.4rem;
  }

  .vm-card-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1.25rem;
    margin-top: 0.4rem;
    color: #e0e0e0;
    font-size: 0.9rem;
  }

  /* icon and text centered on one line */
  .vm-card-meta > span {
    display: inline-flex;
    align-items: center;
    white-space: nowrap;
  }

  .vm-card-meta .icon {
    margin-right: 0.3rem;
    opacity: 0.8;
  }

  .vm-card-body {
    padding-top: 1rem;
  }

  .vm-busy {
    margin-bottom: 0.75rem;
  }

  .vm-section {
    background-color: #484848;
    border-radius: 8px;
    padding: 0.75rem 0.9rem;
    margin-bottom: 0.75rem;
  }

  .vm-section-link {
    margin-left: auto;
    text-transform: none;
  }

  .vm-section-title {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 0.5rem !important;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #cfcfcf;
  }

  .vm-section-link {
    margin-left: auto;
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: normal;
    text-transform: none;
  }

  /* the Network box's start, stop and WebShark buttons */
  .vm-section-actions {
    display: flex;
    gap: 0.3rem;
    margin-left: auto;
  }

  /* one that can't run now is gray, hovered too; its tooltip says why. The
     :not()s outrank the colored buttons' hover rules in _general.scss. */
  .vm-section-actions .button[disabled]:not(.is-outlined):not(.is-inverted) {
    border-color: transparent;
    background-color: #5a5a5a;
    color: #a8a8a8;
    opacity: 1;
  }

  .vm-annotation-list {
    display: grid;
    grid-template-columns: fit-content(50%) minmax(0, 1fr);
    align-items: center;
    gap: 0.35rem 0.6rem;
  }

  .vm-annotation-key {
    padding: 0.1em 0.5em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.8rem;
    color: inherit;
    background-color: rgba(255, 255, 255, 0.1);
  }

  /* the list's label stands in for the section title while editing */
  .vm-annotations :deep(.field-label-help .label) {
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #cfcfcf;
  }

  .vm-reveal {
    margin-left: 0.35rem;
  }

  .vm-annotation-buttons {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  .vm-power-buttons {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
    gap: 0.5rem;
  }

  .vm-tool-buttons {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr));
    gap: 0.4rem;
  }

  .vm-power-buttons :deep(.b-tooltip),
  .vm-tool-buttons :deep(.b-tooltip),
  .vm-power-buttons :deep(.tooltip-trigger),
  .vm-tool-buttons :deep(.tooltip-trigger) {
    display: block;
    width: 100%;
  }

  .vm-power-buttons :deep(.button) {
    font-weight: 600;
  }

  .vm-tool-buttons :deep(.button) {
    justify-content: flex-start;
  }

  .vm-columns {
    margin-bottom: 0 !important;
  }

  .vm-no-screen {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
    padding: 1.5rem 1rem;
    border-radius: 8px;
    border: 2px dashed #7a7a7a;
    color: #bdbdbd;
    text-align: center;
  }

  .vm-no-screen p {
    margin: 0 !important;
  }

  .vm-screenshot {
    margin: 0 0 0.75rem 0 !important;
    position: relative;
    background-color: #2b2b2b;
    border-radius: 8px;
    overflow: hidden;
    aspect-ratio: 4 / 3;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .vm-screenshot a {
    display: flex;
    width: 100%;
    height: 100%;
    align-items: center;
    justify-content: center;
  }

  .vm-screenshot img {
    display: block;
    max-width: 100%;
    max-height: 100%;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  .vm-screenshot-hint {
    position: absolute;
    right: 0.5rem;
    bottom: 0.5rem;
    padding: 0.2rem 0.6rem;
    border-radius: 999px;
    background-color: rgba(0, 0, 0, 0.65);
    color: white;
    font-size: 0.8rem;
  }

  .vm-screenshot a:hover .vm-screenshot-hint {
    background-color: #da622d;
  }

  .vm-stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(8.5rem, 1fr));
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }

  .vm-stat {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    background-color: #484848;
    border-radius: 8px;
    border-left: 4px solid #62c0d7;
    padding: 0.5rem 0.75rem;
  }

  .vm-stat > .icon {
    color: #62c0d7;
  }

  .vm-stat.is-positive {
    border-left-color: hsl(141, 53%, 53%);
  }
  .vm-stat.is-positive > .icon {
    color: hsl(141, 53%, 53%);
  }
  .vm-stat.is-muted {
    border-left-color: #8a8a8a;
  }
  .vm-stat.is-muted > .icon {
    color: #9a9a9a;
  }

  .vm-stat-value {
    font-size: 1.15rem;
    font-weight: 700;
    line-height: 1.2;
    margin: 0 !important;
  }

  .vm-stat-label {
    font-size: 0.75rem;
    color: #cfcfcf;
    margin: 0 !important;
  }

  .vm-kv {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    min-width: 0;
  }

  .vm-kv + .vm-kv,
  .vm-backing-chain + .vm-kv {
    margin-top: 0.35rem;
  }

  .vm-kv-key {
    flex: 0 0 4.5rem;
    color: #cfcfcf;
    font-size: 0.85rem;
  }

  .vm-kv-value {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .vm-mono {
    font-family: monospace;
    font-size: 0.9rem;
  }

  .vm-snapshot-tag {
    font-weight: 600;
  }

  /* the disk's backing images under it, as the Disks page's chain shows */
  .vm-backing-chain {
    list-style: none;
    margin: 0.15rem 0 0 calc(4.5rem + 0.6rem) !important;
    padding: 0;
  }

  .vm-backing-chain li {
    display: flex;
    gap: 0.4rem;
    min-width: 0;
    color: #d8d8d8;
  }

  .vm-chain-arrow {
    color: #9a9a9a;
  }

  .vm-chain-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .vm-capture-count {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    color: hsl(348, 86%, 71%);
    letter-spacing: normal;
    text-transform: none;
  }

  .vm-rec-dot {
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background-color: hsl(348, 86%, 61%);
    box-shadow: 0 0 6px hsl(348, 86%, 61%);
  }

  .vm-iface {
    display: grid;
    grid-template-columns:
      1.6rem minmax(0, 1.4fr) minmax(0, 1fr) minmax(0, 0.8fr)
      2.25rem;
    align-items: center;
    gap: 0.6rem;
    padding: 0.35rem 0.5rem;
    border-radius: 6px;
    border-left: 3px solid transparent;
    background-color: #545454;
  }

  .vm-iface + .vm-iface {
    margin-top: 0.35rem;
  }

  /* the capture button, then Open in WebShark on a capturing interface */
  .vm-iface-cap {
    display: flex;
    align-items: center;
    gap: 0.3rem;
  }

  @media screen and (min-width: 769px) {
    .vm-iface.has-webshark {
      grid-template-columns:
        1.6rem minmax(0, 1.4fr) minmax(0, 1fr) minmax(0, 0.8fr)
        4.8rem;
    }
  }

  .vm-iface.is-capturing {
    border-left-color: hsl(348, 86%, 61%);
    background-color: rgba(241, 70, 104, 0.18);
  }

  .vm-iface.is-disconnected {
    opacity: 0.6;
  }

  .vm-iface-index {
    display: inline-flex;
    justify-content: center;
    align-items: center;
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
    background-color: #686868;
    font-size: 0.75rem;
    font-weight: 700;
  }

  .vm-iface-net {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
  }

  .vm-iface-name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .vm-iface-ip,
  .vm-iface-tap {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .vm-iface-tap {
    color: #cfcfcf;
  }

  .vm-label {
    margin-bottom: 0 !important;
  }

  .vm-label .tag {
    margin-bottom: 0.35rem !important;
  }

  .vm-lists {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: 0 0.75rem;
  }

  .vm-list-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
  }

  .vm-list-row + .vm-list-row {
    margin-top: 0.35rem;
  }

  .vm-list-text {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .vm-empty {
    color: #bdbdbd;
    font-style: italic;
    margin: 0 !important;
  }

  /* narrow windows: tighter padding; interface rows wrap onto three lines */
  @media screen and (max-width: 768px) {
    .vm-card {
      width: calc(100vw - 2rem);
      margin: 0 auto !important;
    }

    .vm-card-body {
      padding: 0.75rem;
    }

    .vm-power-buttons,
    .vm-tool-buttons {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    /* room for the modal's close button */
    .vm-card-head {
      padding-right: 2.75rem;
    }

    .vm-iface {
      grid-template-columns: 1.6rem minmax(0, 1fr) 2.25rem;
      grid-template-areas:
        'idx net cap'
        'idx ip ip'
        'idx tap tap';
      row-gap: 0.15rem;
    }

    .vm-iface-index {
      grid-area: idx;
      align-self: start;
    }
    .vm-iface-net {
      grid-area: net;
    }
    .vm-iface-ip {
      grid-area: ip;
    }
    .vm-iface-tap {
      grid-area: tap;
    }
    .vm-iface-cap {
      grid-area: cap;
    }
  }
</style>
