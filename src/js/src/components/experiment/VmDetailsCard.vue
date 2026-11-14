<template>
  <div :class="['modal-card', 'vm-card', { 'vm-card--console': !stopped }]">
    <header
      class="modal-card-head vm-card-head"
      :class="[stateClass, { 'has-description': vm.description }]">
      <div class="vm-card-heading">
        <div class="vm-card-title-row">
          <h2 class="modal-card-title vm-card-title">
            {{ vm.name || 'unknown' }}
          </h2>
          <b-tag :type="state.type" rounded class="vm-state-tag">
            <b-icon :icon="state.icon" size="is-small" />
            <span>{{ state.label }}</span>
          </b-tag>
          <b-tag
            v-if="os"
            :class="['vm-os-tag', `is-os-${os.family}`]"
            title="Operating system (os_type)">
            <b-icon :icon="os.icon" size="is-small" />
            <span class="is-sr-only">Operating system: </span>
            <span>{{ os.label }}</span>
          </b-tag>
          <b-tag v-if="vm.dnb" rounded type="is-dark">do not boot</b-tag>
        </div>
        <div class="vm-card-meta">
          <span v-if="vm.host" title="Host">
            <b-icon icon="server" size="is-small" />{{ vm.host }}
          </span>
          <span v-if="vm.running && !stopped" title="Uptime">
            <b-icon icon="clock" size="is-small" />up
            {{ formatUptime(vm.uptime) }}
          </span>
          <span v-if="vm.delayed_start" title="Delayed start">
            <b-icon icon="hourglass-half" size="is-small" />{{ delayText }}
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
            label="view state of health"
            type="is-dark"
            position="is-bottom"
            append-to-body>
            <router-link
              class="button is-light is-small"
              :aria-label="`View state of health for experiment ${experiment}`"
              :to="{ name: 'soh', params: { id: experiment } }">
              <b-icon icon="heartbeat" />
            </router-link>
          </b-tooltip>
          <b-tooltip
            label="view SCORCH runs"
            type="is-dark"
            position="is-bottom"
            append-to-body>
            <router-link
              class="button is-light is-small"
              :aria-label="`View SCORCH runs for experiment ${experiment}`"
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

      <div v-if="!stopped && (power.length || consoleLink)" class="vm-section">
        <h3 v-if="power.length" class="vm-section-title">Power</h3>
        <div class="vm-power-row">
          <div v-if="power.length" class="vm-power-buttons">
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
                :aria-disabled="action.disabled ? 'true' : undefined"
                :loading="pendingActions.includes(action.name)"
                :aria-busy="pendingActions.includes(action.name)"
                expanded
                @click="action.disabled || $emit('action', action.name)">
                {{ action.label }}
              </b-button>
            </b-tooltip>
          </div>
          <a
            v-if="consoleLink"
            class="button is-info vm-open-console"
            :href="consoleLink"
            @click="(event) => handleConsoleLinkClick(event, [consoleKey])">
            <span>Open console</span>
            <b-icon icon="up-right-from-square" size="is-small" />
          </a>
        </div>
      </div>

      <div class="columns vm-columns">
        <div v-if="stopped" class="column is-5">
          <div class="vm-section vm-settings">
            <h3 class="vm-section-title">
              Settings
              <span v-if="showReset" class="vm-section-actions">
                <b-tooltip
                  :label="resetTip"
                  type="is-dark"
                  position="is-left"
                  append-to-body
                  multilined>
                  <b-button
                    class="vm-reset"
                    size="is-small"
                    type="is-light"
                    icon-left="undo-alt"
                    :aria-disabled="settingsAtDefaults ? 'true' : undefined"
                    :loading="resetting"
                    @click="settingsAtDefaults || $emit('reset-settings')"
                    >Reset settings</b-button
                  >
                </b-tooltip>
              </span>
            </h3>
            <div
              v-for="row in SETTING_ROWS"
              :key="row.field"
              class="vm-kv vm-setting-row">
              <span class="vm-kv-key">{{ row.label }}</span>
              <VmSettingControl
                :field="row.field"
                :vm="vm"
                :hosts="hosts"
                :disks="diskChoices"
                :editable="canEditSettings"
                :pending="row.field in settingsPending"
                :pending-value="settingsPending[row.field]"
                with-text
                @change="
                  (value) => $emit('change-setting', row.field, value)
                " />
            </div>
          </div>
        </div>
        <div v-else class="column vm-console-column">
          <div v-if="noScreen" class="vm-no-screen">
            <b-icon :icon="state.icon" size="is-large" />
            <p>No screen: the VM is {{ state.label.toLowerCase() }}</p>
          </div>
          <VncPreview
            v-else
            :experiment="experiment"
            :vm="vm"
            :screenshot="screenshot" />

          <div v-if="tools.length" class="vm-section">
            <h3 class="vm-section-title">More actions</h3>
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
                  :aria-disabled="action.disabled ? 'true' : undefined"
                  :loading="pendingActions.includes(action.name)"
                  :aria-busy="pendingActions.includes(action.name)"
                  expanded
                  @click="action.disabled || $emit('action', action.name)">
                  {{ action.label }}
                </b-button>
              </b-tooltip>
            </div>
          </div>
        </div>

        <div class="column">
          <div class="vm-stats">
            <div v-if="!stopped" class="vm-stat" :class="agentClass">
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
            <h3 class="vm-section-title">
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
            </h3>
            <div class="vm-kv">
              <span class="vm-kv-key">Disk</span>
              <router-link
                v-if="diskLink(disk)"
                class="vm-kv-value vm-mono vm-disk-link"
                :title="disk.fullPath"
                :to="diskLink(disk)">
                {{ diskLabel(disk) }}
              </router-link>
              <span v-else class="vm-kv-value vm-mono" :title="vm.disk">
                {{
                  diskLabel(disk) || (vm.disk && baseName(vm.disk)) || 'None'
                }}
              </span>
              <DiskWarning
                v-if="diskReasons(disk).length"
                :reasons="diskReasons(disk)"
                :detail="chainProblem(disk)"
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
                :key="image.label"
                class="vm-mono"
                :title="`backed by ${image.label}`">
                <span class="vm-chain-arrow" aria-hidden="true">&darr;</span>
                <router-link
                  v-if="diskLink(image)"
                  class="vm-chain-name vm-disk-link"
                  :to="diskLink(image)">
                  {{ image.label }}
                </router-link>
                <span v-else class="vm-chain-name">{{ image.label }}</span>
              </li>
            </ol>
            <div v-if="vm.cdRom" class="vm-kv vm-cdrom">
              <span class="vm-kv-key">CD-ROM</span>
              <router-link
                v-if="diskLink(cdRomDisk)"
                class="vm-kv-value vm-mono vm-disk-link"
                :title="cdRomDisk.fullPath"
                :to="diskLink(cdRomDisk)">
                {{ diskLabel(cdRomDisk) }}
              </router-link>
              <span v-else class="vm-kv-value vm-mono" :title="vm.cdRom">
                {{ diskLabel(cdRomDisk) || baseName(vm.cdRom) }}
              </span>
              <DiskWarning
                v-if="diskReasons(cdRomDisk).length"
                :reasons="diskReasons(cdRomDisk)"
                :detail="chainProblem(cdRomDisk)"
                :files-dir="filesDir" />
            </div>
          </div>

          <div class="vm-section">
            <h3 class="vm-section-title">
              Network
              <span v-if="captureCount && !stopped" class="vm-capture-count">
                <span class="vm-rec-dot"></span>
                {{ plural(captureCount, 'capture') }} running
              </span>
              <span v-if="!stopped" class="vm-section-actions">
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
                    :aria-disabled="
                      action.disabled || captureAllPending ? 'true' : undefined
                    "
                    :loading="action.loading"
                    :aria-label="action.label"
                    @click="
                      action.disabled ||
                      captureAllPending ||
                      $emit('action', action.name)
                    " />
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
            </h3>
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
                  label="change VLAN"
                  type="is-dark">
                  <button
                    type="button"
                    class="plain-button vm-iface-name"
                    @click="$emit('change-vlan', iface)">
                    {{ iface.network }}
                  </button>
                </b-tooltip>
                <span v-else class="vm-iface-name">{{ iface.network }}</span>
                <span
                  v-if="pendingActions.includes(vlanPendingKey(iface.index))"
                  class="vm-pending-spinner"
                  title="waiting for the server"></span>
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
            <h3 class="vm-section-title">
              Labels
              <button
                type="button"
                class="plain-button vm-section-link"
                @click="$emit('edit-labels')">
                <b-icon icon="edit" size="is-small" /> view/edit
              </button>
            </h3>
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
            <h3 v-if="!annotationRows" class="vm-section-title">
              Annotations
              <button
                v-if="canEditAnnotations"
                type="button"
                class="plain-button vm-section-link vm-edit-annotations"
                @click="editAnnotations">
                <b-icon icon="edit" size="is-small" /> edit
              </button>
            </h3>
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
                  <button
                    type="button"
                    class="plain-button vm-reveal"
                    :aria-label="`Show ${key}`"
                    @click="revealed = [...revealed, key]">
                    <b-icon icon="eye" size="is-small" />
                  </button>
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
            <div v-if="canListSnapshots && !stopped" class="vm-section">
              <h3 class="vm-section-title">Snapshots</h3>
              <p v-if="!snapshotList.length" class="vm-empty">None yet</p>
              <div v-for="snap in snapshotList" :key="snap" class="vm-list-row">
                <b-icon icon="clone" size="is-small" />
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
                    :loading="pendingActions.includes(restorePendingKey(snap))"
                    :aria-busy="
                      pendingActions.includes(restorePendingKey(snap))
                    "
                    @click="$emit('restore-snapshot', snap)" />
                </b-tooltip>
                <b-tooltip
                  :label="deleteSnapshotReason || 'delete this snapshot'"
                  type="is-dark"
                  multilined>
                  <b-button
                    size="is-small"
                    type="is-danger"
                    outlined
                    icon-left="trash"
                    class="vm-delete-snapshot"
                    :aria-label="`delete the snapshot ${snap}`"
                    :aria-disabled="deleteSnapshotReason ? 'true' : undefined"
                    :loading="
                      pendingActions.includes(deleteSnapshotPendingKey(snap))
                    "
                    :aria-busy="
                      pendingActions.includes(deleteSnapshotPendingKey(snap))
                    "
                    @click="
                      deleteSnapshotReason ||
                      pendingActions.includes(deleteSnapshotPendingKey(snap)) ||
                      $emit('delete-snapshot', snap)
                    " />
                </b-tooltip>
              </div>
            </div>

            <div v-if="stopped" class="vm-section vm-startup-forwards">
              <h3 class="vm-section-title">
                Port forwards started at experiment start
              </h3>
              <p v-if="!annotationsLoaded" class="vm-empty">Loading…</p>
              <p v-else-if="!startupForwards.length" class="vm-empty">
                None. Add them with the {{ AUTOTUNNEL }} annotation.
              </p>
              <div
                v-for="(forward, index) in startupForwards"
                :key="index"
                class="vm-list-row">
                <b-icon icon="arrow-right" size="is-small" />
                <span v-if="forward.invalid" class="vm-mono vm-list-text">
                  {{ forward.invalid }} (not a valid entry)
                </span>
                <span v-else class="vm-mono vm-list-text">
                  {{ forward.srcPort }} → {{ forward.dstHost }}:{{
                    forward.dstPort
                  }}
                </span>
              </div>
            </div>
            <div v-else-if="canListForwards" class="vm-section">
              <h3 class="vm-section-title">
                Port forwards
                <b-tooltip
                  v-if="forwardAction.permitted || forwardAction.denied"
                  :label="forwardAction.reason || forwardAction.description"
                  type="is-dark"
                  multilined>
                  <button
                    type="button"
                    class="plain-button vm-section-link"
                    :aria-disabled="forwardAction.disabled ? 'true' : undefined"
                    @click="
                      forwardAction.disabled || $emit('action', 'portForward')
                    ">
                    <b-icon icon="plus" size="is-small" /> add
                  </button>
                </b-tooltip>
              </h3>
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
    vmAnnotationHelp,
  } from '@/utils/experimentAnnotations.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { docsPage } from '@/utils/docs.js';
  import {
    baseName,
    chainProblem,
    diskDetailsRoute,
    diskLabel,
    diskReasons,
  } from '@/utils/diskChain.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { plural, pluralWord } from '@/utils/plural.js';
  import { webSharkInstalled } from '@/utils/webshark.js';
  import { consoleHref } from '@/vnc/consoleUrl.js';
  import { handleConsoleLinkClick } from '@/vnc/openConsole.js';
  import VncPreview from '@/vnc/VncPreview.vue';
  import { consoleAvailable, vmPaused } from '@/vnc/vmScreen.js';
  import {
    countdownText,
    startDue,
    useCountdownClock,
  } from './delayCountdown.js';
  import { osTypeInfo } from './osType.js';
  import VmSettingControl from './VmSettingControl.vue';
  import {
    AUTOTUNNEL,
    atDefaults,
    hasDefaults,
    startupForwards,
  } from './vmSettings.js';
  import {
    CAPTURE_ICON,
    POWER_ACTIONS,
    TOOL_ACTIONS,
    deleteSnapshotPendingKey,
    notPermittedReason,
    restorePendingKey,
    shownAction,
    vlanPendingKey,
    vmActionState,
    vmActionStates,
    vmButtonStates,
    vmInterfaces,
    vmStateInfo,
  } from './vmActions.js';

  // the settings a stopped VM's card edits, in order
  const SETTING_ROWS = [
    { field: 'host', label: 'Host' },
    { field: 'cpus', label: 'CPUs' },
    { field: 'ram', label: 'Memory' },
    { field: 'disk', label: 'Disk' },
    { field: 'inject_partition', label: 'Partition' },
    { field: 'snapshot', label: 'Persistence' },
    { field: 'dnb', label: 'Boot' },
  ];

  // The body of an experiment's VM details modal. It shows the VM and asks
  // the page, by event, to take any action on it. A stopped experiment's VM
  // shows its settings to edit instead of its screen and live actions.
  export default {
    name: 'VmDetailsCard',
    components: { AnnotationList, DiskWarning, VmSettingControl, VncPreview },
    mixins: [formattingMixin],
    setup(props) {
      return {
        // the shared clock of the countdown, while the VM waits for its timer
        now: useCountdownClock(
          () => startDue(props.vm, props.delayedDue) !== null,
        ),
        captureIcon: CAPTURE_ICON,
        plural,
        pluralWord,
        annotationText,
        baseName,
        chainProblem,
        diskLabel,
        diskReasons,
        NODE_ANNOTATIONS,
        CUSTOM_NODE_ANNOTATION,
        restorePendingKey,
        deleteSnapshotPendingKey,
        vlanPendingKey,
        AUTOTUNNEL,
        SETTING_ROWS,
        handleConsoleLinkClick,
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
      // the actions running in the background for the VM, whose buttons spin
      pendingActions: { type: Array, default: () => [] },
      // the images backing the VM's disk, nearest first, as backingChain in
      // utils/diskChain.js gives them; null while unknown
      backingChain: { type: Array, default: null },
      // the VM's disk and the ISO image in its CD-ROM drive in the disk list,
      // and the minimega files directory it shows; null and empty while
      // unknown
      disk: { type: Object, default: null },
      cdRomDisk: { type: Object, default: null },
      filesDir: { type: String, default: '' },
      // the screenshot or placeholder shown without a live console
      screenshot: { type: String, default: '' },
      // the VM's snapshot names; false until they have loaded
      snapshots: { type: [Array, Boolean], default: false },
      forwards: { type: Array, default: () => [] },
      features: { type: Array, default: () => [] },
      // whether the VM's experiment is stopped: the card then edits its
      // settings, with the hosts and disk list (null while not known) to
      // pick from, and has no live actions
      stopped: { type: Boolean, default: false },
      hosts: { type: Array, default: () => [] },
      diskChoices: { type: Array, default: null },
      // the setting changes waiting on the server, each field's value
      settingsPending: { type: Object, default: () => ({}) },
      // whether a reset of the settings waits on the server
      resetting: { type: Boolean, default: false },
      // when the VM starts (RFC 3339), while it waits for a timer
      delayedDue: { type: String, default: '' },
    },
    emits: [
      'action',
      'capture',
      'change-vlan',
      'delete-forward',
      // (snapshot): delete the snapshot of the VM
      'delete-snapshot',
      'edit-labels',
      'restore-snapshot',
      // (annotations, done): done(true) once they are saved
      'save-annotations',
      // (field, value): save a stopped VM's setting
      'change-setting',
      'reset-settings',
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
        if (this.stopped && !this.vm.external) {
          return { label: 'Stopped', type: null, icon: 'stop' };
        }
        return vmStateInfo(this.vm);
      },
      // the time left before a timer VM starts, else its delay
      delayText() {
        const due = startDue(this.vm, this.delayedDue);
        return due === null
          ? `starts after ${this.vm.delayed_start}`
          : countdownText(due, this.now);
      },
      // the guest operating system's tag, or null when the VM gives none
      os() {
        return osTypeInfo(this.vm.os_type);
      },
      stateClass() {
        return this.state.type ? `has-state-${this.state.type}` : '';
      },
      // a VM that is not running has no screen to show, so the card shows its
      // state icon and "No screen" instead
      noScreen() {
        const vm = this.vm;
        const delayed = vm.delayed_start && vm.state == 'BUILDING';
        return (
          !vm.running && !this.paused && !vm.busy && !vm.external && !delayed
        );
      },
      paused() {
        return vmPaused(this.vm);
      },
      // the VM as the console page names it
      consoleKey() {
        return `${this.experiment}/${this.vm.name}`;
      },
      // the console page of a VM with a screen the role may see, or null
      consoleLink() {
        return consoleAvailable(this.experiment, this.vm)
          ? consoleHref(this.$router, [this.consoleKey])
          : null;
      },
      agentClass() {
        return this.vm.ccActive ? 'is-positive' : 'is-muted';
      },
      // The actions, disabled with the reason in their tooltip when the VM's
      // state or the role rules them out; those the server lacks are left out
      power() {
        return vmButtonStates(POWER_ACTIONS, this.vm, this.opts).filter(
          shownAction,
        );
      },
      tools() {
        return vmActionStates(TOOL_ACTIONS, this.vm, this.opts).filter(
          shownAction,
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
          .filter(shownAction);
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
        return vmAnnotationHelp(this.vm.running);
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
          !this.stopped &&
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
      // why Delete is greyed out for each snapshot, or null
      deleteSnapshotReason() {
        return this.can('vms/snapshots', 'delete')
          ? null
          : notPermittedReason('vms/snapshots', 'delete');
      },
      canListForwards() {
        return this.can('vms/forwards', 'list');
      },
      canDeleteForward() {
        return this.can('vms/forwards', 'delete');
      },
      canEditSettings() {
        return !this.vm.external && this.can('vms', 'patch');
      },
      showReset() {
        return this.canEditSettings && hasDefaults(this.vm);
      },
      settingsAtDefaults() {
        return atDefaults(this.vm);
      },
      resetTip() {
        return this.settingsAtDefaults
          ? 'Cannot reset settings: they have the topology values'
          : 'Set every setting back to the topology value';
      },
      startupForwards() {
        return startupForwards(this.vm.annotations);
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
      // the Disks page with a listed image's card open, or null for an image
      // the list does not hold or a role that may not list disks
      diskLink(image) {
        return image?.fullPath && this.canListDisks
          ? diskDetailsRoute(image.fullPath)
          : null;
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
  /* the modal's 68rem width and 20px side margins center it */
  .vm-card {
    width: min(68rem, calc(100vw - 40px));
  }

  /* M3a: the console on the left 60%, the details scroll on the right */
  .vm-card--console {
    width: min(96rem, calc(100vw - 40px));
    height: 90vh;
  }

  .vm-card--console .vm-card-body {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .vm-card--console .vm-columns {
    flex: 1 1 auto;
    min-height: 0;
  }

  .vm-card--console .vm-columns > .column {
    overflow-y: auto;
  }

  .vm-console-column {
    flex: none;
    width: 60%;
  }

  .vm-power-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .vm-power-row .vm-power-buttons {
    flex: 1 1 auto;
  }

  .vm-open-console {
    flex: none;
    margin-left: auto;
  }

  .vm-card-head {
    align-items: flex-start;
    gap: 1rem;
    border-top: 6px solid #8a8a8a;
    background-color: #4f4f4f;
  }

  /* below 68rem plus two gutters the modal's close X sits on the head's corner */
  @media screen and (max-width: 1191px) {
    .vm-card-head {
      padding-right: 3.5rem;
    }
  }

  /* the same for the console card's 96rem */
  @media screen and (max-width: 1639px) {
    .vm-card--console .vm-card-head {
      padding-right: 3.5rem;
    }
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

  /* !important: the heading reset in _general.scss sets weight 400 */
  .vm-card-title {
    margin: 0 !important;
    flex: 0 1 auto;
    font-weight: 600 !important;
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

  /* the OS tag, a squarer box than the state pill; each family's text is at
     least 7:1 on its color */
  .vm-os-tag {
    font-weight: 600;
    border-radius: 4px;
  }

  :deep(.vm-os-tag > span) {
    display: inline-flex;
    align-items: center;
    gap: 0.35em;
    line-height: 1;
  }

  .vm-os-tag .icon {
    margin: 0 !important;
    width: 1em;
    height: 1em;
  }

  .vm-os-tag.is-os-linux {
    background-color: #f2c94c;
    color: #1a1a1a;
  }
  .vm-os-tag.is-os-windows {
    background-color: #005a9e;
    color: #fff;
  }
  .vm-os-tag.is-os-router {
    background-color: #5e35b1;
    color: #fff;
  }
  .vm-os-tag.is-os-other {
    background-color: #dbdbdb;
    color: #1a1a1a;
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
    /* 7.2:1 on the head */
    color: #f0f0f0;
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

  .vm-section-title {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-top: 0 !important;
    margin-bottom: 0.5rem !important;
    font-size: 0.75rem;
    line-height: 1.5;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e6e6e6;
  }

  .vm-section-link {
    margin-left: auto;
    color: #d0ecf3;
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

  /* an action that cannot run now has no fill and a dashed outline, hovered
     too, so it differs from the filled ones; its tooltip says why. The :not()s
     outrank the colored buttons' hover rules in _general.scss. */
  .vm-power-buttons :deep(.button[aria-disabled='true']:not(.is-inverted)),
  .vm-tool-buttons :deep(.button[aria-disabled='true']:not(.is-inverted)),
  .vm-section-actions .button[aria-disabled='true']:not(.is-inverted) {
    border-width: 1px;
    border-style: dashed;
    border-color: #9a9a9a;
    background-color: transparent;
    box-shadow: none;
    color: #bdbdbd;
    opacity: 1;
    cursor: not-allowed;
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
    background-color: rgba(0, 0, 0, 0.3);
  }

  /* the list's label stands in for the section title while editing */
  .vm-annotations :deep(.field-label-help .label) {
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e6e6e6;
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

  /* Bulma's outlined red text is too faint on the card's gray; 7.4:1 */
  .vm-power-buttons
    :deep(
      .button.is-danger.is-outlined:not(:hover):not([aria-disabled='true'])
    ) {
    border-color: #fde0e5;
    color: #fde0e5;
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
    color: #e6e6e6;
    text-align: center;
  }

  .vm-no-screen p {
    margin: 0 !important;
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
    color: #e6e6e6;
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
    color: #e6e6e6;
    font-size: 0.85rem;
  }

  /* a stopped VM's settings: a control to each line */
  .vm-setting-row .vm-kv-key {
    flex-basis: 6rem;
  }

  .vm-setting-row > .vm-setting {
    flex: 1 1 auto;
    min-width: 0;
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

  /* the link text color, 7.4:1 on the section's gray */
  .vm-disk-link,
  .vm-disk-link:hover,
  .vm-disk-link:focus {
    color: #d0ecf3;
    text-decoration: underline;
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
    color: #e6e6e6;
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
    color: #fde0e5;
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
    background-color: #3d3d3d;
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
    background-color: #45363a;
  }

  /* a dashed edge, not a fade, so the text keeps its contrast */
  .vm-iface.is-disconnected {
    border-left: 3px dashed #8a8a8a;
  }

  .vm-iface-index {
    display: inline-flex;
    justify-content: center;
    align-items: center;
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
    background-color: #575757;
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

  /* the link text color, 8.8:1 on the interface rows */
  button.vm-iface-name {
    color: #d0ecf3;
  }

  .vm-iface-name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  /* beside an interface whose VLAN change is waiting on the server */
  .vm-pending-spinner {
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: vm-pending-spin 0.6s linear infinite;
  }

  @keyframes vm-pending-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .vm-iface-ip,
  .vm-iface-tap {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .vm-iface-tap {
    color: #e6e6e6;
  }

  /* has-addons drops the space between tags, so space the labels apart */
  .vm-label {
    margin-bottom: 0 !important;
    margin-right: 0.5rem;
  }

  .vm-label .tag {
    margin-bottom: 0.35rem !important;
  }

  .vm-label .tag.is-primary {
    background-color: #005f56;
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
    color: #e6e6e6;
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
