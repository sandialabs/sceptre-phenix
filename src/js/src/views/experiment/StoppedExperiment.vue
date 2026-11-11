<template>
  <div class="content">
    <b-modal v-model="expModal.active" has-modal-card width="68rem">
      <VmDetailsCard
        :vm="expModalVm"
        :full-name="`${experiment.name}/${expModalVm.name}`"
        :experiment="experiment.name"
        :disk="findDisk(disks, expModalVm.disk)"
        :backing-chain="disks ? backingChain(disks, expModalVm.disk) : null"
        :files-dir="filesDir(disks)"
        stopped
        :hosts="hosts"
        :disk-choices="disks"
        :settings-pending="settingPending[expModalVm.name] ?? {}"
        :resetting="resetPending.includes(expModalVm.name)"
        @change-setting="
          (field, value) => changeSetting(expModalVm, field, value)
        "
        @reset-settings="resetSettings([expModalVm.name])"
        @edit-labels="showTagsModal(expModalVm)"
        @save-annotations="saveAnnotations" />
    </b-modal>
    <ExperimentHeader
      :name="$route.params.id"
      :experiment="experiment"
      :hosts="scheduleHosts"
      :hosts-failed="scheduleFailed"
      :vm-count="allVmCount"
      stopped />
    <b-field position="is-right" grouped class="exp-toolbar">
      <StoppedSelectionToolbar
        v-if="selectedRows.length"
        :selected="selectedRows"
        :experiment="experiment.name"
        :pending="bulkPending"
        :can-reset="selectionCanReset"
        :disabled="starting"
        @act="actOnSelection" />
      <!-- the VM table's filters and paging, left of the search box -->
      <div v-if="vmFiltersShown" class="control vm-table-filters">
        <VmPickFilter
          kind="host"
          title="Hosts"
          icon="server"
          note="VMs assigned to any of the checked hosts"
          hint="show only the VMs assigned to any of these hosts"
          empty-reason="no VM is assigned to a host"
          :options="hostChoices"
          :picked="hostFilter"
          @update:picked="filterByHosts" />
        <VmLabelFilter
          :options="labelChoices"
          :picked="labelFilter"
          @update:picked="filterByLabels" />
        <b-switch
          v-if="paginationNeeded"
          v-model="table.isPaginated"
          @update:modelValue="updateExperiment()"
          type="is-light"
          >Paginate</b-switch
        >
      </div>

      <b-field position="is-right" class="exp-toolbar-search">
        <b-field v-if="activeTab == TAB.FILES">
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
            v-model="searchName"
            :placeholder="searchPlaceholder"
            icon="search"
            :data="searchOptions"
            @typing="searchVMs"
            @select="(option) => searchVMs(option)" />
          <p v-if="searchName || fileCategory" class="control">
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
          <b-tooltip label="start the experiment" type="is-light" :delay="500">
            <b-button
              aria-label="Start experiment"
              v-if="roleAllowed('experiments/start', 'update', experiment.name)"
              class="button is-success"
              icon-right="play"
              :loading="starting"
              @click="start"></b-button>
          </b-tooltip>
        </b-field>
        <b-field
          v-if="roleAllowed('experiments/schedule', 'create', experiment.name)">
          <ScheduleMenu
            :schedulers="schedulers"
            :pinned="pinnedCount"
            :total="allVmCount"
            :scheduling="scheduling"
            :disabled="starting"
            @schedule="confirmSchedule" />
        </b-field>
        <b-field v-if="roleAllowed('experiments', 'patch', experiment.name)">
          <VlanAliasMenu
            :experiment="$route.params.id"
            :vlans="experiment.vlans ?? []"
            :min="experiment.vlan_min ?? 0"
            :max="experiment.vlan_max ?? 0"
            :disabled="starting"
            @saved="(vlans) => (experiment.vlans = vlans)" />
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
          <StoppedVmTable
            v-model:selected="selectedRows"
            v-model:check-all="checkAll"
            :vms="experiment.vms"
            :experiment="experiment.name"
            :table="table"
            :columns="columnVisibility"
            :empty-text="vmsEmptyText"
            :hosts="hosts"
            :disks="disks"
            :pending="settingPending"
            :reset-pending="resetPending"
            @page-change="onPageChange"
            @sort="onSort"
            @details="showVmDetails"
            @edit-labels="showTagsModal"
            @change="changeSetting"
            @reset="(vm) => resetSettings([vm.name])" />
        </b-tab-item>
        <b-tab-item
          :value="TAB.FILES"
          label="Files"
          icon="file-alt"
          :visible="canListFiles">
          <ExperimentFilesTab
            ref="filesTab"
            paginate-key="stopped-files"
            :filter="filesFilter"
            :category="fileCategory"
            @categories="(list) => (fileCategories = list)"
            @found="addSearchHistory" />
        </b-tab-item>
      </b-tabs>
    </div>
  </div>
</template>

<script>
  import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
  import ExperimentHeader from '@/components/experiment/ExperimentHeader.vue';
  import ScheduleMenu from '@/components/experiment/ScheduleMenu.vue';
  import StoppedSelectionToolbar from '@/components/experiment/StoppedSelectionToolbar.vue';
  import StoppedVmTable from '@/components/experiment/StoppedVmTable.vue';
  import VlanAliasMenu from '@/components/experiment/VlanAliasMenu.vue';
  import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
  import VmLabelFilter from '@/components/experiment/VmLabelFilter.vue';
  import VmPickFilter from '@/components/experiment/VmPickFilter.vue';
  import {
    hostOptions,
    labelOptions,
    labelText,
  } from '@/components/experiment/experimentHeader.js';
  import {
    TAB,
    sameQuery,
    searchFromQuery,
    tabFromQuery,
    tabQuery,
    withoutVm,
  } from '@/components/experiment/experimentTabs.js';
  import { useColumnVisibility } from '@/components/experiment/useColumnVisibility.js';
  import {
    BUILTIN_SCHEDULERS,
    SETTING_FIELDS,
    batchOutcome,
    createSaveClock,
    hasDefaults,
    resetPlan,
    scheduleMap,
    scheduledHosts,
  } from '@/components/experiment/vmSettings.js';
  import { debounce } from 'lodash-es';
  import { addSearch } from '@/utils/searchHistory.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import VMLabelsModal from '@/components/VMLabelsModal.vue';
  import { usePhenixStore } from '@/store';
  import { roleAllowed } from '@/utils/rbac.js';
  import * as disksApi from '@/api/disks.js';
  import * as experimentsApi from '@/api/experiments.js';
  import * as vmsApi from '@/api/vms.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import {
    confirmStart,
    notifyScheduleWarnings,
    notifyStartResult,
  } from '@/utils/experimentActions.js';
  import { startResult } from '@/utils/startOptions.js';
  import { header } from '@/utils/freshness.js';
  import { notifyOutcome, outcomeFromSettled } from '@/utils/operations.js';
  import { backingChain, filesDir, findDisk } from '@/utils/diskChain.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import {
    schedulableHostNames,
    stoppedExperimentKey,
  } from '@/utils/pageData.js';
  import { useTable } from '@/utils/useTable.js';

  // the columns the user can hide, each remembered under its own key
  const COLUMN_TOGGLES = [
    { key: 'host', label: 'Host', storageKey: 'showStoppedHostColumn' },
    { key: 'ipv4', label: 'IP', storageKey: 'showStoppedIPv4Column' },
    { key: 'cpus', label: 'CPUs', storageKey: 'showStoppedCpusColumn' },
    { key: 'ram', label: 'Memory', storageKey: 'showStoppedMemoryColumn' },
    { key: 'disk', label: 'Disk', storageKey: 'showStoppedDiskColumn' },
    {
      key: 'partition',
      label: 'Partition',
      storageKey: 'showStoppedPartitionColumn',
    },
    { key: 'labels', label: 'Labels', storageKey: 'showStoppedLabelsColumn' },
    { key: 'boot', label: 'Boot', storageKey: 'showStoppedBootColumn' },
    {
      key: 'persistence',
      label: 'Persistence',
      storageKey: 'showStoppedPersistenceColumn',
    },
  ];

  // each selection action: the change it sends to every VM, and what its
  // outcome says was done
  const BULK_ACTIONS = {
    boot: { update: { dnb: false }, verbPast: 'Set boot on' },
    dnb: { update: { dnb: true }, verbPast: 'Set do not boot on' },
    persistent: {
      update: { snapshot: false },
      verbPast: 'Set persistent disks on',
    },
    nonPersistent: {
      update: { snapshot: true },
      verbPast: 'Set non-persistent disks on',
    },
    reset: { verbPast: 'Reset the settings of' },
  };

  // the server's X-Phenix-Seq on an answer, or undefined
  const seqOf = (resp) => {
    const seq = Number(header(resp?.headers, 'x-phenix-seq'));
    return Number.isFinite(seq) && seq > 0 ? seq : undefined;
  };

  export default {
    emits: ['running'],
    components: {
      ExperimentFilesTab,
      ExperimentHeader,
      ScheduleMenu,
      StoppedSelectionToolbar,
      StoppedVmTable,
      VlanAliasMenu,
      VmDetailsCard,
      VmLabelFilter,
      VmPickFilter,
    },
    setup() {
      const { table } = useTable({
        name: 'stopped-vms',
        fields: { total: 0, sortColumn: 'name' },
      });
      return {
        table,
        ...useColumnVisibility(COLUMN_TOGGLES, () => usePhenixStore().username),
        roleAllowed,
        TAB,
        backingChain,
        filesDir,
        findDisk,
      };
    },
    beforeUnmount() {
      this.unmounting = true;
      this.searchVMs.cancel();
      removeWsHandler(this.handleWs);
      this.loader.stop();
    },

    mounted() {
      // a link to the Files tab opens it without the activeTab watcher
      if (this.activeTab === TAB.FILES) this.$refs.filesTab?.reload();
    },

    async created() {
      // the URL the page opened on, which already names its tab and search
      this.syncedQuery = this.$route.query;
      this.searchVMs = debounce((term) => {
        this.searchName = term ?? '';
        this.syncQuery();
        if (this.activeTab == TAB.VMS) {
          this.updateExperiment();
          return;
        }

        this.$refs.filesTab?.reload();
      }, 250);
      addWsHandler(this.handleWs);
      this.saves = createSaveClock();
      // when each experiment read was sent, by the data it answered with
      this.readsSent = new WeakMap();
      this.loader = createPageLoader({
        // only the default view is cached, as its request is the same each
        // visit
        key: () =>
          this.vmFilter ||
          this.labelFilter.length ||
          this.hostFilter.length ||
          this.table.isPaginated ||
          !this.defaultSort
            ? null
            : stoppedExperimentKey(this.$route.params.id),
        fetch: async (signal) => {
          const params = {
            show_dnb: true,
            filter: this.vmFilter,
            sortCol: this.table.sortColumn,
            sortDir: this.table.defaultSortDirection,
          };
          if (this.labelFilter.length) {
            params.label = this.labelFilter.map(labelText);
          }
          if (this.hostFilter.length) params.host = [...this.hostFilter];

          if (this.table.isPaginated) {
            params.pageNum = this.table.currentPage;
            params.perPage = this.table.perPage;
          }

          // repeated label and host parameters, as the server reads them
          const sentAt = this.saves.sent();
          const resp = await experimentsApi.getExperiment(
            this.$route.params.id,
            { params, signal, paramsSerializer: { indexes: null } },
          );
          if (resp.data && typeof resp.data === 'object') {
            this.readsSent.set(resp.data, sentAt);
          }
          return resp.data;
        },
        apply: (experiment) => {
          if (experiment.running) {
            // started since the page was opened; Base shows the running view
            this.$emit('running', true);
            return;
          }
          // a read without a number (a cached one) came before every save
          const sentAt = this.readsSent.get(experiment) ?? 0;
          const rows = new Map(
            (this.experiment.vms ?? []).map((vm) => [vm.name, vm]),
          );
          this.experiment = experiment.vms
            ? {
                ...experiment,
                vms: experiment.vms.map((vm) =>
                  this.saves.merge(vm, rows.get(vm.name), {
                    sentAt,
                    keep: this.pendingFields(vm.name),
                  }),
                ),
              }
            : experiment;
          // vms holds only the current page; vm_count is the total before paging
          this.table.total = experiment.vm_count ?? 0;
          if (this.vmsUnfiltered) this.vmTotal = experiment.vm_count ?? 0;
          if (this.scheduleFailed) this.loadSchedule();

          // Only add successful searches to the search history
          if (this.activeTab == TAB.VMS && this.table.total > 0) {
            this.addSearchHistory();
          }
          this.openLinkedVm();
        },
        refresh: () => {
          this.updateLists();
          return this.loader.load();
        },
      });
      this.loader.start();
      this.updateLists();
    },

    computed: {
      // the header checkbox: whether every VM on this page that can be
      // selected is, and selecting all of them or none
      checkAll: {
        get() {
          const names = this.selectableVmNames;
          return (
            names.length > 0 &&
            names.every((name) => this.selectedRows.includes(name))
          );
        },
        set(on) {
          this.selectedRows = on ? [...this.selectableVmNames] : [];
        },
      },

      selectableVmNames() {
        return (this.experiment.vms ?? [])
          .filter((vm) => !vm.external)
          .map((vm) => vm.name);
      },

      // whether a selected VM on this page knows its topology values
      selectionCanReset() {
        return (this.experiment.vms ?? []).some(
          (vm) => this.selectedRows.includes(vm.name) && hasDefaults(vm),
        );
      },

      defaultSort() {
        return (
          this.table.sortColumn === 'name' &&
          this.table.defaultSortDirection === 'asc'
        );
      },

      vmsEmptyText() {
        if (!this.experiment.name) return loadingText('VMs');
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
          return 'No VMs are assigned to any of the checked hosts';
        }
        if (this.vmFilter) return 'No VMs match your search';
        return 'This experiment has no VMs';
      },

      canListFiles() {
        return roleAllowed('experiments/files', 'list', this.$route.params.id);
      },

      // the VM table's search and the Files tab's, kept while the other tab
      // is open
      vmFilter() {
        return this.filterOf(TAB.VMS);
      },

      filesFilter() {
        return this.filterOf(TAB.FILES);
      },

      searchPlaceholder() {
        return this.activeTab == TAB.FILES ? 'Find a File' : 'Find a VM';
      },

      // by all the VMs, so a search or filter leaves the switch as it was
      paginationNeeded() {
        const all = Math.max(this.table.total, this.allVmCount ?? 0);
        return all > this.table.perPage;
      },

      // whether the VM table lists every VM, with no search or filter
      vmsUnfiltered() {
        return (
          !this.vmFilter && !this.labelFilter.length && !this.hostFilter.length
        );
      },

      // how many VMs the experiment has whatever the search and filters, or
      // null while not known
      allVmCount() {
        if (this.vmTotal !== null) return this.vmTotal;
        return this.vmsUnfiltered && this.experiment.name
          ? (this.experiment.vm_count ?? 0)
          : null;
      },

      // the past searches that hold the text typed; they are not the VMs that
      // match, so the list has no "no results" message
      searchOptions() {
        const text = String(this.searchName ?? '')
          .trim()
          .toLowerCase();
        return this.searchHistory
          .filter((past) => past.toLowerCase().includes(text))
          .sort();
      },

      hostChoices() {
        return hostOptions(
          [...scheduledHosts(this.schedule), ...this.vmHosts],
          this.experiment.vms,
          this.hostFilter,
        );
      },

      labelChoices() {
        return labelOptions(
          this.vmLabels,
          this.experiment.vms,
          this.labelFilter,
        );
      },

      // whether the VMs tab shows its filters and Paginate in the toolbar
      vmFiltersShown() {
        return (
          this.activeTab == TAB.VMS &&
          (this.paginationNeeded ||
            this.labelChoices.length > 0 ||
            this.hostChoices.length > 0)
        );
      },

      // the hosts the schedule names, null while it loads, and undefined for
      // a role that may not get it
      scheduleHosts() {
        if (!this.canGetSchedule) return undefined;
        return this.schedule ? scheduledHosts(this.schedule) : null;
      },

      pinnedCount() {
        return this.schedule ? Object.keys(this.schedule).length : null;
      },

      canGetSchedule() {
        return roleAllowed(
          'experiments/schedule',
          'get',
          this.$route.params.id,
        );
      },

      // The VM whose details are open: its table row over the details
      // fetched when it opened, which alone carry the annotations.
      expModalVm() {
        const vm = this.expModal.vm ?? {};
        const row = vm.name
          ? this.experiment.vms?.find((v) => v.name == vm.name)
          : null;
        if (!row || row === vm) return vm;
        return { ...vm, ...row, annotations: vm.annotations };
      },
    },

    methods: {
      // Opens the details of the VM a link names (?vm=web), once: its row, or
      // the VM from the server when the table's page does not hold it.
      async openLinkedVm() {
        const name = this.$route.query.vm;
        if (typeof name !== 'string' || !name || name === this.linkedVm) return;
        this.linkedVm = name;
        let vm = this.experiment.vms?.find((row) => row.name == name);
        const exp = this.experiment.name;
        if (!vm && roleAllowed('vms', 'get', `${exp}/${name}`)) {
          try {
            vm = (await vmsApi.getVm(exp, name)).data;
          } catch (err) {
            useErrorNotification(err);
            return;
          }
        }
        if (vm && !this.unmounting && this.linkedVm == name) {
          this.showVmDetails(vm);
        }
      },

      // Opens a VM's details card, then fetches the VM's own details, which
      // carry its annotations.
      async showVmDetails(vm) {
        this.expModal = { active: true, vm };
        const exp = this.experiment.name;
        if (!roleAllowed('vms', 'get', `${exp}/${vm.name}`)) return;
        try {
          const details = (await vmsApi.getVm(exp, vm.name)).data;
          if (this.expModal.active && this.expModal.vm.name == vm.name) {
            this.expModal.vm = details;
          }
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

      onPageChange(page) {
        this.table.currentPage = page;
        this.updateExperiment();
      },

      onSort(column, order) {
        this.table.sortColumn = column;
        this.table.defaultSortDirection = order;
        this.updateExperiment();
      },

      // lists the VMs assigned to any of these hosts, from the first page
      filterByHosts(hosts) {
        this.hostFilter = hosts;
        this.table.currentPage = 1;
        this.updateExperiment();
      },

      // lists the VMs with any of these labels, from the first page
      filterByLabels(labels) {
        this.labelFilter = labels;
        this.table.currentPage = 1;
        this.updateExperiment();
      },

      handleWs(msg) {
        // experiment resources are named "exp", VM resources "exp/vm"
        const [exp] = (msg.resource?.name ?? '').split('/');
        if (exp !== this.$route.params.id || !this.experiment.vms) {
          return;
        }

        if (this.scheduleFailed) this.loadSchedule();
        // a stopped experiment cares only for schedules and VM updates
        if (
          msg.resource.type == 'experiment' &&
          msg.resource.action == 'schedule'
        ) {
          this.applySchedule(msg.result.schedule);
          this.$buefy.toast.open({
            message: 'The VMs for this experiment have been scheduled.',
            type: 'is-success',
          });
        } else if (
          msg.resource.type == 'experiment/vm' &&
          msg.resource.action == 'update'
        ) {
          this.replaceRow(msg.result, { seq: msg.seq });
        }
      },

      // Puts a VM the server answered with in its row and its open card, and
      // its host in the schedule. It keeps the fields of saves still in
      // flight, other than own (those the answer's own request set), and of
      // saves newer than the answer: a request sent at sentAt, or a websocket
      // update with this seq (see createSaveClock).
      replaceRow(vm, { sentAt, seq, own = [] } = {}) {
        if (!vm?.name) return;
        const keep = this.pendingFields(vm.name).filter(
          (field) => !own.includes(field),
        );
        const order = { sentAt, seq, keep };
        const vms = this.experiment.vms ?? [];
        const i = vms.findIndex((row) => row.name == vm.name);
        let merged = vm;
        if (i >= 0) {
          merged = this.saves.merge(vm, vms[i], order);
          this.experiment.vms = vms.map((row, j) => (j == i ? merged : row));
        }
        if (this.expModal.vm?.name == vm.name) {
          const card =
            i >= 0 ? merged : this.saves.merge(vm, this.expModal.vm, order);
          this.expModal.vm = {
            ...card,
            annotations: vm.annotations ?? this.expModal.vm.annotations,
          };
        }
        if (this.schedule && 'host' in merged) {
          const { [vm.name]: _, ...rest } = this.schedule;
          this.schedule = merged.host
            ? { ...rest, [vm.name]: merged.host }
            : rest;
        }
      },

      // the fields of a VM whose saves wait on the server
      pendingFields(name) {
        return Object.keys(this.settingPending[name] ?? {});
      },

      // reloads the VM table for the current search, filters, sort and page
      async updateExperiment() {
        await this.loader.load();
      },

      // the host and disk choices for editing VMs, the schedule and the
      // schedulers; loaded on open and on refresh rather than with every
      // search, since listing disks is slow
      updateLists() {
        if (roleAllowed('hosts', 'list')) {
          this.updateHosts();
        }
        if (roleAllowed('disks', 'list')) {
          this.updateDisks();
        }
        this.loadSchedule();
        this.loadSchedulers();
        this.loadVmList();
      },

      // Every label and host of the VMs, and how many VMs there are, whatever
      // the search, filters or page; the list's first page of one VM has them.
      async loadVmList() {
        if (!roleAllowed('vms', 'list')) return;
        try {
          const { data } = await vmsApi.listVms(this.$route.params.id, {
            params: { pageNum: 1, perPage: 1 },
          });
          this.vmLabels = data?.labels ?? [];
          this.vmHosts = data?.hosts ?? [];
          if (Number.isFinite(data?.total)) this.vmTotal = data.total;
        } catch {
          // the filters offer what the rows on the page carry
        }
      },

      // shares the Hosts page's cached list, as listing hosts scans the
      // whole cluster
      updateHosts() {
        schedulableHostNames().then(
          (hosts) => {
            this.hosts = hosts;
          },
          (err) => {
            useErrorNotification(err);
          },
        );
      },

      // the images inside the minimega files directory and the ones this
      // experiment names outside it
      updateDisks() {
        disksApi.listDisks({ expName: this.$route.params.id }).then(
          (response) => {
            this.disks = response.data.disks ?? [];
          },
          (err) => {
            useErrorNotification(err);
          },
        );
      },

      // which VMs the experiment's schedule puts on a host; the page shows
      // no count when it cannot get it, and asks again on the next update
      async loadSchedule() {
        if (!this.canGetSchedule) return;
        try {
          const resp = await experimentsApi.getExperimentSchedule(
            this.$route.params.id,
          );
          this.schedule = scheduleMap(resp.data?.schedule);
          this.scheduleFailed = false;
        } catch {
          this.schedule = null;
          this.scheduleFailed = true;
        }
      },

      // the schedulers the server has, or the built-in ones when it does not
      // say
      async loadSchedulers() {
        const name = this.$route.params.id;
        if (!roleAllowed('experiments/schedule', 'create', name)) return;
        try {
          const resp = await experimentsApi.listSchedulers();
          const listed = resp.data?.schedulers;
          if (Array.isArray(listed) && listed.length) this.schedulers = listed;
        } catch {
          this.schedulers = [...BUILTIN_SCHEDULERS];
        }
      },

      assignCategory(value) {
        this.fileCategory = value;
        this.$refs.filesTab?.reload();
      },

      // the tabs this role sees, which a link may open
      offeredTabs() {
        const id = this.$route.params.id;
        return roleAllowed('experiments/files', 'list', id)
          ? [TAB.VMS, TAB.FILES]
          : [TAB.VMS];
      },

      // the search of a tab, whether it is open or not
      filterOf(tab) {
        return this.activeTab === tab
          ? this.searchName
          : (this.tabSearches[tab]?.filter ?? '');
      },

      // keeps the URL naming the open tab and its search, as WebShark does
      syncQuery() {
        if (this.$route.name !== 'experiment') return;
        const query = tabQuery(
          this.$route.query,
          this.activeTab,
          this.searchName,
        );
        // the query watcher leaves alone the URL the page set itself
        this.syncedQuery = query;
        if (!sameQuery(query, this.$route.query))
          this.$router.replace({ query });
      },

      addSearchHistory() {
        this.searchHistory = addSearch(this.searchHistory, this.searchName);
      },

      // Starts the experiment with the options picked once confirmed, its
      // button spinning and the other actions greyed out meanwhile, then
      // shows the running view. A dry run leaves it stopped, so the page
      // stays and tells what the dry run did.
      async start() {
        if (this.starting) return;
        const name = this.$route.params.id;
        const options = await confirmStart(this.$buefy, name);
        if (!options || this.starting) return;
        this.starting = true;
        let result;
        try {
          result = startResult(
            (await experimentsApi.startExperiment(name, options))?.data,
          );
        } catch (err) {
          useErrorNotification(err);
          this.starting = false;
          return;
        }
        notifyStartResult(name, result);
        if (result.dryRun) {
          this.starting = false;
          // the apps of the dry run can change the experiment's VMs
          this.updateExperiment();
          return;
        }
        // Base shows the running view, which loads the experiment afresh
        this.$emit('running', true);
      },

      // Saves one setting of a VM at once: its control spins and shows the
      // value picked until the server answers, then the VM's saved value.
      async changeSetting(vm, field, value) {
        const name = vm.name;
        if (vm.external || field in (this.settingPending[name] ?? {})) return;
        this.settingPending = {
          ...this.settingPending,
          [name]: { ...this.settingPending[name], [field]: value },
        };
        const sentAt = this.saves.sent();
        try {
          const resp = await vmsApi.updateVm(this.$route.params.id, name, {
            [field]: value,
          });
          this.replaceRow(resp.data, { sentAt, own: [field] });
          if (resp.data?.name) this.saves.saved(name, [field], seqOf(resp));
        } catch (err) {
          useErrorNotification(err);
        } finally {
          const rest = { ...this.settingPending[name] };
          delete rest[field];
          const others = { ...this.settingPending };
          delete others[name];
          this.settingPending = Object.keys(rest).length
            ? { ...others, [name]: rest }
            : others;
        }
      },

      // Sets the named VMs' settings back to their topology values, their
      // reset buttons spinning meanwhile, and tells how it went.
      async resetSettings(names) {
        const fresh = names.filter((name) => !this.resetPending.includes(name));
        if (!fresh.length) return;
        this.resetPending = [...this.resetPending, ...fresh];
        try {
          await this.sendBatch('reset', fresh);
        } finally {
          this.resetPending = this.resetPending.filter(
            (name) => !fresh.includes(name),
          );
        }
      },

      // Takes a toolbar action on the selected VMs, its button spinning
      // meanwhile, then clears the selection.
      async actOnSelection(action) {
        if (this.bulkPending || !this.selectedRows.length) return;
        this.bulkPending = action;
        try {
          await this.sendBatch(action, [...this.selectedRows]);
        } finally {
          this.bulkPending = null;
          this.selectedRows = [];
        }
      },

      // Sends one request for an action on these VMs, puts the VMs it
      // changed in their rows, and sums up how it went, offering to retry
      // those that failed. A reset skips the VMs on the page it cannot change.
      async sendBatch(action, names) {
        const { update, verbPast } = BULK_ACTIONS[action];
        const exp = this.$route.params.id;
        const { send, skipped } = update
          ? { send: names, skipped: [] }
          : resetPlan(names, this.experiment.vms);
        const fields = update ? Object.keys(update) : [...SETTING_FIELDS];
        let items = [];
        if (send.length) {
          const sentAt = this.saves.sent();
          try {
            const resp = update
              ? await vmsApi.updateVms(
                  exp,
                  send.map((name) => ({ name, ...update })),
                )
              : await vmsApi.resetVmSettings(exp, send);
            for (const vm of resp.data?.vms ?? []) {
              this.replaceRow(vm, { sentAt, own: fields });
              if (vm?.name) this.saves.saved(vm.name, fields, seqOf(resp));
            }
            items = batchOutcome(resp.data);
          } catch (err) {
            items = outcomeFromSettled(
              send,
              send.map(() => ({ status: 'rejected', reason: err })),
            );
          }
        }
        notifyOutcome(this.$buefy, {
          verbPast,
          noun: 'VM',
          items: [...items, ...skipped],
          onRetry: (retry) =>
            action == 'reset'
              ? this.resetSettings(retry)
              : this.sendBatch(action, retry),
        });
      },

      // Asks before scheduling, as a schedule gives every VM a new host,
      // then spins the Schedule button until the server answers.
      confirmSchedule(algorithm) {
        this.$buefy.dialog.confirm({
          title: 'Assign a Host Schedule',
          message:
            'This will assign every VM of the ' +
            escapeHTML(this.$route.params.id) +
            ' experiment to a host with the ' +
            escapeHTML(algorithm) +
            ' scheduler. It replaces the host of each VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Schedule',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => this.runSchedule(algorithm),
        });
      },

      async runSchedule(algorithm) {
        this.scheduling = true;
        try {
          const resp = await experimentsApi.scheduleExperiment(
            this.$route.params.id,
            algorithm,
          );
          this.applySchedule(resp.data?.schedule);
          notifyScheduleWarnings(this.$route.params.id, resp.data?.warnings);
          this.loadVmList();
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.scheduling = false;
        }
      },

      // sets each VM's host from a schedule of {vm, host} assignments
      applySchedule(schedule) {
        if (!schedule) return;
        if (this.canGetSchedule) {
          this.schedule = scheduleMap(schedule);
          this.scheduleFailed = false;
        }
        if (!this.experiment.vms) return;
        const hosts = new Map(schedule.map((s) => [s.vm, s.host]));
        this.experiment.vms = this.experiment.vms.map((vm) =>
          hosts.has(vm.name) ? { ...vm, host: hosts.get(vm.name) } : vm,
        );
      },

      showTagsModal(vm) {
        this.$buefy.modal.open({
          component: VMLabelsModal,
          trapFocus: true,
          hasModalCard: true,
          props: {
            vmName: vm.name,
            experiment: this.$route.params.id,
            tags: vm.tags ?? {},
          },
          events: {
            saved: () => {
              this.refreshVm(vm.name);
              this.loadVmList();
            },
          },
        });
      },

      // the VM afresh from the server, in its row and open card
      async refreshVm(name) {
        const exp = this.$route.params.id;
        if (!roleAllowed('vms', 'get', `${exp}/${name}`)) return;
        const sentAt = this.saves.sent();
        try {
          this.replaceRow((await vmsApi.getVm(exp, name)).data, { sentAt });
        } catch (err) {
          useErrorNotification(err);
        }
      },
    },

    watch: {
      activeTab(newVal, oldVal) {
        // each tab keeps its own search and history while the other is open
        this.searchVMs.cancel();
        this.tabSearches[oldVal] = {
          filter: this.searchName,
          history: this.searchHistory,
        };
        this.searchName = this.tabSearches[newVal]?.filter ?? '';
        this.searchHistory = this.tabSearches[newVal]?.history ?? [];
        this.syncQuery();

        if (newVal == TAB.VMS) {
          this.updateExperiment();
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
          if (search !== this.searchName) this.searchVMs(search);
          return;
        }
        // the activeTab watcher opens the tab with the link's search
        this.tabSearches[tab] = { ...this.tabSearches[tab], filter: search };
        this.activeTab = tab;
      },
    },

    data() {
      return {
        expModal: {
          active: false,
          vm: {},
        },
        // the VM of the link whose details the page opened
        linkedVm: '',
        // the schedulers to offer, the built-in ones until the server lists
        // its own
        schedulers: [...BUILTIN_SCHEDULERS],
        // whether a schedule waits on the server
        scheduling: false,
        // each VM the schedule puts on a host, by name; null until loaded
        schedule: null,
        // whether the last schedule request failed
        scheduleFailed: false,
        // every label and host of the VMs, and how many VMs the experiment
        // has (null while not known), whatever the search and filters
        vmLabels: [],
        vmHosts: [],
        vmTotal: null,
        experiment: {},
        // the Files tab's category picker, and the categories it offers
        fileCategory: null,
        fileCategories: [],
        hosts: [],
        // the disk list, null until it has loaded
        disks: null,
        searchName: searchFromQuery(this.$route.query),
        // the search and its history of the tab not open
        tabSearches: {},
        // the labels and hosts the VM table is filtered by
        labelFilter: [],
        hostFilter: [],
        // whether the Start waits on the server
        starting: false,
        // the setting changes waiting on the server: by VM, each field's value
        settingPending: {},
        // the VMs whose settings reset waits on the server
        resetPending: [],
        // the selection action waiting on the server, or null
        bulkPending: null,
        searchHistory: [],
        selectedRows: [],
        activeTab: tabFromQuery(this.$route.query, this.offeredTabs()),
      };
    },
  };
</script>

<style scoped>
  /* Room for the tabs drawn beside the toolbar's last row: the VM filters
     wrap onto a row of their own above the search box when it runs out. */
  .exp-toolbar {
    padding-left: 12rem;
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
