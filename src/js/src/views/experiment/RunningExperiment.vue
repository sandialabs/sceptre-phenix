<template>
  <div class="content">
    <b-modal
      v-model="expModal.active"
      @close="resetExpModal"
      has-modal-card
      width="68rem">
      <VmDetailsCard
        :vm="expModalVm"
        :full-name="expModal.fullName"
        :experiment="experiment.name"
        :capture-pending="pendingCaptureIfaces(expModalVm.name)"
        :capture-all-pending="!!captureAllPending[expModalVm.name]"
        :pending-actions="vmActionPending[expModalVm.name] ?? []"
        :backing-chain="expModal.backingChain"
        :disk="expModal.disk"
        :cd-rom-disk="expModal.cdRomDisk"
        :files-dir="expModal.filesDir"
        :screenshot="vmScreenshot(expModalVm, screenshotsDue)"
        :vnc-href="
          expModalVm.running && !expModalVm.busy ? vncLoc(expModalVm) : null
        "
        :snapshots="expModal.snapshots"
        :forwards="expModal.forwards"
        :features="features"
        @action="(name) => onVmAction(name, expModalVm)"
        @capture="(index) => handlePcap(expModalVm, index)"
        @change-vlan="(iface) => showVlanModal(expModalVm, iface.index)"
        @edit-labels="showTagsModal(expModalVm)"
        @save-annotations="saveAnnotations"
        @restore-snapshot="(snap) => restoreSnapshot(expModalVm.name, snap)"
        @delete-forward="(fwd) => deletePortForward(expModalVm.name, fwd)" />
    </b-modal>
    <PortForwardModal
      v-model:active="portForwardModal.active"
      v-model:src-port="portForwardModal.srcPort"
      v-model:dst-host="portForwardModal.dstHost"
      v-model:dst-port="portForwardModal.dstPort"
      @close="resetPortForwardModal"
      @create="createPortForward()" />
    <VlanModal
      v-model:active="vlanModal.active"
      v-model:vlan="vlan"
      :vm-name="vlanModal.vmName"
      :from-net="vlanModal.vmFromNet"
      :net-index="vlanModal.vmNetIndex"
      :vlans="experiment.vlans"
      @change="
        changeVlan(
          vlanModal.vmNetIndex,
          vlan,
          vlanModal.vmFromNet,
          vlanModal.vmName,
        )
      " />
    <RedeployModal
      v-model:active="redeployModal.active"
      :vms="redeployModal.vm"
      :disks="disks"
      @close="resetRedeployModal"
      @edit="(index, field, value) => (redeployModal.vm[index][field] = value)"
      @redeploy="redeployVm(redeployModal.vm)" />
    <VmFilenamesModal
      v-model:active="diskImageModal.active"
      title="Create a Disk Image"
      kind="Disk image"
      prompt="Create disk image of the"
      :vms="diskImageModal.vm"
      :valid="validate(diskImageModal)"
      @close="resetDiskImageModal"
      @update:filename="
        (index, name) => (diskImageModal.vm[index].filename = name)
      "
      @create="backingImage(diskImageModal.vm)" />
    <VmFilenamesModal
      v-model:active="memorySnapshotModal.active"
      title="Create memory snapshot"
      kind="Memory snapshot"
      prompt="Create a memory snapshot for the"
      :vms="memorySnapshotModal.vm"
      :valid="validate(memorySnapshotModal)"
      @close="resetMemorySnapshotModal"
      @update:filename="
        (index, name) => (memorySnapshotModal.vm[index].filename = name)
      "
      @create="createMemorySnapshot(memorySnapshotModal.vm)" />
    <TriggerAppsModal
      v-model:active="appsModal.active"
      v-model:apps="appsModal.apps"
      :triggerable="appsModal.triggerable"
      :experiment="experiment.name"
      @close="resetAppsModal"
      @trigger="startApps(appsModal.apps)" />
    <OpticalDiscModal
      v-model:active="opticalDiscModal.active"
      v-model:disc="opticalDiscModal.disc"
      :vm-name="opticalDiscModal.vmName"
      :current="opticalDiscModal.current"
      :isos="opticalDiscModal.isos"
      :loading="opticalDiscModal.loading"
      :error="opticalDiscModal.error"
      @close="resetOpticalDiscModal"
      @insert="insertOpticalDisc()"
      @eject="ejectOpticalDisc()" />
    <div class="experiment-header">
      <ExperimentSummary
        class="experiment-header-summary"
        :name="$route.params.id"
        :experiment="experiment"
        :running="runningVms" />
      <ExperimentAppsBox
        v-if="experiment.apps?.length"
        class="experiment-header-apps"
        :experiment="$route.params.id"
        :apps="experiment.apps"
        :states="appStates"
        @trigger="getApps" />
    </div>
    <b-notification
      v-if="experiment.interrupted"
      class="start-interrupted"
      type="is-warning"
      :closable="false"
      role="status">
      <b>Start interrupted:</b> {{ experiment.interrupted }}. Restart the
      experiment to run its delayed VMs and post-start apps.
    </b-notification>
    <b-field position="is-right" grouped class="exp-toolbar">
      <!-- Multi-VM options shown next to search -->
      <VmSelectionToolbar
        v-if="isMultiVmSelected"
        v-model:modify-open="showModifyStateBar"
        :selected="vmSelectedArray"
        :experiment="experiment.name"
        @act="actOnSelection" />
      <!-- the VM table's filters and paging, left of the search box -->
      <div v-if="vmFiltersShown" class="control vm-table-filters">
        <VmHostFilter
          :options="hostChoices"
          :picked="hostFilter"
          :fits="hostsFit"
          @update:picked="filterByHosts" />
        <VmLabelFilter
          :options="labelChoices"
          :picked="labelFilter"
          :fits="labelsFit"
          @update:picked="filterByLabels" />
        <b-switch
          v-if="paginationNeeded"
          v-model="table.isPaginated"
          @update:modelValue="updateTable()"
          type="is-light"
          >Paginate</b-switch
        >
      </div>

      <b-field position="is-right" class="exp-toolbar-search">
        <b-field
          v-if="
            activeTab == TAB.FILES &&
            roleAllowed('experiments/files', 'list', experiment.name)
          ">
          <b-tooltip label="search on a specific category" type="is-light">
            <b-select
              aria-label="Filter files by category"
              v-model="fileCategory"
              @update:modelValue="(value) => assignCategory(value)"
              placeholder="All Categories">
              <option
                v-for="(category, index) in fileCategories"
                :key="index"
                :value="category">
                {{ category }}
              </option>
            </b-select>
          </b-tooltip>
        </b-field>
        <b-field :addons="false" class="has-addons">
          <b-autocomplete
            v-model="search.filter"
            :placeholder="searchPlaceholder"
            icon="search"
            :data="searchOptions"
            @typing="searchVMs"
            @select="(option) => searchVMs(option)">
            <!-- netflow is filtered in place; there is nothing to list here -->
            <template v-if="activeTab != TAB.NETFLOW" #empty
              >No results found</template
            >
          </b-autocomplete>
          <p v-if="search.filter || fileCategory" class="control">
            <button
              aria-label="Clear VM search"
              class="button input-button"
              @click="
                searchVMs('');
                fileCategory = null;
              ">
              <b-icon icon="window-close"></b-icon>
            </button>
          </p>
        </b-field>

        <b-field>
          <b-tooltip label="stop the experiment" type="is-light" :delay="500">
            <b-button
              aria-label="Stop experiment"
              v-if="roleAllowed('experiments/stop', 'update', experiment.name)"
              class="button is-danger"
              icon-right="stop"
              @click="stop">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field
          v-if="roleAllowed('experiments/netflow', 'create', experiment.name)">
          <b-tooltip :label="netflow.tooltip" type="is-light">
            <b-button
              :aria-label="
                netflow.capturing
                  ? 'Stop netflow capture'
                  : 'Start netflow capture'
              "
              :class="netflow.capturing ? 'is-danger' : 'is-success'"
              :loading="netflow.starting || netflow.stopping"
              icon-left="circle-nodes"
              @click="handleNetflow(!netflow.capturing)">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field>
          <b-tooltip
            label="select visible columns"
            type="is-light"
            :delay="500">
            <b-dropdown
              multiple
              aria-role="list"
              position="is-bottom-left"
              style="margin-right: 1rem">
              <template #trigger>
                <b-button
                  aria-label="Select visible columns"
                  type="is-light"
                  icon-left="columns" />
              </template>
              <b-dropdown-item
                v-for="toggle in columnToggles"
                :key="toggle.key"
                custom
                :focusable="false">
                <b-checkbox
                  v-model="columnVisibility[toggle.key]"
                  @update:modelValue="persistColumnVisibility(toggle)"
                  size="is-small"
                  type="is-primary">
                  {{ toggle.label }}
                </b-checkbox>
              </b-dropdown-item>
            </b-dropdown>
          </b-tooltip>
        </b-field>
      </b-field>
    </b-field>

    <div style="margin-top: -4em">
      <b-tabs v-model="activeTab">
        <b-tab-item :value="TAB.VMS" label="VMs" icon="desktop">
          <b-notification
            v-if="runtimeUnknown"
            class="runtime-unknown"
            type="is-warning"
            :closable="false"
            role="status">
            minimega did not answer. Showing the VMs as configured.
          </b-notification>
          <VmTable
            ref="vmTable"
            v-model:selected="vmSelectedArray"
            v-model:check-all="checkAll"
            :vms="experiment.vms"
            :experiment="experiment.name"
            :table="table"
            :columns="columnVisibility"
            :empty-text="vmsEmptyText"
            :screenshots-due="screenshotsDue"
            :capture-pending="capturePending"
            :capture-all-pending="captureAllPending"
            :action-pending="vmActionPending"
            @page-change="onPageChange"
            @sort="onSort"
            @info="getInfo"
            @details="showVmDetails"
            @capture="handlePcap"
            @change-vlan="showVlanModal"
            @edit-labels="showTagsModal"
            @action="onVmAction" />
        </b-tab-item>
        <b-tab-item
          :value="TAB.FILES"
          label="Files"
          icon="file-alt"
          :visible="roleAllowed('experiments/files', 'list', experiment.name)">
          <ExperimentFilesTab
            ref="filesTab"
            paginate-key="running-files"
            :filter="filesFilter"
            :category="fileCategory"
            @categories="(list) => (fileCategories = list)"
            @found="addSearchHistory"
            @waiting="(on) => (isWaiting = on)" />
        </b-tab-item>
        <b-tab-item :value="TAB.VNC" label="VNC" icon="arrow-pointer">
          <VncTiles
            v-model:zoom="vncZoom"
            :vms="experiment.vms"
            :experiment="experiment.name"
            :width="vncWidth"
            :screenshots-due="screenshotsDue"
            @info="getInfo" />
        </b-tab-item>
        <b-tab-item
          :value="TAB.NETFLOW"
          label="Netflow"
          icon="circle-nodes"
          v-if="roleAllowed('experiments/netflow', 'get', experiment.name)">
          <NetflowPanel
            ref="netflowPanel"
            :experiment="experiment.name"
            :lines="netflow.lines"
            :starting="netflow.starting"
            :search="netflowSearch" />
        </b-tab-item>
      </b-tabs>
    </div>
    <b-loading
      :is-full-page="false"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import { getCurrentInstance } from 'vue';
  import ExperimentAppsBox from '@/components/experiment/ExperimentAppsBox.vue';
  import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
  import ExperimentSummary from '@/components/experiment/ExperimentSummary.vue';
  import VMLabelsModal from '@/components/VMLabelsModal.vue';
  import NetflowPanel from '@/components/experiment/NetflowPanel.vue';
  import OpticalDiscModal from '@/components/experiment/OpticalDiscModal.vue';
  import PortForwardModal from '@/components/experiment/PortForwardModal.vue';
  import RedeployModal from '@/components/experiment/RedeployModal.vue';
  import TriggerAppsModal from '@/components/experiment/TriggerAppsModal.vue';
  import VlanModal from '@/components/experiment/VlanModal.vue';
  import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
  import VmFilenamesModal from '@/components/experiment/VmFilenamesModal.vue';
  import VmHostFilter from '@/components/experiment/VmHostFilter.vue';
  import VmLabelFilter from '@/components/experiment/VmLabelFilter.vue';
  import VmSelectionToolbar from '@/components/experiment/VmSelectionToolbar.vue';
  import VmTable from '@/components/experiment/VmTable.vue';
  import VncTiles from '@/components/experiment/VncTiles.vue';
  import {
    partitionVMsForAction,
    restorePendingKey,
    skippedVMsText,
    vlanPendingKey,
  } from '@/components/experiment/vmActions.js';
  import {
    VM_EVENT_TYPES,
    applyVmEvent,
    eventVmName,
    findVm,
    replaceVm,
    useVmRows,
  } from '@/components/experiment/vmRows.js';
  import { vmScreenshot, vncHref } from '@/components/experiment/vmScreens.js';
  import { createRunningCount } from '@/components/experiment/runningCount.js';
  import {
    DEFAULT_APPS,
    appStateAfter,
    hostOptions,
    labelOptions,
  } from '@/components/experiment/experimentHeader.js';
  import { useNetflowCapture } from '@/components/experiment/useNetflowCapture.js';
  import { useVmCaptures } from '@/components/experiment/useVmCaptures.js';
  import {
    TAB,
    sameQuery,
    searchFromQuery,
    searchKey,
    tabFromQuery,
    tabQuery,
    withoutVm,
  } from '@/components/experiment/experimentTabs.js';
  import {
    minuteStamp,
    timestampParts,
  } from '@/components/experiment/timestamp.js';
  import { addSearch } from '@/utils/searchHistory.js';
  import {
    addWsHandler,
    onWsReconnect,
    removeWsHandler,
    sendWsMsg,
  } from '@/utils/websocket';
  import { usePhenixStore } from '@/store';
  import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
  import { debounce } from 'lodash-es';
  import { readPref, writePref } from '@/utils/prefs.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import * as disksApi from '@/api/disks.js';
  import * as experimentsApi from '@/api/experiments.js';
  import * as vmsApi from '@/api/vms.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { cachePage } from '@/utils/pageCache.js';
  import { FreshnessMeta } from '@/utils/freshness.js';
  import {
    experimentKey,
    fetchRunningExperiment,
    withoutScreenshots,
  } from '@/utils/pageData.js';
  import { useTable } from '@/utils/useTable.js';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import {
    offerForcedStop,
    stopCanBeForced,
  } from '@/utils/experimentActions.js';
  import {
    backingChain,
    baseName,
    diskList,
    filesDir,
    findDisk,
    isoDisks,
  } from '@/utils/diskChain.js';
  import { namedList, pluralWord } from '@/utils/plural.js';
  import { useJobStore } from '@/utils/jobs.js';
  import {
    confirmOperation,
    notifyOutcome,
    outcomeFromJob,
    outcomeFromSettled,
  } from '@/utils/operations.js';
  import { wsMsgFits } from '@/utils/wsMessage.js';

  // how long a VM shows "Loading Screenshot" before "Not Available"; minimega
  // takes screenshots one at a time, so large experiments need a while
  const SCREENSHOT_WAIT_MS = 60000;

  // the screenshot width the server uses until the page asks for another
  const DEFAULT_SCREENSHOT_WIDTH = 200;

  // the CD-ROM picker with nothing open
  function emptyOpticalDiscModal() {
    return {
      active: false,
      vmName: null,
      // the ISO inserted when it opened, '' for none
      current: '',
      // the ISO picked
      disc: null,
      // the ISO images to pick from, while loading and after
      isos: [],
      loading: false,
      error: null,
    };
  }

  // what each power action did, for its outcome summary
  const VM_ACTION_DONE = {
    start: 'Started',
    pause: 'Paused',
    restart: 'Restarted',
    shutdown: 'Shut down',
    kill: 'Killed',
    resetDisk: 'Reset the disks of',
  };

  // the page's handler for each action the selection toolbar takes
  const SELECTION_HANDLERS = {
    start: 'startVm',
    pause: 'pauseVm',
    memorySnapshot: 'queueMemorySnapshotVMs',
    commit: 'diskImage',
    snapshot: 'captureSnapshot',
    redeploy: 'redeploy',
    resetDisk: 'resetVmState',
    restart: 'restartVm',
    shutdown: 'shutdownVm',
    kill: 'killVm',
  };

  export default {
    emits: ['running'],
    components: {
      ExperimentAppsBox,
      ExperimentFilesTab,
      ExperimentSummary,
      NetflowPanel,
      OpticalDiscModal,
      PortForwardModal,
      RedeployModal,
      TriggerAppsModal,
      VlanModal,
      VmDetailsCard,
      VmFilenamesModal,
      VmHostFilter,
      VmLabelFilter,
      VmSelectionToolbar,
      VmTable,
      VncTiles,
    },
    setup() {
      const { proxy } = getCurrentInstance();
      const { table } = useTable({
        name: 'running-vms',
        fields: { total: 0, sortColumn: 'name', sortOrder: 'asc' },
      });
      return {
        table,
        // the seq of each VM's last change, so a VM list read before it
        // does not undo it
        vmSeqs: useVmRows(),
        runningCount: createRunningCount(),
        ...useNetflowCapture(),
        ...useVmCaptures({
          fanOut: (...args) => proxy.fanOut(...args),
        }),
        roleAllowed,
        vmScreenshot,
        TAB,
      };
    },

    async beforeUnmount() {
      this.unmounting = true;
      this.searchVMs.cancel();
      this.pageResize?.disconnect();
      document.removeEventListener('visibilitychange', this.onVisibilityChange);
      removeWsHandler(this.handleWs);
      this.offWsReconnect();
      clearTimeout(this.screenshotsTimer);
      this.unsubscribeVms();

      this.loader.stop();
    },

    mounted() {
      // the page changes height without the window resizing: the tab left
      // for the netflow tab stays laid out until its slide ends, and the VM
      // table fills in after the netflow table may have been sized
      this.pageResize = new ResizeObserver(() =>
        this.$refs.netflowPanel?.fit(),
      );
      this.pageResize.observe(this.$el);
      // a link to the Files tab opens it without the activeTab watcher
      if (this.activeTab === TAB.FILES) this.$refs.filesTab?.reload();
    },

    async created() {
      // the URL the page opened on, which already names its tab and search
      this.syncedQuery = this.$route.query;
      this.searchVMs = debounce((term) => {
        this.search.filter = term ?? '';
        this.syncQuery();
        switch (this.activeTab) {
          case TAB.VMS:
          case TAB.VNC:
            // before the first load, apply() lists the VMs with this filter
            if (this.experiment.name) this.updateTable();
            break;
          case TAB.FILES:
            this.$refs.filesTab?.reload();
            break;
          // the netflow tab filters what it already has
        }
      }, 250);
      addWsHandler(this.handleWs);
      document.addEventListener('visibilitychange', this.onVisibilityChange);
      // the server drops VM subscriptions and the screenshot size when the
      // websocket reconnects
      this.offWsReconnect = onWsReconnect(
        () => {
          if (this.vncWidth != DEFAULT_SCREENSHOT_WIDTH) {
            this.setVncScreenshotRes(this.vncWidth);
          }
          // a hidden tab lists its VMs, and so gets screenshots, once shown
          if (this.experiment.name && !document.hidden) this.updateTable();
        },
        { spread: true },
      );
      this.loadColumnVisibility();
      // cached so the page opens at once; the header shows how old the copy
      // is while a fresh one loads
      this.loader = createPageLoader({
        key: experimentKey(this.$route.params.id),
        fetch: (signal, meta) =>
          fetchRunningExperiment(this.$route.params.id, signal, meta),
        apply: (experiment) => {
          if (!experiment.running) {
            // stopped since the page was opened; Base shows the stopped view
            this.$emit('running', false);
            return;
          }
          // the VM table arrives over the websocket; once it has, a reload
          // keeps it (and its screenshots, pagination, and filter)
          if (this.vmsLoaded) {
            this.experiment = { ...experiment, vms: this.experiment.vms };
          } else {
            const shots = new Map(
              (this.experiment.vms ?? []).map((vm) => [vm.name, vm.screenshot]),
            );
            this.experiment = {
              ...experiment,
              vms: (experiment.vms ?? []).map((vm) => ({
                ...vm,
                screenshot: vm.screenshot || shots.get(vm.name),
              })),
            };
            this.table.total = experiment.vm_count;
          }

          // Once is enough: the list comes back over the websocket and VM
          // events keep it current, and each list has the server ask minimega
          // for every VM. The cached copy and the fresh one are both applied
          // on open, so listing on each would list twice.
          if (!this.vmsRequested) {
            this.vmsRequested = true;
            this.updateTable();
          }
          this.openLinkedVm();
        },
        // the header's refresh button lists the VMs again too
        refresh: () => {
          this.updateTable();
          return this.loader.load();
        },
      });
      this.loader.start();
      this.loadAppStates();

      try {
        await experimentsApi.getNetflowCapture(this.$route.params.id);
        // the page may have been left while waiting
        if (!this.unmounting) this.handleNetflow(true, false);
      } catch {}
    },

    computed: {
      searchOptions() {
        return [...this.searchHistory].sort();
      },

      // the VM table's search and the Files tab's, kept while another tab is
      // open
      vmFilter() {
        return this.filterOf(TAB.VMS);
      },

      filesFilter() {
        return this.filterOf(TAB.FILES);
      },

      searchPlaceholder() {
        switch (this.activeTab) {
          case TAB.FILES:
            return 'Find a File';
          case TAB.NETFLOW:
            return 'Search netflow';
          default:
            return 'Find a VM';
        }
      },

      // the server's features, from the store: they may arrive after the page
      // opens, so a copy taken when it opened could miss them
      features() {
        return usePhenixStore().features ?? [];
      },

      // the search box's text, which the netflow tab filters by while open
      netflowSearch() {
        return this.activeTab == TAB.NETFLOW ? (this.search.filter ?? '') : '';
      },

      vmsEmptyText() {
        // the websocket lists VMs only for roles allowed to
        if (!this.vmsLoaded && roleAllowed('vms', 'list')) {
          return loadingText('VMs');
        }
        const checked = [
          this.labelFilter.length > 0 && 'labels',
          this.hostFilter.length > 0 && 'hosts',
        ].filter(Boolean);
        if (this.vmFilter && checked.length) {
          return `No VMs match your search and the checked ${checked.join(' and ')}`;
        }
        if (checked.length === 2) {
          return 'No VMs match the checked labels and hosts';
        }
        if (checked[0] === 'labels') {
          return 'No VMs have any of the checked labels';
        }
        if (checked[0] === 'hosts') {
          return 'No VMs run on any of the checked hosts';
        }
        if (this.vmFilter) return 'No VMs match your search';
        return 'This experiment has no VMs';
      },

      // the cluster hosts the VM table can filter by
      hostChoices() {
        return hostOptions(this.vmHosts, this.experiment.vms, this.hostFilter);
      },

      // whether the VMs tab shows its filters and Paginate in the toolbar
      vmFiltersShown() {
        return (
          this.activeTab == TAB.VMS &&
          (this.paginationNeeded ||
            this.labelChoices.length > 0 ||
            this.labelFilter.length > 0 ||
            this.hostChoices.length > 0 ||
            this.hostFilter.length > 0)
        );
      },

      // the labels the VM table can filter by
      labelChoices() {
        return labelOptions(
          this.vmLabels,
          this.experiment.vms,
          this.labelFilter,
        );
      },

      vncWidth() {
        return this.activeTab == TAB.VNC
          ? this.vncZoom * 50
          : DEFAULT_SCREENSHOT_WIDTH;
      },

      // by all the VMs, so a search or label filter leaves the bar as it was
      paginationNeeded() {
        const all = Math.max(this.table.total, this.experiment.vm_count ?? 0);
        return all > this.table.perPage;
      },

      // The VM whose details are open: its live table row over the details
      // fetched when it opened, which alone carry the annotations.
      expModalVm() {
        const vm = this.expModal.vm ?? {};
        const row = vm.name
          ? this.experiment.vms?.find((v) => v.name == vm.name)
          : null;
        if (!row || row === vm) return vm;
        return {
          ...vm,
          ...row,
          annotations: vm.annotations,
          screenshot: row.screenshot || vm.screenshot,
        };
      },

      // the header checkbox: whether every VM on this page that can be
      // selected is, and selecting all of them or none
      checkAll: {
        get() {
          const names = this.selectableVmNames;
          return (
            names.length > 0 &&
            names.every((name) => this.vmSelectedArray.includes(name))
          );
        },
        set(on) {
          this.vmSelectedArray = on ? [...this.selectableVmNames] : [];
        },
      },

      selectableVmNames() {
        return (this.experiment.vms ?? [])
          .filter((vm) => !vm.external)
          .map((vm) => vm.name);
      },

      isMultiVmSelected() {
        return this.vmSelectedArray.length > 0;
      },
    },

    methods: {
      getUserStorageKey(storageKey) {
        var user = usePhenixStore().username;
        return user + '.' + storageKey;
      },

      loadColumnVisibility() {
        for (const toggle of this.columnToggles) {
          const value = readPref(this.getUserStorageKey(toggle.storageKey));
          if (value !== null) {
            this.columnVisibility[toggle.key] = value == 'true';
          }
        }
      },

      persistColumnVisibility(toggle) {
        writePref(
          this.getUserStorageKey(toggle.storageKey),
          this.columnVisibility[toggle.key],
        );
      },

      vncLoc(vm) {
        return vncHref(this.$router, this.$route.params.id, vm);
      },

      setVncScreenshotRes(width) {
        let msg = {
          resource: {
            type: 'metadata/screenshot',
            action: 'resize',
          },
          request: {
            size: width + '',
          },
        };

        sendWsMsg(msg);
      },

      // the request for the table's VMs, with these labels and hosts checked
      vmListMsg({ labels = this.labelFilter, hosts = this.hostFilter } = {}) {
        let number = this.table.currentPage;
        let size = this.table.perPage;

        if (!this.table.isPaginated) {
          number = 0;
          size = this.experiment.vm_count;
        }

        return {
          resource: {
            type: 'experiment/vms',
            name: this.$route.params.id,
            action: 'list',
          },
          request: {
            sort_column: this.table.sortColumn,
            sort_asc: this.table.sortOrder === 'asc',
            page_number: number,
            page_size: size,
            filter: this.vmFilter,
            labels: labels.map(({ key, value }) => ({ key, value })),
            hosts,
          },
        };
      },

      updateTable() {
        sendWsMsg(this.vmListMsg());
      },

      // whether the VM list request with these labels is short enough to send
      labelsFit(labels) {
        return wsMsgFits(this.vmListMsg({ labels }));
      },

      // whether the VM list request with these hosts is short enough to send
      hostsFit(hosts) {
        return wsMsgFits(this.vmListMsg({ hosts }));
      },

      // lists the VMs on any of these hosts, from the first page
      filterByHosts(hosts) {
        this.hostFilter = hosts;
        this.table.currentPage = 1;
        this.updateTable();
      },

      // lists the VMs with any of these labels, from the first page
      filterByLabels(labels) {
        this.labelFilter = labels;
        this.table.currentPage = 1;
        this.updateTable();
      },

      onPageChange(page) {
        this.table.currentPage = page;
        this.updateTable();
      },

      onSort(column, order) {
        this.table.sortColumn = column;
        this.table.sortOrder = order;
        this.updateTable();
      },

      showTagsModal(vm) {
        this.$buefy.modal.open({
          component: VMLabelsModal,
          trapFocus: true,
          hasModalCard: true,
          props: {
            vmName: vm.name,
            experiment: this.$route.params.id,
            tags: vm.tags,
          },
          events: { saved: () => this.refreshVmDetails(vm.name) },
        });
      },

      // Apps publish their runs on apps/<app>, scheduled runs included: each
      // updates the app's tile, and only the apps this page triggered get a toast.
      handleAppTrigger(msg) {
        const app = msg.resource.type.slice('apps/'.length);
        if (msg.resource.name !== this.$route.params.id) return;
        const state = appStateAfter(msg.resource.action);
        if (state) this.appStates = { ...this.appStates, [app]: state };

        const i = this.triggeredApps.indexOf(app);
        if (i === -1) return;

        switch (msg.resource.action) {
          case 'success':
            this.$buefy.toast.open({
              message: 'phēnix app ' + app + ' was triggered successfully.',
              type: 'is-success',
            });
            break;
          case 'error':
            showError(
              'Triggering phēnix app ' + app + ' failed',
              msg.result?.error,
            );
            break;
          case 'canceled':
            this.$buefy.toast.open({
              message: 'phēnix app ' + app + ' was canceled.',
              type: 'is-warning',
            });
            break;
          default:
            return;
        }

        this.triggeredApps.splice(i, 1);
      },

      // a queued redeploy finished or failed: send the next or close the modal
      advanceRedeployQueue(name) {
        const queue = this.redeployModal.actionsQueue;
        const index = queue.findIndex((action) => action.name == name);
        if (index === -1) return;

        queue.splice(index, 1);
        if (queue.length > 0) {
          vmsApi
            .redeployVm(queue[0].exp, queue[0].name, queue[0].body)
            .catch((err) => {
              useErrorNotification(err);
            });
        } else {
          this.redeployModal.active = false;
          this.resetRedeployModal();
          this.isWaiting = false;
        }
      },

      // Stopped or deleted from elsewhere (the CLI, another window or user):
      // the page has nothing left to show, and leaving it tells the server to
      // stop screenshotting the VMs.
      handleExperimentEvent(msg) {
        if (
          msg.resource.name !== this.$route.params.id ||
          !['stop', 'delete'].includes(msg.resource.action)
        ) {
          return;
        }

        // this window's own Stop leaves the page itself
        if (this.isWaiting) return;

        this.$buefy.toast.open({
          message:
            'The ' +
            msg.resource.name +
            ' experiment was ' +
            (msg.resource.action == 'stop' ? 'stopped' : 'deleted') +
            ' elsewhere.',
          type: 'is-info',
        });
        this.$router.replace({ name: 'experiments' });
      },

      // the VM list the page asked the websocket for
      handleVmList(msg) {
        if (
          msg.resource.action != 'list' ||
          msg.resource.name !== this.$route.params.id
        ) {
          return;
        }

        this.experiment.vms = this.vmSeqs.listed(
          this.experiment.vms,
          msg.result.vms,
          msg.seq,
        );
        this.runningVms = this.runningCount.listed(
          msg.result.running,
          msg.result.vms,
          this.experiment.vms,
        );
        this.table.total = msg.result.total;
        this.vmLabels = msg.result.labels ?? [];
        this.vmHosts = msg.result.hosts ?? [];
        this.runtimeUnknown = !!msg.result.runtime_unknown;
        this.vmsLoaded = true;

        // a VM still without a screenshot by then has none to show
        this.screenshotsDue = false;
        clearTimeout(this.screenshotsTimer);
        this.screenshotsTimer = setTimeout(() => {
          this.screenshotsDue = true;
        }, SCREENSHOT_WAIT_MS);

        // cache the page without screenshots, unless it is a filtered view or a
        // later page: the page reopens unfiltered on its first page
        if (
          !this.vmFilter &&
          this.labelFilter.length === 0 &&
          this.hostFilter.length === 0 &&
          (!this.table.isPaginated || this.table.currentPage === 1)
        ) {
          cachePage(
            experimentKey(this.$route.params.id),
            {
              ...this.experiment,
              vms: withoutScreenshots(this.experiment.vms),
            },
            Object.assign(new FreshnessMeta(), {
              seq: msg.seq,
              observedAt: Date.now(),
            }),
          );
        }

        // Only add successful searches to the search history
        if (
          searchKey(this.activeTab) === TAB.VMS &&
          this.vmFilter &&
          this.table.total > 0
        ) {
          this.addSearchHistory();
        }

        this.isWaiting = false;
      },

      // a per-VM event: its change to the VM's row and its toast (see
      // vmRows.js), then what the page does about it
      handleVmEvent(msg) {
        const type = msg.resource.type;
        const name = eventVmName(msg, this.$route.params.id, this.experiment);
        if (name === null) return;

        if (type === 'experiment/vm') {
          this.vmSeqs.record(name, msg);
          this.runningVms = this.runningCount.changed(
            name,
            msg.resource.action,
            msg.result,
          );
        }

        const notice = applyVmEvent(this.experiment, msg, name);
        if (notice?.type === 'is-danger') showError(notice.message);
        else if (notice) this.$buefy.toast.open(notice);

        if (type !== 'experiment/vm') return;
        switch (msg.resource.action) {
          case 'cdrom-inserted':
          case 'cdrom-ejected':
            // the event carries no VM, so ask for the one whose disc changed
            // if its details are open; its snapshots and forwards are the same
            if (this.expModal.active && this.expModal.vm.name == name) {
              this.refreshVmDetails(name);
            }
            break;

          case 'redeployed':
          case 'errorRedeploying':
            this.advanceRedeployQueue(name);
            break;
        }
      },

      handleWs(msg) {
        const type = msg.resource.type;
        if (type.startsWith('apps/')) {
          this.handleAppTrigger(msg);
        } else if (type === 'experiment') {
          this.handleExperimentEvent(msg);
        } else if (type === 'experiment/vms') {
          this.handleVmList(msg);
        } else if (VM_EVENT_TYPES.includes(type)) {
          this.handleVmEvent(msg);
        }
      },

      // the images the redeploy modal offers: those inside the minimega files
      // directory and the ones this experiment names outside it
      updateDisks() {
        this.disks = null;

        disksApi
          .listDisks({ expName: this.$route.params.id })
          .then((resp) => {
            this.disks = resp.data.disks ?? [];
          })
          .catch((err) => useErrorNotification(err));
      },

      assignCategory(value) {
        this.fileCategory = value;
        this.$refs.filesTab?.reload();
      },

      async fetchVMDetails(vm) {
        let resp = await vmsApi.getVm(this.$route.params.id, vm.name);

        return resp.data;
      },

      async fetchVMSnapshots(vm) {
        let resp = await vmsApi.listVmSnapshots(this.$route.params.id, vm.name);

        return resp.data;
      },

      async fetchVMForwards(vm) {
        let resp = await vmsApi.listVmForwards(this.$route.params.id, vm.name);

        return resp.data;
      },

      // fetches a VM's details into its open details modal and its table row
      async refreshVmDetails(name) {
        try {
          const details = await this.fetchVMDetails({ name });
          replaceVm(this.experiment, details);
          if (this.expModal.active && this.expModal.vm.name == name) {
            this.expModal.vm = details;
          }
        } catch (err) {
          useErrorNotification(err);
        }
      },

      // Opens the details of the VM a link names (?vm=web), once: its full
      // details, or the table's row for a role that may not get the VM.
      async openLinkedVm() {
        const name = this.$route.query.vm;
        if (typeof name !== 'string' || !name || name === this.linkedVm) return;
        this.linkedVm = name;
        if (!roleAllowed('vms', 'get', `${this.experiment.name}/${name}`)) {
          const row = this.experiment.vms?.find((vm) => vm.name == name);
          if (row) this.showVmDetails(row);
          return;
        }
        try {
          const vm = await this.fetchVMDetails({ name });
          if (!this.unmounting && this.linkedVm == name) this.getInfo(vm);
        } catch (err) {
          useErrorNotification(err);
        }
      },

      // opens the details of a VM the role may not get, as the table has it
      showVmDetails(vm) {
        this.expModal.active = true;
        this.expModal.vm = vm;
      },

      async getInfo(vm) {
        if (vm.busy) {
          this.$buefy.dialog.alert({
            title: 'VM Busy',
            message: [
              'VM',
              vm.name,
              'is currently busy and not available for another action',
            ].join(' '),
            confirmText: 'OK',
          });

          return;
        }

        const fullName = this.experiment.name + '/' + vm.name;
        this.expModal.vm = vm;
        this.expModal.fullName = fullName;
        this.expModal.backingChain = null;
        this.expModal.disk = null;
        this.expModal.cdRomDisk = null;
        this.expModal.active = true;
        this.loadBackingChain(vm);

        try {
          // roles without these permissions still get the rest of the details
          const [details, snapshots, forwards] = await Promise.all([
            this.fetchVMDetails(vm),
            this.roleAllowed('vms/snapshots', 'list', fullName)
              ? this.fetchVMSnapshots(vm)
              : { snapshots: [] },
            this.roleAllowed('vms/forwards', 'list', fullName)
              ? this.fetchVMForwards(vm)
              : { listeners: [] },
          ]);
          if (!this.expModal.active || this.expModal.vm.name != vm.name) return;

          this.expModal.vm = details;

          if (snapshots.snapshots && snapshots.snapshots.length > 0) {
            this.expModal.snapshots = snapshots.snapshots;
          }

          this.showForwards(forwards.listeners);
        } catch (err) {
          useErrorNotification(err);
        }
      },

      // Replaces the open VM's annotations, from its details card; done(true)
      // tells the card they are saved.
      async saveAnnotations(annotations, done) {
        const name = this.expModal.vm.name;
        try {
          const resp = await vmsApi.updateVm(this.$route.params.id, name, {
            annotations,
          });
          if (this.expModal.active && this.expModal.vm.name == name) {
            this.expModal.vm = {
              ...this.expModal.vm,
              annotations: resp.data?.annotations ?? annotations,
            };
          }
          done(true);
        } catch (err) {
          useErrorNotification(err);
          done(false);
        }
      },

      // The open VM's disk and the images backing it, and the ISO image in
      // its CD-ROM drive, from the Disks page's list. Roles that can't list
      // disks see their paths alone.
      async loadBackingChain(vm) {
        if (!(vm.disk || vm.cdRom) || !roleAllowed('disks', 'list')) return;

        try {
          const disks = await diskList();
          if (this.expModal.active && this.expModal.vm.name == vm.name) {
            this.expModal.backingChain = backingChain(disks, vm.disk);
            this.expModal.disk = findDisk(disks, vm.disk);
            this.expModal.cdRomDisk = findDisk(disks, vm.cdRom);
            this.expModal.filesDir = filesDir(disks);
          }
        } catch (err) {
          useErrorNotification(err);
        }
      },

      async captureSnapshot(names) {
        const { allowed, skipped } = partitionVMsForAction(
          'snapshot',
          names,
          this.experiment.vms,
        );
        if (allowed.length === 0) {
          this.alertSkipped(skipped);
          return;
        }

        const time = minuteStamp();

        const confirmed = await confirmOperation(this.$buefy, {
          title: pluralWord(
            allowed.length,
            'Create a VM Snapshot',
            'Create VM Snapshots',
          ),
          message: `This will create ${pluralWord(allowed.length, 'a snapshot', 'snapshots')} of ${namedList(allowed, 'VM')}.`,
          confirmText: 'Create',
          type: 'is-success',
          skipped,
          noun: 'VM',
        });
        if (!confirmed) return;

        this.setVmActionPending('snapshot', allowed, true);
        try {
          await this.fanOut(allowed, 'Snapshotted', (vmName) =>
            vmsApi.createVmSnapshot(this.$route.params.id, vmName, time),
          );
        } finally {
          this.setVmActionPending('snapshot', allowed, false);
        }
        const open = this.expModal.vm.name;
        if (this.expModal.active && allowed.includes(open)) {
          this.reloadSnapshots(open);
        }
      },

      // lists the open VM's snapshots again, in its details card
      async reloadSnapshots(name) {
        try {
          const { snapshots } = await this.fetchVMSnapshots({ name });
          if (this.expModal.active && this.expModal.vm.name == name) {
            this.expModal.snapshots = snapshots ?? [];
          }
        } catch (err) {
          useErrorNotification(err);
        }
      },

      restoreSnapshot(name, snapshot) {
        this.$buefy.dialog.confirm({
          title: 'Restore a VM Snapshot',
          message: 'This will revert the ' + name + ' VM to ' + snapshot + '.',
          cancelText: 'Cancel',
          confirmText: 'Revert',
          type: 'is-success',
          hasIcon: true,
          onConfirm: async () => {
            const key = restorePendingKey(snapshot);
            this.setVmActionPending(key, [name], true);
            try {
              await vmsApi.restoreVmSnapshot(
                this.$route.params.id,
                name,
                snapshot,
              );
            } catch (err) {
              useErrorNotification(err);
            } finally {
              this.setVmActionPending(key, [name], false);
            }
          },
        });
      },

      diskImage(name) {
        name = this.actionableVMs('commit', name);
        if (name.length === 0) return;

        const t = timestampParts();
        const date = `${t.year}${t.month}${t.day}`;
        const time = `${t.hours}${t.minutes}${t.seconds}`;
        let vms = this.experiment.vms;
        name.forEach((arg) => {
          for (let i = 0; i < vms.length; i++) {
            if (vms[i].name == arg) {
              // the disk's file name up to its first "_" when it holds a date
              // and time, else up to its first "."
              const file = baseName(vms[i].disk);
              const stem = file.split(/_\d{14}/.test(file) ? '_' : '.')[0];
              const filename = `${vms[i].name}_${stem}_${date}${time}`;
              this.diskImageModal.vm.push({
                dateTime: date + time + '',
                name: vms[i].name,
                filename: filename,
                nameErrType: '',
                nameErrMsg: '',
              });
            }
          }
        });

        this.diskImageModal.active = true;
      },

      backingImage(vm) {
        const names = vm.map((arg) => arg.name);
        const files = Object.fromEntries(
          vm.map((arg) => [arg.name, arg.filename + '.qc2']),
        );
        this.$buefy.dialog.confirm({
          title: pluralWord(
            names.length,
            'Create a Disk Image',
            'Create Disk Images',
          ),
          message: `This will create ${pluralWord(names.length, 'a backing image', 'backing images')} of ${namedList(names, 'VM')}.`,
          cancelText: 'Cancel',
          confirmText: 'Create',
          type: 'is-success',
          hasIcon: true,
          onConfirm: async () => {
            this.diskImageModal.active = false;
            this.resetDiskImageModal();
            this.setVmActionPending('commit', names, true);
            try {
              await this.fanOut(names, 'Created backing images of', (name) =>
                vmsApi.commitVmDisk(this.$route.params.id, name, files[name]),
              );
            } finally {
              this.setVmActionPending('commit', names, false);
            }
          },
        });
      },

      queueMemorySnapshotVMs(name) {
        name = this.actionableVMs('memorySnapshot', name);
        if (name.length === 0) return;

        const t = timestampParts();
        const date = `${t.year}${t.month}${t.day}`;
        const time = `${t.hours}${t.minutes}${t.seconds}`;
        let vms = this.experiment.vms;
        name.forEach((arg) => {
          for (let i = 0; i < vms.length; i++) {
            if (vms[i].name == arg) {
              var filename = date + '_' + time;

              filename =
                vms[i].name +
                '_' +
                filename.substring(filename.lastIndexOf('/') + 1);

              this.memorySnapshotModal.vm.push({
                dateTime: date + time + '',
                name: vms[i].name,
                filename: filename,
                nameErrType: '',
                nameErrMsg: '',
              });
            }
          }
        });
        this.memorySnapshotModal.active = true;
      },

      async createMemorySnapshot(vm) {
        const files = Object.fromEntries(
          vm.map((arg) => [arg.name, arg.filename + '.elf']),
        );
        const names = Object.keys(files);
        this.memorySnapshotModal.active = false;
        this.resetMemorySnapshotModal();
        this.setVmActionPending('memorySnapshot', names, true);
        try {
          await this.fanOut(names, 'Created memory snapshots of', (name) =>
            vmsApi.createVmMemorySnapshot(
              this.$route.params.id,
              name,
              files[name],
            ),
          );
        } finally {
          this.setVmActionPending('memorySnapshot', names, false);
        }
      },

      // Sends one request for each name, then tells in one summary how they
      // went, offering to send again those that may work if retried.
      async fanOut(names, verbPast, send, noun = 'VM') {
        const results = await Promise.allSettled(names.map(send));
        notifyOutcome(this.$buefy, {
          verbPast,
          noun,
          items: outcomeFromSettled(names, results),
          onRetry: (retry) => this.fanOut(retry, verbPast, send, noun),
        });
      },

      killVm(names) {
        this.confirmVmAction('kill', names, (vms) => ({
          title: `Kill the ${pluralWord(vms.length, 'VM')}`,
          message:
            `This will kill ${namedList(vms, 'VM')}. You will not be able ` +
            `to restore ${pluralWord(vms.length, 'it', 'them')} until you ` +
            `restart the ${this.$route.params.id} experiment!`,
          confirmText: pluralWord(vms.length, 'KILL IT!', 'KILL THEM!'),
          type: 'is-danger',
        }));
      },

      stop() {
        this.$buefy.dialog.confirm({
          title: 'Stop the Experiment',
          message:
            'This will stop the ' + this.$route.params.id + ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Stop',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => this.stopExperiment(),
        });
      },

      // Stops the experiment; one whose VMs minimega could not kill is still
      // running, and the user may force its stop.
      stopExperiment(force = false) {
        const name = this.$route.params.id;
        this.isWaiting = true;

        return experimentsApi
          .stopExperiment(name, { force })
          .then(() => {
            this.$router.replace({ name: 'experiments' });
          })
          .catch((err) => {
            useErrorNotification(err);
            this.isWaiting = false;
            if (!force && stopCanBeForced(err)) {
              offerForcedStop(this.$buefy, name, () =>
                this.stopExperiment(true),
              );
            }
          });
      },

      // runs a VM action from the details modal or the table's Actions
      // column; power actions but kill run without asking, unlike the toolbar's
      onVmAction(action, vm) {
        switch (action) {
          case 'start':
          case 'pause':
          case 'restart':
          case 'shutdown':
            this.runVmAction(action, vm.name);
            break;
          case 'kill':
            this.killVm(vm.name);
            break;
          case 'redeploy':
            this.redeploy(vm.name);
            break;
          case 'resetDisk':
            this.resetVmState(vm.name);
            break;
          case 'snapshot':
            this.captureSnapshot(vm.name);
            break;
          case 'commit':
            this.diskImage(vm.name);
            break;
          case 'memorySnapshot':
            this.queueMemorySnapshotVMs(vm.name);
            break;
          case 'portForward':
            this.showPortForwardDialog(vm.name);
            break;
          case 'cdrom':
            this.showChangeDisc(vm);
            break;
          case 'mount':
            this.showMountDialog(vm.name);
            break;
          case 'captureAll':
            this.captureAllInterfaces(vm);
            break;
          case 'stopCaptures':
            this.stopAllCaptures(vm);
            break;
        }
      },

      showVlanModal(vm, index) {
        this.vlanModal.active = true;
        this.vlanModal.vmName = vm.name;
        this.vlanModal.vmFromNet = vm.networks[index];
        this.vlanModal.vmNetIndex = index;
      },

      startVm(names) {
        this.confirmVmAction('start', names, (vms) => ({
          title: `Start the ${pluralWord(vms.length, 'VM')}`,
          message: `This will start ${namedList(vms, 'VM')}.`,
          confirmText: 'Start',
          type: 'is-success',
        }));
      },

      pauseVm(names) {
        this.confirmVmAction('pause', names, (vms) => ({
          title: `Pause the ${pluralWord(vms.length, 'VM')}`,
          message: `This will pause ${namedList(vms, 'VM')}.`,
          confirmText: 'Pause',
          type: 'is-success',
        }));
      },

      resetVmState(names) {
        this.confirmVmAction('resetDisk', names, (vms) => ({
          title: `Reset the ${pluralWord(vms.length, "VM's Disk", "VMs' Disks")}`,
          message: `This will reset the disk state of ${namedList(vms, 'VM')}.`,
          confirmText: 'Reset',
          type: 'is-success',
        }));
      },

      restartVm(names) {
        this.confirmVmAction('restart', names, (vms) => ({
          title: `Restart the ${pluralWord(vms.length, 'VM')}`,
          message: `This will restart ${namedList(vms, 'VM')}.`,
          confirmText: 'Restart',
          type: 'is-success',
        }));
      },

      shutdownVm(names) {
        this.confirmVmAction('shutdown', names, (vms) => ({
          title: `Shut Down the ${pluralWord(vms.length, 'VM')}`,
          message: `This will shut down ${namedList(vms, 'VM')}.`,
          confirmText: 'Shut Down',
          type: 'is-danger',
        }));
      },

      redeploy(names) {
        const vmList = this.actionableVMs('redeploy', names);
        if (vmList.length === 0) return;

        if (roleAllowed('disks', 'list')) {
          this.updateDisks();
        } else {
          this.disks = null;
        }
        for (const name of vmList) {
          const vm = findVm(this.experiment, name);
          this.redeployModal.vm.push({
            name: vm.name,
            cpus: vm.cpus,
            ram: vm.ram,
            disk: vm.disk,
            // the server keeps the VM's disk unless another one is picked
            currentDisk: vm.disk,
            inject: false,
          });
        }

        this.redeployModal.active = true;
      },

      redeployVm(vms) {
        let body = '';
        let name = '';
        const exp = this.$route.params.id;

        vms.forEach((vm) => {
          body = {
            cpus: parseInt(vm.cpus),
            ram: parseInt(vm.ram),
          };
          // a snapshot VM's disk is the image under phēnix's own snapshot
          // overlay; sending it back would replace the overlay with the image
          if (vm.disk !== vm.currentDisk) {
            body.disk = vm.disk;
          }
          name = vm.name;

          // the select stores the string 'true' or 'false'
          if (vm.inject === true || vm.inject === 'true') {
            body['injects'] = true;
          }

          this.redeployModal.actionsQueue.push({
            name: vm.name,
            exp,
            body: body,
          });
        });

        vmsApi
          .redeployVm(exp, name, body)
          .catch((err) => useErrorNotification(err));
      },

      changeVlan(index, vlan, from, name) {
        // the select's disconnect option is the string '0'
        const disconnect = vlan === '0';
        this.$buefy.dialog.confirm({
          title: disconnect
            ? 'Disconnect a VM Network Interface'
            : 'Change the VLAN',
          message: disconnect
            ? `This will disconnect the ${index} interface for the ${name} VM.`
            : `This will change the VLAN from ${from.toLowerCase()} to ` +
              `${vlan.alias.toLowerCase()} (${vlan.vlan}) for the ${name} VM.`,
          cancelText: 'Cancel',
          confirmText: disconnect ? 'Disconnect' : 'Change',
          type: 'is-warning',
          hasIcon: true,
          onConfirm: async () => {
            const key = vlanPendingKey(index);
            this.setVmActionPending(key, [name], true);
            try {
              const resp = await vmsApi.updateVm(this.$route.params.id, name, {
                interface: { index, vlan: disconnect ? '' : vlan.alias },
              });
              replaceVm(this.experiment, resp.data);
              this.showOpenVm(resp.data);
            } catch (err) {
              useErrorNotification(err);
            } finally {
              this.setVmActionPending(key, [name], false);
            }
          },
        });

        this.vlanModal.active = false;
      },

      insertOpticalDisc() {
        const isoPath = this.opticalDiscModal.disc;
        if (!isoPath) return;

        vmsApi
          .insertVmDisc(
            this.$route.params.id,
            this.opticalDiscModal.vmName,
            isoPath,
          )
          .catch((err) => useErrorNotification(err));
        this.resetOpticalDiscModal();
      },

      ejectOpticalDisc() {
        vmsApi
          .ejectVmDisc(this.$route.params.id, this.opticalDiscModal.vmName)
          .catch((err) => useErrorNotification(err));
        this.resetOpticalDiscModal();
      },

      resetExpModal() {
        this.expModal = {
          active: false,
          fullName: '',
          vm: {},
          snapshots: false,
          forwards: [],
          backingChain: null,
          disk: null,
          cdRomDisk: null,
          filesDir: '',
        };
        this.showModifyStateBar = false;
      },

      resetRedeployModal() {
        this.redeployModal = {
          active: false,
          vm: [],
          actionsQueue: [],
        };
      },

      resetDiskImageModal() {
        this.diskImageModal = {
          active: false,
          vm: [],
        };
      },

      resetMemorySnapshotModal() {
        this.memorySnapshotModal = {
          active: false,
          vm: [],
        };
      },

      // opens the trigger dialog, with app picked when given
      getApps(app) {
        this.appsModal.triggerable = this.experiment.apps.filter(
          (a) => !DEFAULT_APPS.includes(a),
        );
        this.appsModal.apps = this.appsModal.triggerable.includes(app)
          ? [app]
          : [];
        this.appsModal.active = true;
      },

      // The apps running now, for their tiles; the websocket keeps them
      // current after. Tiles say nothing of a role that can't read them.
      async loadAppStates() {
        const name = this.$route.params.id;
        if (!roleAllowed('experiments/apps', 'get', name)) return;
        try {
          const resp = await experimentsApi.getExperimentApps(name);
          const running = Object.entries(resp.data ?? {})
            .filter(([app, on]) => on === true && !this.appStates[app])
            .map(([app]) => [app, 'running']);
          this.appStates = {
            ...Object.fromEntries(running),
            ...this.appStates,
          };
        } catch {
          // the tiles fall back to saying which apps are built in
        }
      },

      startApps(apps) {
        this.triggeredApps.push(...apps);

        experimentsApi
          .triggerApps(this.$route.params.id, apps.join())
          .catch((err) => {
            useErrorNotification(err);
            this.triggeredApps = this.triggeredApps.filter(
              (a) => !apps.includes(a),
            );
            this.isWaiting = false;
          });

        this.resetAppsModal();
      },

      resetAppsModal() {
        this.appsModal.apps = [];
        this.appsModal.active = false;
      },

      resetOpticalDiscModal() {
        this.opticalDiscModal = emptyOpticalDiscModal();
      },

      // Opens the CD-ROM picker for a VM and lists the ISO images it can
      // insert, saying so when there are none or they cannot be listed.
      async showChangeDisc(vm) {
        this.opticalDiscModal = {
          ...emptyOpticalDiscModal(),
          active: true,
          vmName: vm.name,
          current: vm.cdRom || '',
          disc: vm.cdRom || null,
        };
        // the reactive copy, to tell if the modal was closed or reopened
        const modal = this.opticalDiscModal;

        if (!roleAllowed('disks', 'list')) {
          modal.error =
            'Your role cannot list disk images, so it cannot pick an ISO ' +
            'image to insert.';
          return;
        }

        modal.loading = true;
        let isos = [];
        let error = null;
        try {
          const resp = await disksApi.listDisks({ diskType: 'ISO' });
          isos = isoDisks(resp.data?.disks);
        } catch (err) {
          error = 'Could not list the ISO images.';
          useErrorNotification(err);
        }

        if (this.opticalDiscModal !== modal) return;
        modal.isos = isos;
        modal.error = error;
        modal.loading = false;
      },

      validate(modalVMQueue) {
        var regexp = /^[a-zA-Z0-9-_]+$/;
        for (let i = 0; i < modalVMQueue.vm.length; i++) {
          if (!regexp.test(modalVMQueue.vm[i].filename)) {
            modalVMQueue.vm[i].nameErrType = 'is-danger';
            modalVMQueue.vm[i].nameErrMsg =
              'image names can only contain alphanumeric, dash, and underscore; we will add the file extension';
            return false;
          }

          modalVMQueue.vm[i].nameErrType = '';
          modalVMQueue.vm[i].nameErrMsg = '';
        }

        return true;
      },

      // runs a toolbar action on the selected VMs, then clears the selection
      actOnSelection(action) {
        this[SELECTION_HANDLERS[action]](this.vmSelectedArray);
        this.vmSelectedArray = [];
        this.showModifyStateBar = false;
      },

      // the named VMs the action's rules for one VM allow, saying which were
      // skipped and why
      actionableVMs(action, names) {
        const { allowed, skipped } = partitionVMsForAction(
          action,
          names,
          this.experiment.vms,
        );
        if (skipped.length > 0) this.alertSkipped(skipped);
        return allowed;
      },

      // says which VMs an action skipped, and why
      alertSkipped(skipped) {
        this.$buefy.dialog.alert({
          title: 'No Action',
          message: escapeHTML(skippedVMsText(skipped)),
          confirmText: 'OK',
        });
      },

      // Asks once to take a power action on the named VMs its rules allow,
      // saying in the same dialog which it skips and why, then takes it in
      // one background job: `ask(vms)` words the question for the VMs it
      // acts on.
      async confirmVmAction(action, names, ask) {
        const { allowed, skipped } = partitionVMsForAction(
          action,
          names,
          this.experiment.vms,
        );
        if (allowed.length === 0) {
          this.alertSkipped(skipped);
          return;
        }

        const confirmed = await confirmOperation(this.$buefy, {
          ...ask(allowed),
          skipped,
          noun: 'VM',
        });
        if (confirmed) await this.runVmJob(action, allowed);
      },

      // marks an action running, or no longer, for each named VM
      setVmActionPending(action, names, pending) {
        const next = { ...this.vmActionPending };
        for (const name of names) {
          const rest = (next[name] ?? []).filter((a) => a !== action);
          if (pending) next[name] = [...rest, action];
          else if (rest.length) next[name] = rest;
          else delete next[name];
        }
        this.vmActionPending = next;
      },

      // shows the job's copy of a VM in its open details card, keeping the
      // annotations, which a job's copy leaves out
      showOpenVm(vm) {
        if (!this.expModal.active || this.expModal.vm.name != vm.name) return;
        const open = this.expModal.vm;
        this.expModal.vm = {
          ...open,
          ...vm,
          annotations: vm.annotations ?? open.annotations,
        };
      },

      // takes a power action on the named VMs its rules allow without asking,
      // saying why when they allow none
      runVmAction(action, names) {
        const { allowed, skipped } = partitionVMsForAction(
          action,
          names,
          this.experiment.vms,
        );
        if (allowed.length === 0) {
          this.alertSkipped(skipped);
          return;
        }
        return this.runVmJob(action, allowed);
      },

      // Takes a power action in a background job, spinning the VMs' buttons,
      // then shows each VM as the job left it and offers to retry failures.
      async runVmJob(action, vms) {
        const jobs = useJobStore();
        this.setVmActionPending(action, vms, true);
        try {
          const job = await vmsApi.startVmActionsJob(
            this.$route.params.id,
            action,
            vms,
          );
          const done = await jobs.whenDone(job.id);
          const items = outcomeFromJob(done);

          const succeeded = (done?.items ?? []).filter(
            (item) => item.status === 'succeeded',
          );
          if (action === 'kill') {
            const killed = new Set(succeeded.map((item) => item.name));
            this.experiment.vms = this.experiment.vms.filter(
              (vm) => !killed.has(vm.name),
            );
            // a killed VM has no details left to show
            if (this.expModal.active && killed.has(this.expModal.vm.name)) {
              this.resetExpModal();
            }
          } else {
            for (const item of succeeded) {
              if (!item.result) continue;
              replaceVm(this.experiment, item.result);
              this.showOpenVm(item.result);
            }
          }

          notifyOutcome(this.$buefy, {
            verbPast: VM_ACTION_DONE[action],
            noun: 'VM',
            items,
            onRetry: (retry) => this.runVmJob(action, retry),
          });
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.setVmActionPending(action, vms, false);
        }
      },

      // the search of a tab's searchKey, whether its tab is open or not
      filterOf(key) {
        return searchKey(this.activeTab) === key
          ? this.search.filter
          : (this.tabSearches[key]?.filter ?? '');
      },

      // the tabs this role sees, which a link may open
      offeredTabs() {
        const id = this.$route.params.id;
        return [
          TAB.VMS,
          TAB.VNC,
          ...(roleAllowed('experiments/files', 'list', id) ? [TAB.FILES] : []),
          ...(roleAllowed('experiments/netflow', 'get', id)
            ? [TAB.NETFLOW]
            : []),
        ];
      },

      // keeps the URL naming the open tab and its search, as WebShark does
      syncQuery() {
        if (this.unmounting || this.$route.name !== 'experiment') return;
        const query = tabQuery(
          this.$route.query,
          this.activeTab,
          this.search.filter,
        );
        // the query watcher leaves alone the URL the page set itself
        this.syncedQuery = query;
        if (!sameQuery(query, this.$route.query))
          this.$router.replace({ query });
      },

      addSearchHistory() {
        this.searchHistory = addSearch(this.searchHistory, this.search.filter);
      },

      showMountDialog(vm) {
        this.$buefy.modal.open({
          component: VMMountBrowserModal,
          trapFocus: true,
          hasModalCard: true,
          canCancel: [],
          props: { targetVm: vm, targetExp: this.$route.params.id },
        });
      },

      showPortForwardDialog(vm) {
        this.portForwardModal.vmName = vm;
        this.portForwardModal.active = true;
      },

      resetPortForwardModal() {
        this.portForwardModal = {
          active: false,
          vmName: null,
          srcPort: null,
          dstHost: '127.0.0.1',
          dstPort: null,
        };
      },

      async createPortForward() {
        const exp = this.$route.params.id;
        const vmName = this.portForwardModal.vmName;
        const params = {
          src: this.portForwardModal.srcPort,
          host: this.portForwardModal.dstHost,
          dst: this.portForwardModal.dstPort,
        };

        try {
          await vmsApi.createVmForward(exp, vmName, params);

          this.$buefy.toast.open({
            message: `Port forward ${params.src} → ${params.host}:${params.dst} created for the ${this.portForwardModal.vmName} VM.`,
            type: 'is-success',
          });

          if (
            !this.roleAllowed('vms/forwards', 'list', this.expModal.fullName)
          ) {
            return;
          }

          let resp = await vmsApi.listVmForwards(exp, vmName);

          this.showForwards(resp.data.listeners);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.resetPortForwardModal();
        }
      },

      async deletePortForward(vm, forward) {
        const exp = this.$route.params.id;
        const params = { host: forward.dstHost, dst: forward.dstPort };

        try {
          await vmsApi.deleteVmForward(exp, vm, params);

          let resp = await vmsApi.listVmForwards(exp, vm);

          this.showForwards(resp.data.listeners);
        } catch (err) {
          useErrorNotification(err);
        }
      },

      // the open VM's port forwards, each marked deletable when the user
      // owns it
      showForwards(listeners) {
        const user = usePhenixStore().username;
        this.expModal.forwards = (listeners ?? []).map((forward) => ({
          ...forward,
          canDelete: forward.owner === user,
        }));
      },

      // Tells the server to stop sending the VMs; otherwise it keeps
      // screenshotting them, and each screenshot holds up every other
      // minimega command.
      unsubscribeVms() {
        sendWsMsg({
          resource: {
            type: 'experiment/vms',
            name: this.experiment?.name ?? '',
            action: 'unsubscribe',
          },
        });
      },

      // a hidden browser tab needs no screenshots; on return the VMs are
      // listed again, which brings fresh ones
      onVisibilityChange() {
        if (!this.experiment.name) return;
        if (document.hidden) {
          this.unsubscribeVms();
        } else {
          this.updateTable();
        }
      },
    },

    watch: {
      activeTab(newVal, oldVal) {
        // each tab keeps its own search and history while another is open
        const [from, to] = [searchKey(oldVal), searchKey(newVal)];
        if (from !== to) {
          this.searchVMs.cancel();
          this.tabSearches[from] = {
            filter: this.search.filter,
            history: this.searchHistory,
          };
          const kept = this.tabSearches[to];
          this.search.filter = kept?.filter ?? '';
          this.searchHistory = kept?.history ?? [];
        }
        this.syncQuery();

        if (newVal == TAB.VMS || newVal == TAB.VNC) {
          if (this.experiment.name) this.updateTable();
        } else if (newVal == TAB.NETFLOW) {
          this.$nextTick(() => this.$refs.netflowPanel?.fit());
        } else {
          this.$refs.filesTab?.reload();
        }
      },

      '$route.query.vm'(name) {
        if (!name) this.linkedVm = '';
        else if (this.experiment.name) this.openLinkedVm();
      },

      // a closed card takes its link out of the URL, so a reload does not
      // open it again
      'expModal.active'(open) {
        const query = this.$route.query;
        if (open || this.unmounting || this.$route.name !== 'experiment')
          return;
        if (!('vm' in query)) return;
        this.syncedQuery = withoutVm(query);
        this.$router.replace({ query: this.syncedQuery });
      },

      // a link to this page with another tab or search
      '$route.query'(query) {
        if (
          this.$route.name !== 'experiment' ||
          sameQuery(query, this.syncedQuery)
        ) {
          return;
        }
        const tab = tabFromQuery(query, this.offeredTabs());
        const search = searchFromQuery(query);
        if (tab === this.activeTab) {
          if (search !== this.search.filter) this.searchVMs(search);
          return;
        }
        // the activeTab watcher opens the tab with the link's search
        const key = searchKey(tab);
        this.searchVMs.cancel();
        if (key === searchKey(this.activeTab)) this.search.filter = search;
        else
          this.tabSearches[key] = { ...this.tabSearches[key], filter: search };
        this.activeTab = tab;
      },

      vncWidth(w) {
        this.setVncScreenshotRes(w);
      },
    },

    data() {
      return {
        // whether VMs still without screenshots have waited long enough
        screenshotsDue: false,
        screenshotsTimer: null,
        search: {
          filter: searchFromQuery(this.$route.query),
        },
        // whether the websocket has sent the VM table yet
        vmsLoaded: false,
        // whether minimega failed to answer for the VMs last listed
        runtimeUnknown: false,
        // whether the page has asked the websocket for the VM table
        vmsRequested: false,
        // the VM of the link whose details the page opened
        linkedVm: '',
        expModal: {
          active: false,
          vm: {},
          fullName: '',
          snapshots: false,
          forwards: [],
          backingChain: null,
          // the VM's disk and CD-ROM ISO image in the disk list, and the
          // minimega files directory
          disk: null,
          cdRomDisk: null,
          filesDir: '',
        },
        portForwardModal: {
          active: false,
          vmName: null,
          srcPort: null,
          dstHost: '127.0.0.1',
          dstPort: null,
        },
        vlanModal: {
          active: false,
          vmName: null,
          vmFromNet: null,
          vmNetIndex: null,
        },
        redeployModal: {
          active: false,
          vm: [],
          actionsQueue: [],
        },
        diskImageModal: {
          active: false,
          vm: [],
          /*  vm is structured as so:
         name:  null, filename: null, dateTime: null, 
         nameErrType: null, nameErrMsg: null
        */
        },
        memorySnapshotModal: {
          active: false,
          vm: [],
          /*  vm is structured as so:
         name:  null, filename: null, dateTime: null, 
         nameErrType: null, nameErrMsg: null
        */
        },
        appsModal: {
          active: false,
          triggerable: [],
          apps: [],
        },
        opticalDiscModal: emptyOpticalDiscModal(),
        // apps this page triggered that have not reported back
        triggeredApps: [],
        // what each app was last heard doing, for its tile
        appStates: {},
        // the labels on the experiment's VMs, as the last VM list gave them,
        // and those the VM table is filtered by
        vmLabels: [],
        labelFilter: [],
        // the same for the cluster hosts running the VMs
        vmHosts: [],
        hostFilter: [],
        // how many of the experiment's VMs are running, null until listed
        runningVms: null,
        experiment: {},
        // the Files tab's category picker, and the categories it offers
        fileCategory: null,
        fileCategories: [],
        // the redeploy modal's disk list, null until it has loaded
        disks: null,
        vlan: null,
        // shows the loading overlay while the page waits on the server
        isWaiting: false,
        // the actions running in the background for each VM, by its name,
        // whose buttons spin meanwhile
        vmActionPending: {},
        showModifyStateBar: false,
        vmSelectedArray: [],
        searchHistory: [],
        // the search and its history of each tab not open, by searchKey
        tabSearches: {},
        columnToggles: [
          {
            key: 'screenshot',
            label: 'Screenshots',
            storageKey: 'showScreenshotsColumn',
          },
          {
            key: 'host',
            label: 'Host',
            storageKey: 'showHostColumn',
          },
          {
            key: 'ipv4',
            label: 'IP',
            storageKey: 'showIPv4Column',
          },
          {
            key: 'network',
            label: 'Network',
            storageKey: 'showNetworkColumn',
          },
          {
            key: 'taps',
            label: 'Taps',
            storageKey: 'showTapsColumn',
          },
          {
            key: 'uptime',
            label: 'Uptime',
            storageKey: 'showUptimeColumn',
          },
          {
            key: 'labels',
            label: 'Labels',
            storageKey: 'showLabelsColumn',
          },
          {
            key: 'actions',
            label: 'Actions',
            storageKey: 'showActionsColumn',
          },
        ],
        columnVisibility: {
          screenshot: true,
          host: true,
          ipv4: true,
          network: true,
          taps: true,
          uptime: true,
          labels: true,
          actions: true,
        },
        activeTab: tabFromQuery(this.$route.query, this.offeredTabs()),
        vncZoom: 4,
      };
    },
  };
</script>

<style scoped>
  /* the experiment's facts on the left, its apps on the right */
  .experiment-header {
    display: flex;
    flex-wrap: wrap;
    align-items: stretch;
    gap: 1rem;
    margin-bottom: 1.5rem;
  }

  .experiment-header-summary {
    flex: 1 1 32rem;
  }

  .experiment-header-apps {
    flex: 0 1 36rem;
  }

  /* Room for the tabs drawn beside the toolbar's last row: the VM filters
     wrap onto a row of their own above the search box when it runs out. */
  .exp-toolbar {
    padding-left: 25rem;
  }

  .exp-toolbar :deep(.field.is-grouped.is-grouped-right) {
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
  }

  .exp-toolbar :deep(.field.is-grouped.is-grouped-right > *) {
    margin-right: 0;
  }

  /* the search and its buttons wrap below rather than run left over the tabs */
  .exp-toolbar :deep(.exp-toolbar-search),
  .exp-toolbar :deep(.exp-toolbar-search > .field-body > .field) {
    flex-shrink: 1;
    min-width: 0;
  }

  .exp-toolbar :deep(.exp-toolbar-search > .field-body > .field) {
    flex-wrap: wrap;
    justify-content: flex-end;
    row-gap: 0.5rem;
  }

  .vm-table-filters {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  :deep(.tabs ul) {
    margin-left: 0px !important;
  }

  :deep(.b-tabs .tab-content) {
    padding: 1rem 0 0 0;
  }
</style>
