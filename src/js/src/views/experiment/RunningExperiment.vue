<template>
  <div class="content">
    <b-modal v-model="expModal.active" @close="resetExpModal" has-modal-card>
      <VmDetailsCard
        :vm="expModalVm"
        :full-name="expModal.fullName"
        :experiment="experiment.name"
        :capture-pending="pendingCaptureIfaces(expModalVm.name)"
        :capture-all-pending="!!captureAllPending[expModalVm.name]"
        :backing-chain="expModal.backingChain"
        :disk="expModal.disk"
        :cd-rom-disk="expModal.cdRomDisk"
        :files-dir="expModal.filesDir"
        :screenshot="getVmScreenshot(expModalVm)"
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
    <b-modal
      v-model="portForwardModal.active"
      @close="resetPortForwardModal"
      has-modal-card>
      <div class="modal-card" style="width: 30em">
        <header class="modal-card-head">
          <p class="modal-card-title">Create New Port Forward</p>
        </header>
        <section class="modal-card-body">
          <b-field label="Source Port" label-for="port-forward-src-port">
            <b-input
              type="text"
              id="port-forward-src-port"
              :compat-fallthrough="false"
              v-model="portForwardModal.srcPort"></b-input>
          </b-field>
          <b-field label="Destination Host" label-for="port-forward-dst-host">
            <b-input
              type="text"
              id="port-forward-dst-host"
              :compat-fallthrough="false"
              v-model="portForwardModal.dstHost"></b-input>
          </b-field>
          <b-field label="Destination Port" label-for="port-forward-dst-port">
            <b-input
              type="text"
              id="port-forward-dst-port"
              :compat-fallthrough="false"
              v-model="portForwardModal.dstPort"></b-input>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-success" @click="createPortForward()">
            Create
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal v-model="vlanModal.active" has-modal-card>
      <div class="modal-card" style="width: 25em">
        <header class="modal-card-head">
          <p class="modal-card-title">Change the VLAN</p>
        </header>
        <section class="modal-card-body">
          Move interface {{ vlanModal.vmNetIndex }} from
          {{ formatLowercase(vlanModal.vmFromNet) }} to a new one for the
          {{ vlanModal.active ? vlanModal.vmName : 'unknown' }} VM. <br /><br />
          <b-field>
            <b-select
              :aria-label="`New VLAN for interface ${vlanModal.vmNetIndex} of VM ${vlanModal.vmName}`"
              v-model="vlan"
              expanded>
              <option value="0">disconnect</option>
              <option
                v-for="(n, index) in experiment.vlans"
                :key="index"
                :value="n">
                {{ formatLowercase(n.alias) }} ({{ n.vlan }})
              </option>
            </b-select>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button
            class="button is-success"
            @click="
              changeVlan(
                vlanModal.vmNetIndex,
                vlan,
                vlanModal.vmFromNet,
                vlanModal.vmName,
              )
            ">
            Change
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal
      v-model="redeployModal.active"
      @close="resetRedeployModal"
      has-modal-card
      ref="reDeploy">
      <div class="modal-card" style="width: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">
            Redeploy the
            {{ pluralWord(redeployModal.vm.length, 'VM') }}
          </p>
        </header>
        <section class="modal-card-body">
          <div v-if="redeployModal.vm.length > 0">
            <div v-for="(vmI, index) in redeployModal.vm" :key="index">
              <div>
                <hr v-if="parseInt(index) > 0" />
                Modify current settings and redeploy
                {{ vmI.name }} <br /><br />
                CPUs:
                <b-tooltip label="menu for assigning cpus" type="is-dark">
                  <b-select
                    :aria-label="`CPUs for VM ${vmI.name}`"
                    v-model="vmI.cpus"
                    expanded>
                    <option value="1">1</option>
                    <option value="2">2</option>
                    <option value="3">3</option>
                    <option value="4">4</option>
                    <option value="5">5</option>
                    <option value="6">6</option>
                    <option value="7">7</option>
                    <option value="8">8</option>
                  </b-select>
                </b-tooltip>
                Memory:
                <b-tooltip label="menu for assigning memory" type="is-dark">
                  <b-select
                    :aria-label="`Memory for VM ${vmI.name}`"
                    v-model="vmI.ram"
                    expanded>
                    <option value="512">512 MB</option>
                    <option value="1024">1 GB</option>
                    <option value="2048">2 GB</option>
                    <option value="3072">3 GB</option>
                    <option value="4096">4 GB</option>
                    <option value="8192">8 GB</option>
                    <option value="12288">12 GB</option>
                    <option value="16384">16 GB</option>
                  </b-select>
                </b-tooltip>
                <br /><br />
                Disk:
                <DiskSelect
                  v-model="vmI.disk"
                  :disks="disks"
                  :label="`Disk for VM ${vmI.name}`" />
                <br /><br />
                Replicate Original Injection(s):
                <b-tooltip
                  label="menu for replicating injections"
                  type="is-dark">
                  <b-select
                    :aria-label="`Replicate original injections for VM ${vmI.name}`"
                    v-model="vmI.inject"
                    expanded>
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </b-select>
                </b-tooltip>
              </div>
            </div>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button" type="button" @click="closeModal('reDeploy')">
            Cancel
          </button>
          <button
            class="button is-success"
            @click="redeployVm(redeployModal.vm)">
            Redeploy
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal
      v-model="diskImageModal.active"
      has-modal-card
      @close="resetDiskImageModal"
      ref="diskImage">
      <div class="modal-card" style="width: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">Create a Disk Image</p>
        </header>
        <section class="modal-card-body">
          <div v-if="diskImageModal.vm.length > 0">
            <div v-for="(vmI, index) in diskImageModal.vm" :key="index">
              <div>
                <hr
                  v-if="parseInt(index) > 0"
                  style="color: #595959; background-color: #595959" />
                Create disk image of the {{ vmI.name }} VM with filename:
                <br /><br />
                <b-field
                  :type="vmI.nameErrType"
                  :message="vmI.nameErrMsg"
                  autofocus>
                  <b-input
                    :aria-label="`Disk image filename for VM ${vmI.name}`"
                    type="text"
                    v-model="vmI.filename"
                    focus></b-input>
                </b-field>
              </div>
            </div>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button" type="button" @click="closeModal('diskImage')">
            Cancel
          </button>
          <button
            class="button is-success"
            :disabled="!validate(diskImageModal)"
            @click="backingImage(diskImageModal.vm)">
            Create
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal
      v-model="memorySnapshotModal.active"
      has-modal-card
      @close="resetMemorySnapshotModal"
      ref="memorySnapshot">
      <div class="modal-card" style="width: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">Create memory snapshot</p>
        </header>
        <section class="modal-card-body">
          <div v-if="memorySnapshotModal.vm.length > 0">
            <div v-for="(vmI, index) in memorySnapshotModal.vm" :key="index">
              <div>
                <hr
                  v-if="parseInt(index) > 0"
                  style="color: #595959; background-color: #595959" />
                Create a memory snapshot for the
                {{ vmI.name }} VM with filename: <br /><br />
                <b-field
                  :type="vmI.nameErrType"
                  :message="vmI.nameErrMsg"
                  autofocus>
                  <b-input
                    :aria-label="`Memory snapshot filename for VM ${vmI.name}`"
                    type="text"
                    v-model="vmI.filename"
                    focus></b-input>
                </b-field>
              </div>
            </div>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button
            class="button"
            type="button"
            @click="closeModal('memorySnapshot')">
            Cancel
          </button>
          <button
            class="button is-success"
            :disabled="!validate(memorySnapshotModal)"
            @click="createMemorySnapshot(memorySnapshotModal.vm)">
            Create
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal v-model="appsModal.active" @close="resetAppsModal" has-modal-card>
      <div class="modal-card" style="width: 25em">
        <header class="modal-card-head">
          <p class="modal-card-title">phēnix Apps</p>
        </header>
        <section class="modal-card-body">
          <div v-if="appsModal.triggerable.length">
            <b-checkbox
              v-for="(a, index) in appsModal.triggerable"
              :key="index"
              :native-value="a"
              v-model="appsModal.apps"
              type="is-light">
              {{ a }}
            </b-checkbox>
          </div>
          <div v-else>
            <span>This experiment doesn't include any triggerable apps.</span>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <div
            v-if="
              roleAllowed('experiments/trigger', 'create', experiment.name)
            ">
            <b-tooltip label="start selected apps" type="is-light is-left">
              <b-button
                v-if="appsModal.apps.length > 0"
                class="button is-success"
                @click="startApps(appsModal.apps)"
                >Trigger Apps</b-button
              >
              <b-button v-else disabled class="button is-success"
                >Trigger Apps</b-button
              >
            </b-tooltip>
          </div>
        </footer>
      </div>
    </b-modal>
    <b-modal
      v-model="opticalDiscModal.active"
      has-modal-card
      @close="resetOpticalDiscModal"
      ref="opticalDisc">
      <div class="modal-card optical-disc-card">
        <header class="modal-card-head">
          <p class="modal-card-title">
            {{
              opticalDiscModal.current ? 'Change the CD-ROM' : 'Insert a CD-ROM'
            }}
            for {{ opticalDiscModal.vmName }}
          </p>
        </header>
        <section class="modal-card-body">
          <p v-if="opticalDiscModal.current" class="optical-disc-current">
            Inserted:
            <span class="is-family-monospace" :title="opticalDiscModal.current">
              {{ insertedIsoLabel }}
            </span>
          </p>
          <p
            v-if="opticalDiscModal.loading"
            class="optical-disc-state optical-disc-loading"
            role="status">
            <span class="capture-spinner"></span>
            Loading ISO images…
          </p>
          <p
            v-else-if="opticalDiscModal.error"
            class="optical-disc-state has-text-danger"
            role="alert">
            {{ opticalDiscModal.error }}
          </p>
          <p
            v-else-if="!opticalDiscModal.isos.length"
            class="optical-disc-state">
            There are no ISO images to insert. Upload an .iso file on the
            <router-link :to="{ name: 'disks' }">Disks</router-link> page, then
            try again.
          </p>
          <b-field v-else label="ISO image" label-for="optical-disc-iso">
            <b-select
              id="optical-disc-iso"
              :compat-fallthrough="false"
              v-model="opticalDiscModal.disc"
              class="optical-disc-select"
              placeholder="Select an ISO image"
              expanded>
              <option
                v-for="iso in isoOptions.inside"
                :key="iso.value"
                :value="iso.value"
                :disabled="iso.disabled">
                {{ iso.label }}
              </option>
              <optgroup v-if="isoOptions.outside.length" :label="OUTSIDE_GROUP">
                <option
                  v-for="iso in isoOptions.outside"
                  :key="iso.value"
                  :value="iso.value"
                  :disabled="iso.disabled">
                  {{ iso.label }}
                </option>
              </optgroup>
            </b-select>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button
            class="button"
            type="button"
            @click="closeModal('opticalDisc')">
            Cancel
          </button>
          <button
            v-if="opticalDiscModal.current"
            class="button is-danger is-outlined optical-disc-eject"
            @click="ejectOpticalDisc()">
            Eject
          </button>
          <button
            class="button is-success optical-disc-insert"
            :disabled="
              !opticalDiscModal.disc ||
              opticalDiscModal.disc === opticalDiscModal.current
            "
            @click="insertOpticalDisc()">
            {{ opticalDiscModal.current ? 'Change' : 'Insert' }}
          </button>
        </footer>
      </div>
    </b-modal>
    <div class="level is-vcentered">
      <div class="level-left is-block">
        <span style="font-weight: bold; font-size: x-large"
          >Experiment: {{ $route.params.id }}</span
        ><br />
        <span v-if="experiment.scenario" style="font-weight: bold"
          >Scenario: {{ experiment.scenario }}</span
        >
      </div>
      <div
        class="level-right is-clickable"
        v-if="experiment.scenario"
        style="max-width: 50%"
        @click="getApps()">
        <span style="font-weight: bold">Apps:</span>&nbsp;
        <div style="display: flex; flex-wrap: wrap; gap: 4px">
          <b-tag v-for="(a, index) in experiment.apps" :key="index">
            {{ a }}
          </b-tag>
        </div>
      </div>
    </div>
    <b-field position="is-right" grouped>
      <!-- Multi-VM options shown next to search -->
      <b-field v-if="isMultiVmSelected" position="is-center" grouped>
        <b-field v-if="selectionAllows('start') && !showModifyStateBar">
          <b-tooltip label="start" type="is-light">
            <b-button
              aria-label="Start selected VMs"
              class="button is-success"
              icon-left="play"
              @click="actOnSelection(startVm)">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field v-if="selectionAllows('pause') && !showModifyStateBar">
          <b-tooltip label="pause" type="is-light">
            <b-button
              aria-label="Pause selected VMs"
              class="button is-warning"
              icon-left="pause"
              @click="actOnSelection(pauseVm)">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field
          v-if="selectionAllows('memorySnapshot') && !showModifyStateBar">
          <b-tooltip label="create memory snapshot" type="is-light">
            <b-button
              aria-label="Create memory snapshots of selected VMs"
              class="button is-light"
              icon-left="database"
              @click="actOnSelection(queueMemorySnapshotVMs)">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field v-if="selectionAllows('commit') && !showModifyStateBar">
          <b-tooltip label="create backing image" type="is-light">
            <b-button
              aria-label="Create backing images for selected VMs"
              class="button is-light"
              icon-left="save"
              @click="actOnSelection(diskImage)">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field v-if="selectionAllows('snapshot') && !showModifyStateBar">
          <b-tooltip label="create vm snapshot" type="is-light">
            <b-button
              aria-label="Create snapshots of selected VMs"
              class="button is-light"
              icon-left="camera"
              @click="actOnSelection(captureSnapshot)">
            </b-button>
          </b-tooltip>
        </b-field>
        <b-field v-if="!showModifyStateBar">
          <b-tooltip label="modify state" type="is-light">
            <b-button
              aria-label="Modify state of selected VMs"
              class="button is-light"
              icon-left="edit"
              @click="showModifyStateBar = true">
            </b-button>
          </b-tooltip>
        </b-field>
        <template v-if="showModifyStateBar">
          <b-field>
            <b-tooltip
              v-if="selectionAllows('redeploy')"
              label="redeploy"
              type="is-light">
              <b-button
                aria-label="Redeploy selected VMs"
                class="button is-success"
                icon-left="history"
                @click="actOnSelection(redeploy)">
              </b-button>
            </b-tooltip>
          </b-field>

          <b-field>
            <b-tooltip
              v-if="selectionAllows('resetDisk')"
              label="reset disk state"
              type="is-light">
              <b-button
                aria-label="Reset disk state of selected VMs"
                class="button is-success"
                icon-left="undo-alt"
                @click="actOnSelection(resetVmState)">
              </b-button>
            </b-tooltip>
          </b-field>

          <b-field>
            <b-tooltip
              v-if="selectionAllows('restart')"
              label="restart"
              type="is-light">
              <b-button
                aria-label="Restart selected VMs"
                class="button is-success"
                icon-left="sync-alt"
                @click="actOnSelection(restartVm)">
              </b-button>
            </b-tooltip>
          </b-field>
          <b-field>
            <b-tooltip
              v-if="selectionAllows('shutdown')"
              label="shutdown"
              type="is-light">
              <b-button
                aria-label="Shut down selected VMs"
                class="button is-danger"
                icon-left="power-off"
                @click="actOnSelection(shutdownVm)">
              </b-button>
            </b-tooltip>
          </b-field>
          <b-field>
            <b-tooltip
              v-if="selectionAllows('kill')"
              label="kill"
              type="is-light">
              <b-button
                aria-label="Kill selected VMs"
                class="button is-danger"
                icon-left="skull-crossbones"
                @click="actOnSelection(killVm)">
              </b-button>
            </b-tooltip>
          </b-field>
          <b-field>
            <b-tooltip label="close toolbar" type="is-light">
              <b-button
                aria-label="Close state actions"
                class="button is-light"
                icon-left="window-close"
                @click="showModifyStateBar = false">
              </b-button>
            </b-tooltip>
          </b-field>
        </template>
        <hr style="width: 1px; height: 100%; margin: 0" />
      </b-field>

      <b-field position="is-right">
        <b-field
          v-if="
            activeTab == 1 &&
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
        <b-field>
          <b-autocomplete
            v-model="search.filter"
            :placeholder="searchPlaceholder"
            icon="search"
            :data="searchHistory"
            @typing="searchVMs"
            @select="(option) => searchVMs(option)">
            <!-- netflow is filtered in place; there is nothing to list here -->
            <template v-if="activeTab != NETFLOW_TAB" #empty
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
          <b-tooltip label="view soh" type="is-light" :delay="500">
            <router-link
              v-if="roleAllowed('experiments', 'get', experiment.name)"
              :aria-label="`View state of health for experiment ${$route.params.id}`"
              class="button is-light"
              :to="{
                name: 'soh',
                params: { id: $route.params.id },
              }">
              <b-icon icon="heartbeat"></b-icon>
            </router-link>
          </b-tooltip>
        </b-field>
        <b-field>
          <b-tooltip label="view scorch" type="is-light" :delay="500">
            <router-link
              v-if="roleAllowed('experiments', 'get', experiment.name)"
              :aria-label="`View SCORCH runs for experiment ${$route.params.id}`"
              class="button is-light"
              :to="{
                name: 'scorchruns',
                params: { id: $route.params.id },
              }">
              <b-icon icon="fire"></b-icon>
            </router-link>
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
                custom>
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
        <b-tab-item label="VMs" icon="desktop">
          <b-field v-if="paginationNeeded" grouped position="is-right">
            <div class="control is-flex">
              <b-switch
                v-model="table.isPaginated"
                @update:modelValue="updateTable()"
                size="is-small"
                type="is-light"
                >Paginate</b-switch
              >
            </div>
          </b-field>
          <b-table
            :data="experiment.vms"
            :paginated="table.isPaginated"
            aria-next-label="Next page"
            aria-previous-label="Previous page"
            aria-page-label="Page"
            aria-current-label="Current page"
            backend-pagination
            :total="table.total"
            :per-page="table.perPage"
            @page-change="onPageChange"
            :pagination-simple="table.isPaginationSimple"
            :pagination-size="table.paginationSize"
            backend-sorting
            default-sort-direction="asc"
            default-sort="name"
            @sort="onSort"
            ref="vmTable">
            <template #empty>
              <section class="section">
                <div class="content has-text-white has-text-centered">
                  {{ vmsEmptyText }}
                </div>
              </section>
            </template>
            <b-table-column field="multiselect" label="">
              <template v-slot:header>
                <b-tooltip label="Select/Unselect All" type="is-dark">
                  <b-checkbox v-model="checkAll">
                    <span class="is-sr-only">Select all VMs</span>
                  </b-checkbox>
                </b-tooltip>
              </template>
              <template v-slot:default="props">
                <template v-if="!props.row.busy">
                  <div>
                    <b-checkbox
                      :disabled="props.row.external"
                      v-model="vmSelectedArray"
                      :native-value="props.row.name">
                      <span class="is-sr-only">
                        Select VM {{ props.row.name }}
                      </span>
                    </b-checkbox>
                  </div>
                </template>
                <template v-else> BUSY </template>
              </template>
            </b-table-column>
            <b-table-column
              field="name"
              label="Node"
              width="150"
              sortable
              header-class="sort-inline"
              centered
              v-slot="props">
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'get',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip label="details and actions" type="is-dark">
                  <span
                    class="tag is-medium is-clickable"
                    :class="decorator(props.row.state, props.row.busy)">
                    <div class="field">
                      <div @click="getInfo(props.row)">
                        {{ props.row.name }}
                      </div>
                    </div>
                  </span>
                </b-tooltip>
              </template>
              <template v-else-if="props.row.external">
                <span class="tag is-medium">
                  <div class="field">
                    {{ props.row.name }}
                  </div>
                </span>
              </template>
              <template v-else>
                <b-tooltip label="get info for the vm" type="is-dark">
                  <span
                    class="tag is-medium"
                    :class="decorator(props.row.state, props.row.busy)">
                    <div class="field">
                      <div
                        @click="
                          expModal.active = true;
                          expModal.vm = props.row;
                        ">
                        {{ props.row.name }}
                      </div>
                    </div>
                  </span>
                </b-tooltip>
              </template>
              <section v-if="props.row.busy">
                <b-progress
                  size="is-small"
                  type="is-warning"
                  show-value
                  :value="props.row.percent"
                  format="percent"></b-progress>
              </section>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.screenshot"
              field="screenshot"
              label="Screenshot"
              centered
              v-slot="props">
              <template v-if="props.row.external">
                <img
                  :alt="screenshotAlt(props.row)"
                  :src="getVmScreenshot(props.row)"
                  width="200"
                  height="150" />
              </template>
              <template
                v-else-if="
                  (props.row.running && !props.row.busy) ||
                  (props.row.delayed_start && props.row.state == 'BUILDING')
                ">
                <a :href="vncLoc(props.row)" target="_blank">
                  <img
                    :alt="consoleLinkAlt(props.row)"
                    :src="getVmScreenshot(props.row)"
                    :width="props.row.screenshot ? undefined : 200"
                    :height="props.row.screenshot ? undefined : 150" />
                </a>
              </template>
              <template v-else-if="props.row.busy">
                <b-tooltip
                  label="Screenshot not available while busy with action"
                  type="is-dark">
                  <img
                    :alt="screenshotAlt(props.row)"
                    :src="getVmScreenshot(props.row)"
                    width="200"
                    height="150" />
                </b-tooltip>
              </template>
              <template v-else>
                <img
                  :alt="screenshotAlt(props.row)"
                  :src="getVmScreenshot(props.row)"
                  width="200"
                  height="150" />
              </template>
            </b-table-column>
            <b-table-column
              v-if="isDelayed()"
              field="delayed"
              label="Delay"
              sortable
              header-class="sort-inline"
              centered
              v-slot="props">
              <b-tag
                type="is-info"
                v-if="props.row.delayed_start && props.row.state == 'BUILDING'"
                >{{ props.row.delayed_start }}</b-tag
              >
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.host"
              field="host"
              label="Host"
              width="150"
              sortable
              header-class="sort-inline"
              v-slot="props">
              <template v-if="props.row.external"> EXTERNAL </template>
              <template v-else>
                {{ props.row.host }}
              </template>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.ipv4"
              field="ipv4"
              label="IP"
              width="150">
              <template v-slot:default="props">
                <template
                  v-if="
                    roleAllowed(
                      'vms/captures',
                      'create',
                      experiment.name + '/' + props.row.name,
                    ) &&
                    props.row.running &&
                    !props.row.busy
                  ">
                  <b-tooltip
                    :label="updateCaptureLabel(props.row)"
                    type="is-dark">
                    <div class="field">
                      <div
                        v-for="(ip, index) in props.row.ipv4"
                        :class="tapDecorator(props.row.captures, index)"
                        :key="index"
                        :aria-busy="isCapturePending(props.row.name, index)"
                        @click="handlePcap(props.row, index)">
                        {{ ip }}
                        <span
                          v-if="isCapturePending(props.row.name, index)"
                          class="capture-spinner"
                          title="waiting for the server"></span>
                      </div>
                    </div>
                  </b-tooltip>
                </template>
                <template v-else>
                  {{ formatStringify(props.row.ipv4) }}
                </template>
              </template>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.network"
              field="network"
              label="Network"
              v-slot="props">
              <template
                v-if="
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  ) &&
                  props.row.running &&
                  !props.row.busy
                ">
                <b-tooltip label="change vlan(s)" type="is-dark">
                  <div class="field">
                    <div
                      v-for="(n, index) in props.row.networks"
                      :key="index"
                      @click="
                        vlanModal.active = true;
                        vlanModal.vmName = props.row.name;
                        vlanModal.vmFromNet = n;
                        vlanModal.vmNetIndex = index;
                      ">
                      {{ formatLowercase(n) }}
                    </div>
                  </div>
                </b-tooltip>
              </template>
              <template v-else>
                {{ formatStringify(props.row.networks) }}
              </template>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.taps"
              field="taps"
              label="Taps"
              v-slot="props">
              <template
                v-if="
                  roleAllowed(
                    'vms/captures',
                    'create',
                    experiment.name + '/' + props.row.name,
                  ) &&
                  props.row.running &&
                  !props.row.busy
                ">
                <b-tooltip
                  :label="updateCaptureLabel(props.row)"
                  type="is-dark">
                  <div class="field">
                    <div
                      v-for="(t, index) in props.row.taps"
                      :class="tapDecorator(props.row.captures, index)"
                      :key="index"
                      :aria-busy="isCapturePending(props.row.name, index)"
                      @click="handlePcap(props.row, index)">
                      {{ formatLowercase(t) }}
                      <span
                        v-if="isCapturePending(props.row.name, index)"
                        class="capture-spinner"
                        title="waiting for the server"></span>
                    </div>
                  </div>
                </b-tooltip>
              </template>
              <template v-else>
                {{ formatStringify(props.row.taps) }}
              </template>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.uptime"
              field="uptime"
              label="Uptime"
              sortable
              header-class="sort-inline"
              cell-class="nowrap-cell"
              v-slot="props">
              <template v-if="props.row.external"> unknown </template>
              <template v-else>
                {{ formatUptime(props.row.uptime) }}
              </template>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.labels"
              label="Labels"
              centered
              v-slot="props">
              <b-tooltip label="View/Edit Labels" type="is-dark">
                <div @click="showTagsModal(props.row)" class="is-clickable">
                  <font-awesome-layers full-width>
                    <font-awesome-icon icon="tag" />
                    <font-awesome-layers-text
                      counter
                      :value="tagCount(props.row.tags)" />
                  </font-awesome-layers>
                </div>
              </b-tooltip>
            </b-table-column>
            <b-table-column
              v-if="columnVisibility.actions && rowActionsAllowed"
              field="actions"
              label="Actions"
              width="120"
              centered
              v-slot="props">
              <VmRowActions
                :vm="props.row"
                :full-name="experiment.name + '/' + props.row.name"
                :pending="
                  captureAllPending[props.row.name]
                    ? ['captureAll', 'stopCaptures']
                    : []
                "
                @action="(name) => onVmAction(name, props.row)" />
            </b-table-column>
          </b-table>
        </b-tab-item>
        <b-tab-item
          label="Files"
          icon="file-alt"
          :visible="roleAllowed('experiments/files', 'list', experiment.name)">
          <ExperimentFilesTab
            ref="filesTab"
            paginate-key="running-files"
            :filter="search.filter"
            :category="fileCategory"
            @categories="(list) => (fileCategories = list)"
            @found="addSearchHistory"
            @waiting="(on) => (isWaiting = on)" />
        </b-tab-item>
        <b-tab-item label="VNC" icon="arrow-pointer">
          <b-field
            label="Zoom Level"
            style="width: 300px; margin-left: 32px"
            horizontal>
            <b-slider
              aria-label="Zoom level"
              :min="2"
              :max="16"
              :custom-formatter="(val) => 0.25 + (val - 1) * 0.25 + 'x'"
              v-model="vncZoom">
              <template v-for="val in [4, 8, 12, 16]" :key="val">
                <b-slider-tick :value="val">
                  {{ 0.25 + (val - 1) * 0.25 + 'x' }}
                </b-slider-tick>
              </template>
            </b-slider>
          </b-field>
          <div
            style="
              display: flex;
              flex-direction: row;
              flex-wrap: wrap;
              align-items: flex-end;
              justify-content: center;
              gap: 4px;
            ">
            <template v-if="vncVms.length">
              <div v-for="vm in vncVms" :key="vm.name">
                <a :href="vncLoc(vm)" target="_blank">
                  <img
                    :alt="consoleLinkAlt(vm)"
                    :src="getVmScreenshot(vm)"
                    :width="vncWidth"
                    style="display: block" />
                </a>
                <a
                  v-if="
                    roleAllowed('vms', 'get', experiment.name + '/' + vm.name)
                  "
                  class="vnc-tile-name"
                  @click="getInfo(vm)"
                  >{{ vm.name }}</a
                >
                <span v-else class="vnc-tile-name">{{ vm.name }}</span>
              </div>
            </template>
            <template v-else>{{
              experiment.name ? 'No VMs with a VNC console' : loadingText('VMs')
            }}</template>
          </div>
        </b-tab-item>
        <b-tab-item
          label="Netflow"
          icon="circle-nodes"
          v-if="roleAllowed('experiments/netflow', 'get', experiment.name)">
          <div
            v-if="netflow.lines.length === 0"
            class="content has-text-white has-text-centered">
            {{
              netflow.starting
                ? 'Starting netflow capture…'
                : 'No netflow captures yet. Start one with the netflow button above.'
            }}
          </div>
          <template v-else>
            <p class="netflow-summary">
              {{ netflowSummary }}
            </p>
            <div
              ref="netflowView"
              class="netflow-view"
              :style="{ maxHeight: netflowMaxHeight }">
              <table class="table is-narrow is-fullwidth netflow-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Source</th>
                    <th>Destination</th>
                    <th>Protocol</th>
                    <th class="has-text-right">Packets</th>
                    <th class="has-text-right">Bytes</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="line in netflowRows"
                    :key="line.id"
                    :class="{ 'netflow-marker': line.marker }">
                    <td>{{ line.time }}</td>
                    <td v-if="line.marker" colspan="5">{{ line.marker }}</td>
                    <template v-else>
                      <td>{{ line.src }}</td>
                      <td>{{ line.dst }}</td>
                      <td>{{ line.proto }}</td>
                      <td class="has-text-right">{{ line.packets }}</td>
                      <td class="has-text-right">{{ line.bytes }}</td>
                    </template>
                  </tr>
                </tbody>
              </table>
            </div>
          </template>
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
  import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
  import { BSlider, BSliderTick } from 'buefy';
  import VMLabelsModal from '@/components/VMLabelsModal.vue';
  import DiskSelect from '@/components/experiment/DiskSelect.vue';
  import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
  import VmRowActions from '@/components/experiment/VmRowActions.vue';
  import {
    ROW_ACTIONS,
    actionPermitted,
    captureTargets,
    partitionVMsForAction,
    skippedVMsText,
    vmStateType,
  } from '@/components/experiment/vmActions.js';
  import { tagCount } from '@/utils/tagCount';
  import {
    addWsHandler,
    onWsReconnect,
    removeWsHandler,
    sendWsMsg,
  } from '@/utils/websocket';
  import { usePhenixStore } from '@/store';
  import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
  import { debounce } from 'lodash-es';
  import { markRaw } from 'vue';
  import { readPref, writePref } from '@/utils/prefs.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import axiosInstance from '@/utils/axios.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { cachePage } from '@/utils/pageCache.js';
  import { experimentKey, fetchRunningExperiment } from '@/utils/pageData.js';
  import { useTable } from '@/utils/useTable.js';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import {
    applyStopCaptureUpdate,
    captureFilename,
    isInterfaceCapturing,
    hasMultipleCaptures,
  } from '@/utils/captures.js';
  import {
    OUTSIDE_GROUP,
    backingChain,
    baseName,
    diskLabel,
    diskList,
    diskOptions,
    filesDir,
    findDisk,
    isoDisks,
  } from '@/utils/diskChain.js';
  import { listText, namedList, pluralWord } from '@/utils/plural.js';
  import externalImg from '@/assets/imgs/external.png';
  import notAvailableImg from '@/assets/imgs/not-available.png';
  import delayedImg from '@/assets/imgs/delayed.png';
  import notRunningImg from '@/assets/imgs/not-running.png';
  import loadingScreenshotImg from '@/assets/imgs/loading-screenshot.svg';
  import { roomInWindow } from '@/utils/viewportFit.js';

  // how long a VM shows "Loading Screenshot" before "Not Available"; minimega
  // takes screenshots one at a time, so large experiments need a while
  const SCREENSHOT_WAIT_MS = 60000;

  // the screenshot width the server uses until the page asks for another
  const DEFAULT_SCREENSHOT_WIDTH = 200;

  // the netflow tab's position among the tabs
  const NETFLOW_TAB = 3;
  // flows kept for the netflow tab, and how many of them it shows at once
  const NETFLOW_MAX_LINES = 10000;
  const NETFLOW_SHOWN_LINES = 500;
  const IP_PROTOCOLS = { 1: 'ICMP', 6: 'TCP', 17: 'UDP', 58: 'ICMPv6' };

  let netflowLineID = 0;
  const clockTime = () => new Date().toLocaleTimeString();

  // a captured flow as the netflow tab shows it; markRaw: flows never change
  function netflowLine(flow) {
    return markRaw({
      id: ++netflowLineID,
      time: clockTime(),
      src: `${flow.src}:${flow.sport}`,
      dst: `${flow.dst}:${flow.dport}`,
      proto: IP_PROTOCOLS[flow.proto] ?? String(flow.proto),
      packets: flow.packets,
      bytes: flow.bytes,
    });
  }

  function netflowMarker(text) {
    return markRaw({ id: ++netflowLineID, time: clockTime(), marker: text });
  }

  const pad2 = (n) => String(n).padStart(2, '0');

  // zero-padded parts of the current local time, for generated filenames
  function timestampParts() {
    const now = new Date();
    return {
      year: now.getFullYear(),
      month: pad2(now.getMonth() + 1),
      day: pad2(now.getDate()),
      hours: pad2(now.getHours()),
      minutes: pad2(now.getMinutes()),
      seconds: pad2(now.getSeconds()),
    };
  }

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

  export default {
    emits: ['running'],
    components: {
      BSlider,
      BSliderTick,
      DiskSelect,
      ExperimentFilesTab,
      VmDetailsCard,
      VmRowActions,
    },
    mixins: [formattingMixin],
    setup() {
      const { table } = useTable({
        name: 'running-vms',
        fields: { total: 0, sortColumn: 'name', sortOrder: 'asc' },
      });
      return {
        table,
        roleAllowed,
        tagCount,
        pluralWord,
        NETFLOW_TAB,
        OUTSIDE_GROUP,
      };
    },

    async beforeUnmount() {
      this.unmounting = true;
      window.removeEventListener('resize', this.fitNetflowView);
      document.removeEventListener('visibilitychange', this.onVisibilityChange);
      removeWsHandler(this.handleWs);
      this.offWsReconnect();
      clearTimeout(this.screenshotsTimer);
      this.unsubscribeVms();

      if (this.socket) {
        this.socket.close();
        this.socket = null;
      }

      this.loader.stop();
    },

    async created() {
      addWsHandler(this.handleWs);
      window.addEventListener('resize', this.fitNetflowView);
      document.addEventListener('visibilitychange', this.onVisibilityChange);
      // the server drops VM subscriptions and the screenshot size when the
      // websocket reconnects
      this.offWsReconnect = onWsReconnect(() => {
        if (this.vncWidth != DEFAULT_SCREENSHOT_WIDTH) {
          this.setVncScreenshotRes(this.vncWidth);
        }
        if (this.experiment.name) this.updateTable();
      });
      this.loadColumnVisibility();
      // cached so the page opens at once; the header shows how old the copy
      // is while a fresh one loads
      this.loader = createPageLoader({
        key: experimentKey(this.$route.params.id),
        fetch: (signal) =>
          fetchRunningExperiment(this.$route.params.id, signal),
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
        },
        // the header's refresh button lists the VMs again too
        refresh: () => {
          this.updateTable();
          return this.loader.load();
        },
      });
      this.loader.start();

      try {
        await axiosInstance.get(`experiments/${this.$route.params.id}/netflow`);
        // the page may have been left while waiting
        if (!this.unmounting) this.handleNetflow(true, false);
      } catch {}
    },

    computed: {
      // the CD-ROM picker's ISO images, by label, those outside the minimega
      // files directory apart
      isoOptions() {
        return diskOptions(this.opticalDiscModal.isos);
      },
      // the ISO inserted when the CD-ROM picker opened, by its label, or its
      // full path when the list does not hold it
      insertedIsoLabel() {
        const { current, isos } = this.opticalDiscModal;
        return diskLabel(findDisk(isos, current)) || current;
      },

      // the server's features, from the store: they may arrive after the page
      // opens, so a copy taken when it opened could miss them
      features() {
        return usePhenixStore().features ?? [];
      },

      // every flow matching the search box, oldest first
      netflowMatches() {
        const text =
          this.activeTab == NETFLOW_TAB
            ? (this.search.filter ?? '').trim().toLowerCase()
            : '';
        const lines = this.netflow.lines;
        const matches = text
          ? lines.filter((line) =>
              [line.marker, line.src, line.dst, line.proto, line.time]
                .filter(Boolean)
                .join(' ')
                .toLowerCase()
                .includes(text),
            )
          : lines;
        return matches;
      },

      // the newest NETFLOW_SHOWN_LINES of those, newest first
      netflowRows() {
        return this.netflowMatches.slice(-NETFLOW_SHOWN_LINES).reverse();
      },

      netflowSummary() {
        const matches = this.netflowMatches.length;
        const shown = Math.min(matches, NETFLOW_SHOWN_LINES);
        const of =
          matches === this.netflow.lines.length
            ? `${matches}`
            : `${matches} matching (of ${this.netflow.lines.length})`;
        return shown < matches
          ? `Newest ${shown} of ${of} lines, newest first`
          : `${of} lines, newest first`;
      },
      vmsEmptyText() {
        // the websocket lists VMs only for roles allowed to
        if (!this.vmsLoaded && roleAllowed('vms', 'list')) {
          return loadingText('VMs');
        }
        if (this.search.filter) return 'No VMs match your search';
        return 'This experiment has no VMs';
      },

      vncWidth() {
        return this.activeTab == 2
          ? this.vncZoom * 50
          : DEFAULT_SCREENSHOT_WIDTH;
      },

      vncVms() {
        // Hide external/HIL nodes and DNB (do not boot) nodes from the VNC tab
        // since they don't have a VNC console to display.
        return (this.experiment.vms || []).filter(
          (vm) => !vm.external && !vm.dnb,
        );
      },

      paginationNeeded() {
        return this.table.total > this.table.perPage;
      },

      // The VM whose details are open: the details fetched when it opened,
      // kept current with the table row's live screenshot, state, and
      // captures, which websocket events update.
      expModalVm() {
        const vm = this.expModal.vm ?? {};
        const row = vm.name
          ? this.experiment.vms?.find((v) => v.name == vm.name)
          : null;
        if (!row || row === vm) return vm;
        return {
          ...vm,
          state: row.state ?? vm.state,
          running: row.running,
          busy: row.busy,
          percent: row.percent,
          captures: row.captures ?? vm.captures,
          screenshot: row.screenshot || vm.screenshot,
        };
      },

      // whether the role may take any of the Actions column's actions on
      // some VM; the column is hidden otherwise
      rowActionsAllowed() {
        return ROW_ACTIONS.some((name) => actionPermitted(name, roleAllowed));
      },

      isMultiVmSelected() {
        if (
          this.vmSelectedArray == undefined ||
          this.vmSelectedArray.length == 0
        ) {
          return false;
        }
        return true;
      },
    },

    methods: {
      loadingText,

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
        return this.$router.resolve({
          name: 'vnc',
          params: {
            id: this.$route.params.id,
            name: vm.name,
            token: usePhenixStore().token,
          },
        }).href;
      },

      getVmScreenshot(vm) {
        if (vm.external) {
          return externalImg;
        } else if (vm.running && !vm.busy && !vm.screenshot) {
          // the server sends screenshots after the VM list
          return this.screenshotsDue ? notAvailableImg : loadingScreenshotImg;
        } else if (vm.delayed_start && vm.state == 'BUILDING') {
          return delayedImg;
        } else if (vm.running && !vm.busy && vm.screenshot) {
          return vm.screenshot;
        } else if (vm.busy) {
          return notAvailableImg;
        } else {
          return notRunningImg;
        }
      },

      // Alt text for getVmScreenshot's image, which for a VM without a
      // screenshot is a picture of a message; keep the branches the same.
      screenshotAlt(vm) {
        const name = vm.name;
        if (vm.external) {
          return `VM ${name} is an external node`;
        } else if (vm.running && !vm.busy && !vm.screenshot) {
          return this.screenshotsDue
            ? `Screenshot of VM ${name} is not available`
            : `Loading the screenshot of VM ${name}`;
        } else if (vm.delayed_start && vm.state == 'BUILDING') {
          return `VM ${name} has a delayed start`;
        } else if (vm.running && !vm.busy && vm.screenshot) {
          return `Screenshot of VM ${name}`;
        } else if (vm.busy) {
          return `Screenshot of VM ${name} is not available while it is busy`;
        } else {
          return `VM ${name} is not running`;
        }
      },

      // Alt text for a screenshot that links to the VM's console: where the
      // link goes, then what the picture says when it isn't a screenshot.
      consoleLinkAlt(vm) {
        const link = `Open the console of VM ${vm.name}`;
        const alt = this.screenshotAlt(vm);
        return alt == `Screenshot of VM ${vm.name}` ? link : `${link}: ${alt}`;
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

      searchVMs: debounce(function (term) {
        if (term == null) {
          term = '';
        }
        this.search.filter = term;
        switch (this.activeTab) {
          case 0:
          case 2:
            // before the first load, apply() lists the VMs with this filter
            if (this.experiment.name) this.updateTable();
            break;
          case 1:
            this.$refs.filesTab?.reload();
            break;
          // the netflow tab filters what it already has (netflowRows)
        }
      }, 250),

      updateTable() {
        let number = this.table.currentPage;
        let size = this.table.perPage;

        if (!this.table.isPaginated) {
          number = 0;
          size = this.experiment.vm_count;
        }

        let msg = {
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
            filter: this.search.filter,
          },
        };

        sendWsMsg(msg);
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
        });
      },

      // the VM a per-VM event is about, or null when the event is for another
      // experiment or the VM table has not loaded
      eventVmName(msg) {
        const [exp, name] = (msg.resource.name ?? '').split('/');
        if (exp !== this.$route.params.id || !this.experiment.vms) return null;
        return name ?? null;
      },

      findVm(name) {
        return this.experiment.vms.find((vm) => vm.name == name);
      },

      // Swaps in the server's copy of a VM. A copy without a screenshot keeps
      // the one shown while the VM stays running; a VM that stopped, or is
      // starting again, has no current screen until the next one arrives.
      replaceVm(vm) {
        const vms = this.experiment.vms;
        const i = vms.findIndex((v) => v.name == vm.name);
        if (i === -1) return;
        const old = vms[i];
        vms[i] = {
          ...vm,
          screenshot:
            vm.screenshot || (vm.running && old.running ? old.screenshot : ''),
        };
        this.experiment.vms = [...vms];
      },

      setVmBusy(name, busy) {
        const row = this.findVm(name);
        if (!row) return;
        row.busy = busy;
        if (busy) row.percent = 0;
      },

      setVmPercent(name, fraction) {
        const row = this.findVm(name);
        if (!row) return;
        row.busy = true; // in case the starting message was missed
        row.percent = Math.round(fraction * 100);
      },

      // Apps publish their runs on apps/<app>, scheduled runs included, so
      // only the apps this page triggered get a toast.
      handleAppTrigger(msg) {
        const app = msg.resource.type.slice('apps/'.length);
        const i = this.triggeredApps.indexOf(app);
        if (msg.resource.name !== this.$route.params.id || i === -1) return;

        switch (msg.resource.action) {
          case 'success':
            this.$buefy.toast.open({
              message: 'phēnix app ' + app + ' was triggered successfully.',
              type: 'is-success',
              duration: 4000,
            });
            break;
          case 'error':
            showError(
              'Triggering phēnix app ' + app + ' failed',
              msg.result?.error,
            );
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
          axiosInstance.post(queue[0].url, queue[0].body).catch((err) => {
            useErrorNotification(err);
          });
        } else {
          this.redeployModal.active = false;
          this.resetRedeployModal();
          this.isWaiting = false;
        }
      },

      handleWs(msg) {
        if (msg.resource.type.startsWith('apps/')) {
          this.handleAppTrigger(msg);
          return;
        }

        switch (msg.resource.type) {
          // Stopped or deleted from elsewhere (the CLI, another window or
          // user): the page has nothing left to show, and leaving it tells
          // the server to stop screenshotting the VMs.
          case 'experiment': {
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
            this.$router.replace('/experiments/');

            break;
          }

          case 'experiment/vms': {
            if (
              msg.resource.action != 'list' ||
              msg.resource.name !== this.$route.params.id
            ) {
              return;
            }

            // screenshots follow the list, so keep the ones already shown
            const shots = new Map(
              (this.experiment.vms ?? []).map((vm) => [vm.name, vm.screenshot]),
            );
            this.experiment.vms = msg.result.vms.map((vm) => ({
              ...vm,
              screenshot: vm.screenshot || shots.get(vm.name),
            }));
            this.table.total = msg.result.total;
            this.vmsLoaded = true;

            // a VM still without a screenshot by then has none to show
            this.screenshotsDue = false;
            clearTimeout(this.screenshotsTimer);
            this.screenshotsTimer = setTimeout(() => {
              this.screenshotsDue = true;
            }, SCREENSHOT_WAIT_MS);

            // keep the cached page current, unless it is a filtered view or
            // a later page: the page reopens unfiltered on its first page
            if (
              !this.search.filter &&
              (!this.table.isPaginated || this.table.currentPage === 1)
            ) {
              cachePage(experimentKey(this.$route.params.id), {
                ...this.experiment,
              });
            }

            // Only add successful searches to the search history
            if (this.search.filter && this.table.total > 0) {
              this.addSearchHistory();
            }

            this.isWaiting = false;

            break;
          }

          case 'experiment/vm': {
            const name = this.eventVmName(msg);
            if (name === null) break;

            switch (msg.resource.action) {
              case 'update': {
                this.replaceVm(msg.result);

                this.$buefy.toast.open({
                  message:
                    'The VM ' +
                    msg.result.name +
                    ' has been successfully updated.',
                  type: 'is-success',
                });

                break;
              }

              case 'delete': {
                const vms = this.experiment.vms;
                const i = vms.findIndex((v) => v.name == name);
                if (i !== -1) {
                  vms.splice(i, 1);
                }

                this.$buefy.toast.open({
                  message: 'The ' + name + ' VM was killed.',
                  type: 'is-success',
                });

                break;
              }

              // also published when a delayed-start VM starts
              case 'start':
              case 'stop':
              case 'redeploying': {
                this.replaceVm(msg.result);
                break;
              }

              case 'cdrom-inserted':
              case 'cdrom-ejected': {
                this.$buefy.toast.open({
                  message:
                    'The optical disc for ' +
                    name +
                    ' was successfully ' +
                    (msg.resource.action == 'cdrom-inserted'
                      ? 'inserted.'
                      : 'ejected.'),
                  type: 'is-success',
                  duration: 4000,
                });

                // the event carries no VM, so ask for the one whose disc
                // changed if its details are open; its snapshots and port
                // forwards are unchanged
                if (this.expModal.active && this.expModal.vm.name == name) {
                  this.refreshVmDetails(name);
                }

                break;
              }

              case 'redeployed': {
                this.$buefy.toast.open({
                  message: 'Redeployed ' + name,
                  type: 'is-success',
                });
                this.replaceVm(msg.result);
                this.advanceRedeployQueue(name);

                break;
              }

              case 'errorRedeploying': {
                this.setVmBusy(name, false);
                this.$buefy.toast.open({
                  message: 'Redeploying the ' + name + ' VM failed.',
                  type: 'is-danger',
                  duration: 4000,
                });
                this.advanceRedeployQueue(name);

                break;
              }

              case 'error': {
                this.$buefy.toast.open({
                  message: msg.result.error,
                  type: 'is-danger',
                  duration: 4000,
                });

                break;
              }
            }

            break;
          }

          case 'experiment/vm/commit': {
            const name = this.eventVmName(msg);
            if (name === null) break;

            switch (msg.resource.action) {
              case 'commit': {
                if (!this.findVm(name)) break;
                this.replaceVm({ ...msg.result.vm, busy: false });

                this.$buefy.toast.open({
                  message:
                    'The backing image with name ' +
                    msg.result.disk +
                    ' for the ' +
                    name +
                    ' VM was successfully created.',
                  type: 'is-success',
                  duration: 4000,
                });

                break;
              }

              case 'committing': {
                if (!this.findVm(name)) break;
                this.setVmBusy(name, true);

                this.$buefy.toast.open({
                  message:
                    'A backing image with name ' +
                    msg.result.disk +
                    ' for the ' +
                    name +
                    ' VM is being created.',
                  type: 'is-warning',
                  duration: 4000,
                });

                break;
              }

              case 'progress': {
                this.setVmPercent(name, msg.result.percent);
                break;
              }

              case 'errorCommitting': {
                this.setVmBusy(name, false);
                this.$buefy.toast.open({
                  message:
                    'Creating a backing image for the ' + name + ' VM failed.',
                  type: 'is-danger',
                  duration: 4000,
                });
                break;
              }
            }

            break;
          }

          case 'experiment/vm/memorySnapshot': {
            const name = this.eventVmName(msg);
            if (name === null) break;

            switch (msg.resource.action) {
              case 'commit': {
                if (!this.findVm(name)) break;
                this.setVmBusy(name, false);

                this.$buefy.toast.open({
                  message:
                    'A memory snapshot with name ' +
                    msg.result.disk +
                    ' for the ' +
                    name +
                    ' VM was successfully created.',
                  type: 'is-success',
                  duration: 4000,
                });
                break;
              }

              case 'committing': {
                if (!this.findVm(name)) break;
                this.setVmBusy(name, true);

                this.$buefy.toast.open({
                  message:
                    'A memory snapshot with name ' +
                    msg.result.disk +
                    ' for the ' +
                    name +
                    ' VM is being created.',
                  type: 'is-warning',
                  duration: 4000,
                });
                break;
              }

              case 'progress': {
                this.setVmPercent(name, msg.result.percent);
                break;
              }

              case 'errorCommitting': {
                this.setVmBusy(name, false);
                this.$buefy.toast.open({
                  message:
                    'Creating a memory snapshot for the ' +
                    name +
                    ' VM failed.',
                  type: 'is-danger',
                  duration: 4000,
                });
                break;
              }
            }
            break;
          }

          case 'experiment/vm/screenshot': {
            const name = this.eventVmName(msg);
            if (name === null) break;

            if (msg.resource.action == 'update') {
              // Screenshots arrive for every subscribed VM every few
              // seconds: mutate the row in place instead of replacing the
              // array, which would make b-table re-process every row.
              const row = this.findVm(name);
              if (row) {
                row.screenshot = msg.result.screenshot;
              }
            }
            break;
          }

          case 'experiment/vm/capture': {
            const name = this.eventVmName(msg);
            if (name === null) break;
            const row = this.findVm(name);

            switch (msg.resource.action) {
              case 'start': {
                if (row) {
                  if (row.captures == null) {
                    row.captures = [];
                  }

                  row.captures.push({
                    vm: name,
                    interface: msg.result.interface,
                    filename: msg.result.filename,
                  });
                }

                this.$buefy.toast.open({
                  message:
                    msg.result?.interface !== undefined
                      ? `Packet capture was started on interface ${msg.result.interface} of the ${name} VM.`
                      : `Packet capture was started for the ${name} VM.`,
                  type: 'is-success',
                });

                break;
              }

              case 'stop': {
                if (row) {
                  row.captures = applyStopCaptureUpdate(
                    row.captures,
                    msg.result,
                  );
                }

                this.$buefy.toast.open({
                  message:
                    msg.result && msg.result.interface !== undefined
                      ? 'Packet capture was stopped for interface ' +
                        msg.result.interface +
                        ' on the ' +
                        name +
                        ' VM.'
                      : 'Packet capture was stopped for the ' + name + ' VM.',
                  type: 'is-success',
                });

                break;
              }
            }

            break;
          }

          case 'experiment/vm/snapshot': {
            const name = this.eventVmName(msg);
            if (name === null || !this.findVm(name)) break;

            switch (msg.resource.action) {
              case 'create': {
                this.setVmBusy(name, false);
                this.$buefy.toast.open({
                  message:
                    'The snapshot for the ' +
                    name +
                    ' VM was successfully created.',
                  type: 'is-success',
                  duration: 4000,
                });

                break;
              }

              case 'creating': {
                this.setVmBusy(name, true);
                this.$buefy.toast.open({
                  message:
                    'A snapshot for the ' + name + ' VM is being created.',
                  type: 'is-warning',
                  duration: 4000,
                });

                break;
              }

              case 'progress': {
                this.setVmPercent(name, msg.result.percent);
                break;
              }

              case 'restore': {
                this.setVmBusy(name, false);
                this.$buefy.toast.open({
                  message:
                    'The ' +
                    name +
                    ' VM was successfully reverted to a previous snapshot.',
                  type: 'is-success',
                  duration: 4000,
                });

                break;
              }

              case 'restoring': {
                this.setVmBusy(name, true);
                this.$buefy.toast.open({
                  message:
                    'A snapshot for the ' + name + ' VM is being restored.',
                  type: 'is-warning',
                  duration: 4000,
                });

                break;
              }

              case 'errorCreating':
              case 'errorRestoring': {
                this.setVmBusy(name, false);
                this.$buefy.toast.open({
                  message:
                    (msg.resource.action == 'errorCreating'
                      ? 'Creating a snapshot for the '
                      : 'Restoring a snapshot for the ') +
                    name +
                    ' VM failed.',
                  type: 'is-danger',
                  duration: 4000,
                });

                break;
              }
            }

            break;
          }
        }
      },

      isDelayed() {
        if (this.experiment.vms === undefined) {
          return false;
        }
        for (let i = 0; i < this.experiment.vms.length; i++) {
          if (
            this.experiment.vms[i].delayed_start &&
            this.experiment.vms[i].state == 'BUILDING'
          ) {
            return true;
          }
        }

        return false;
      },

      // the images the redeploy modal offers: those inside the minimega files
      // directory and the ones this experiment names outside it
      updateDisks() {
        this.disks = null;
        this.isWaiting = true;

        axiosInstance
          .get('disks', { params: { expName: this.$route.params.id } })
          .then((resp) => {
            this.isWaiting = false;
            this.disks = resp.data.disks ?? [];
          })
          .catch((err) => {
            this.isWaiting = false;
            useErrorNotification(err);
          });
      },

      assignCategory(value) {
        this.fileCategory = value;
        this.$refs.filesTab?.reload();
      },

      async fetchVMDetails(vm) {
        let url = `experiments/${this.$route.params.id}/vms/${vm.name}`;
        let resp = await axiosInstance.get(url);

        return resp.data;
      },

      async fetchVMSnapshots(vm) {
        let url = `experiments/${this.$route.params.id}/vms/${vm.name}/snapshots`;
        let resp = await axiosInstance.get(url);

        return resp.data;
      },

      async fetchVMForwards(vm) {
        let url = `experiments/${this.$route.params.id}/vms/${vm.name}/forwards`;
        let resp = await axiosInstance.get(url);

        return resp.data;
      },

      // fetches a VM's details into its open details modal and its table row
      async refreshVmDetails(name) {
        try {
          const details = await this.fetchVMDetails({ name });
          this.replaceVm(details);
          if (this.expModal.active && this.expModal.vm.name == name) {
            this.expModal.vm = details;
          }
        } catch (err) {
          useErrorNotification(err);
        }
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
            confirmText: 'Ok',
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
          const resp = await axiosInstance.patch(
            `experiments/${this.$route.params.id}/vms/${name}`,
            { annotations },
          );
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

      captureSnapshot(name) {
        name = this.actionableVMs('snapshot', name);
        if (name.length === 0) return;

        const t = timestampParts();
        const time = `${t.year}-${t.month}-${t.day}_${t.hours}${t.minutes}`;

        this.$buefy.dialog.confirm({
          title: pluralWord(
            name.length,
            'Create a VM Snapshot',
            'Create VM Snapshots',
          ),
          message: `This will create ${pluralWord(name.length, 'a snapshot', 'snapshots')} of ${namedList(name, 'VM')}.`,
          cancelText: 'Cancel',
          confirmText: 'Create',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.resetExpModal();
            name.forEach((vmName) => {
              axiosInstance
                .post(
                  'experiments/' +
                    this.$route.params.id +
                    '/vms/' +
                    vmName +
                    '/snapshots',
                  { filename: time },
                  { timeout: 0 },
                )
                .catch((err) => {
                  useErrorNotification(err);
                });
            });
          },
        });
      },

      restoreSnapshot(name, snapshot) {
        this.$buefy.dialog.confirm({
          title: 'Restore a VM Snapshot',
          message: 'This will revert the ' + name + ' VM to ' + snapshot + '.',
          cancelText: 'Cancel',
          confirmText: 'Revert',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.resetExpModal();

            axiosInstance
              .post(
                'experiments/' +
                  this.$route.params.id +
                  '/vms/' +
                  name +
                  '/snapshots/' +
                  snapshot,
                {},
                { timeout: 0 },
              )
              .catch((err) => {
                useErrorNotification(err);
              });
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
          onConfirm: () => {
            this.diskImageModal.active = false;
            this.resetDiskImageModal();
            this.resetExpModal();
            let url = '';
            let body = '';
            vm.forEach((arg) => {
              url =
                'experiments/' +
                this.$route.params.id +
                '/vms/' +
                arg.name +
                '/commit';
              body = { filename: arg.filename + '.qc2' };

              axiosInstance.post(url, body, { timeout: 0 }).catch((err) => {
                useErrorNotification(err);
              });
            });
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
        this.expModal.active = false;
        this.memorySnapshotModal.active = true;
      },

      createMemorySnapshot(vm) {
        this.memorySnapshotModal.active = false;
        let url = '';
        let body = '';
        vm.forEach((arg) => {
          url =
            'experiments/' +
            this.$route.params.id +
            '/vms/' +
            arg.name +
            '/memorySnapshot';
          body = { filename: arg.filename + '.elf' };

          axiosInstance.post(url, body, { timeout: 0 }).catch((err) => {
            useErrorNotification(err);
          });

          this.resetMemorySnapshotModal();
        });
      },

      killVm(names) {
        this.confirmVmAction(
          'kill',
          names,
          (vms) => ({
            title: `Kill the ${pluralWord(vms.length, 'VM')}`,
            message:
              `This will kill ${namedList(vms, 'VM')}. You will not be able ` +
              `to restore ${pluralWord(vms.length, 'it', 'them')} until you ` +
              `restart the ${this.$route.params.id} experiment!`,
            confirmText: pluralWord(vms.length, 'KILL IT!', 'KILL THEM!'),
            type: 'is-danger',
          }),
          (url, name) =>
            axiosInstance.delete(url).then(() => {
              this.experiment.vms = this.experiment.vms.filter(
                (vm) => vm.name != name,
              );
            }),
        );
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
          onConfirm: () => {
            this.isWaiting = true;

            axiosInstance
              .post('experiments/' + this.$route.params.id + '/stop')
              .then(() => {
                this.$router.replace('/experiments/');
              })
              .catch((err) => {
                useErrorNotification(err);
                this.isWaiting = false;
              });
          },
        });
      },

      decorator(state, busy) {
        return vmStateType({ state, busy });
      },

      // runs a VM action from the details modal or the table's Actions column
      onVmAction(action, vm) {
        switch (action) {
          case 'start':
            this.startVm(vm.name);
            break;
          case 'pause':
            this.pauseVm(vm.name);
            break;
          case 'restart':
            this.restartVm(vm.name);
            break;
          case 'shutdown':
            this.shutdownVm(vm.name);
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

      tapDecorator(captures, iface) {
        return isInterfaceCapturing(captures, iface) ? 'is-success' : undefined;
      },

      // stopVMCapture issues the DELETE request to stop packet capture(s) for
      // a VM. If iface is provided, only that interface's capture is
      // stopped; otherwise all captures for the VM are stopped.
      stopVMCapture(vm, iface) {
        this.isWaiting = true;

        const params = iface === undefined ? {} : { iface: iface };

        axiosInstance
          .delete(
            'experiments/' +
              this.$route.params.id +
              '/vms/' +
              vm.name +
              '/captures',
            { params: params },
          )
          .then(() => {
            // the row updates from the capture's websocket event
            this.isWaiting = false;
          })
          .catch((err) => {
            useErrorNotification(err);
            this.isWaiting = false;
          });
      },

      // "vm/interface", the key of a capture button waiting on the server
      captureKey(vmName, iface) {
        return `${vmName}/${iface}`;
      },

      isCapturePending(vmName, iface) {
        return !!this.capturePending[this.captureKey(vmName, iface)];
      },

      // the interfaces of a VM whose capture buttons are waiting
      pendingCaptureIfaces(vmName) {
        const prefix = `${vmName}/`;
        return Object.keys(this.capturePending)
          .filter((key) => key.startsWith(prefix))
          .map((key) => Number(key.slice(prefix.length)));
      },

      setCapturePending(vmName, iface, pending) {
        const key = this.captureKey(vmName, iface);
        if (pending) {
          this.capturePending = { ...this.capturePending, [key]: true };
        } else {
          const { [key]: _, ...rest } = this.capturePending;
          this.capturePending = rest;
        }
      },

      // The capture button of an interface: asks the server which captures
      // are running (minimega lists them across the cluster, which takes a
      // moment), then offers to start or stop this interface's. The button
      // shows a spinner until the dialog opens.
      handlePcap(vm, iface) {
        if (this.isCapturePending(vm.name, iface)) return;

        const t = timestampParts();
        const time = `${t.year}-${t.month}-${t.day}_${t.hours}${t.minutes}`;

        this.setCapturePending(vm.name, iface, true);
        axiosInstance
          .get(
            'experiments/' +
              this.$route.params.id +
              '/vms/' +
              vm.name +
              '/captures',
          )
          .finally(() => this.setCapturePending(vm.name, iface, false))
          .then((response) => {
            let captures = response.data.captures || [];
            let capturing = isInterfaceCapturing(captures, iface);

            if (capturing && hasMultipleCaptures(captures)) {
              // More than one capture is currently running for this VM;
              // let the user choose between stopping just this interface's
              // capture or all of the VM's running captures.
              this.$buefy.dialog.confirm({
                title: 'Stop Packet Capture',
                message:
                  'The ' +
                  vm.name +
                  ' VM has multiple packet captures running. Stop the ' +
                  'capture on interface ' +
                  iface +
                  ' only, or stop all packet captures for the VM?',
                cancelText: 'Stop Interface ' + iface + ' Only',
                confirmText: 'Stop All Captures',
                type: 'is-danger',
                hasIcon: true,
                onConfirm: () => {
                  this.stopVMCapture(vm);
                },
                onCancel: (trigger) => {
                  // Only the "Stop Interface <iface> Only" button (as
                  // opposed to dismissing the dialog via escape or clicking
                  // outside of it, either of which should be a no-op) stops
                  // this interface's capture.
                  if (trigger === 'button') {
                    this.stopVMCapture(vm, iface);
                  }
                },
              });
            } else if (capturing) {
              this.$buefy.dialog.confirm({
                title: 'Stop Packet Capture',
                message:
                  'This will stop the packet capture on interface ' +
                  iface +
                  ' for the ' +
                  vm.name +
                  ' VM.',
                cancelText: 'Cancel',
                confirmText: 'Stop',
                type: 'is-danger',
                hasIcon: true,
                onConfirm: () => {
                  this.stopVMCapture(vm, iface);
                },
              });
            } else if (vm.networks[iface] == 'disconnected') {
              this.$buefy.toast.open({
                message: 'Cannot capture traffic on a disconnected interface.',
                type: 'is-danger',
                duration: 4000,
              });
            } else {
              this.$buefy.dialog.confirm({
                title: 'Start a Packet Capture',
                message:
                  'This will start a packet capture for the ' +
                  vm.name +
                  ' VM, interface ' +
                  iface +
                  '.',
                cancelText: 'Cancel',
                confirmText: 'Start',
                type: 'is-success',
                hasIcon: true,
                onConfirm: () => {
                  this.isWaiting = true;

                  axiosInstance
                    .post(
                      'experiments/' +
                        this.$route.params.id +
                        '/vms/' +
                        vm.name +
                        '/captures',
                      {
                        interface: iface,
                        filename: captureFilename(vm.name, iface, time),
                      },
                    )
                    .then(() => {
                      this.isWaiting = false;
                    })
                    .catch((err) => {
                      useErrorNotification(err);
                      this.isWaiting = false;
                    });
                },
              });
            }
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },

      // Starts a packet capture on every connected interface of a VM that
      // has none running, as the capture button does for one: the files are
      // named <vm>_<interface>_<time>.pcap. The Actions column's button shows
      // a spinner while the server is asked and while the captures start.
      async captureAllInterfaces(vm) {
        if (this.captureAllPending[vm.name]) return;

        const t = timestampParts();
        const time = `${t.year}-${t.month}-${t.day}_${t.hours}${t.minutes}`;
        const url = `experiments/${this.$route.params.id}/vms/${vm.name}/captures`;
        const setPending = (pending) => {
          const { [vm.name]: _, ...rest } = this.captureAllPending;
          this.captureAllPending = pending
            ? { ...rest, [vm.name]: true }
            : rest;
        };

        setPending(true);
        let targets;
        try {
          const resp = await axiosInstance.get(url);
          targets = captureTargets(vm, resp.data?.captures ?? []);
        } catch (err) {
          useErrorNotification(err);
          return;
        } finally {
          setPending(false);
        }

        if (targets.length === 0) {
          this.$buefy.toast.open({
            message: `Every connected interface of the ${vm.name} VM is already being captured.`,
            type: 'is-info',
            duration: 4000,
          });
          return;
        }

        const files = targets.map((iface) =>
          captureFilename(vm.name, iface, time),
        );
        this.$buefy.dialog.confirm({
          title: `Start ${pluralWord(targets.length, 'a Packet Capture', 'Packet Captures')}`,
          message:
            `This will start ${pluralWord(targets.length, 'a packet capture', 'packet captures')} ` +
            `on ${pluralWord(targets.length, 'interface')} ${listText(targets)} ` +
            `of the ${vm.name} VM, writing ${listText(files)} to the ` +
            "experiment's files.",
          cancelText: 'Cancel',
          confirmText: targets.length == 1 ? 'Start' : 'Start All',
          type: 'is-success',
          hasIcon: true,
          onConfirm: async () => {
            setPending(true);
            try {
              const results = await Promise.allSettled(
                targets.map((iface, i) =>
                  axiosInstance.post(url, {
                    interface: iface,
                    filename: files[i],
                  }),
                ),
              );
              // each failure has its own notification; the rows update from
              // the captures' websocket events
              results
                .filter((result) => result.status === 'rejected')
                .forEach((result) => useErrorNotification(result.reason));
            } finally {
              setPending(false);
            }
          },
        });
      },

      // Stops every packet capture running on a VM, from the Actions
      // column's stop button; it spins until the server answers.
      stopAllCaptures(vm) {
        if (this.captureAllPending[vm.name]) return;

        const ifaces = [
          ...new Set((vm.captures ?? []).map((c) => c.interface)),
        ];
        this.$buefy.dialog.confirm({
          title: `Stop ${pluralWord(ifaces.length, 'Packet Capture', 'Packet Captures')}`,
          message:
            `This will stop the packet ${pluralWord(ifaces.length, 'capture')} ` +
            `on ${pluralWord(ifaces.length, 'interface')} ${listText(ifaces)} ` +
            `of the ${vm.name} VM.`,
          cancelText: 'Cancel',
          confirmText: ifaces.length == 1 ? 'Stop' : 'Stop All',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: async () => {
            this.captureAllPending = {
              ...this.captureAllPending,
              [vm.name]: true,
            };
            try {
              // the row updates from the captures' websocket events
              await axiosInstance.delete(
                `experiments/${this.$route.params.id}/vms/${vm.name}/captures`,
              );
            } catch (err) {
              useErrorNotification(err);
            } finally {
              const { [vm.name]: _, ...rest } = this.captureAllPending;
              this.captureAllPending = rest;
            }
          },
        });
      },

      startVm(names) {
        this.confirmVmAction(
          'start',
          names,
          (vms) => ({
            title: `Start the ${pluralWord(vms.length, 'VM')}`,
            message: `This will start ${namedList(vms, 'VM')}.`,
            confirmText: 'Start',
            type: 'is-success',
          }),
          (url) =>
            axiosInstance
              .post(`${url}/start`)
              .then((resp) => this.replaceVm(resp.data)),
        );
      },

      pauseVm(names) {
        this.confirmVmAction(
          'pause',
          names,
          (vms) => ({
            title: `Pause the ${pluralWord(vms.length, 'VM')}`,
            message: `This will pause ${namedList(vms, 'VM')}.`,
            confirmText: 'Pause',
            type: 'is-success',
          }),
          (url) =>
            axiosInstance
              .post(`${url}/stop`)
              .then((resp) => this.replaceVm(resp.data)),
        );
      },

      resetVmState(names) {
        this.confirmVmAction(
          'resetDisk',
          names,
          (vms) => ({
            title: `Reset the ${pluralWord(vms.length, "VM's Disk", "VMs' Disks")}`,
            message: `This will reset the disk state of ${namedList(vms, 'VM')}.`,
            confirmText: 'Reset',
            type: 'is-success',
          }),
          (url) =>
            axiosInstance
              .get(`${url}/reset`)
              .then((resp) => this.replaceVm(resp.data)),
        );
      },

      restartVm(names) {
        this.confirmVmAction(
          'restart',
          names,
          (vms) => ({
            title: `Restart the ${pluralWord(vms.length, 'VM')}`,
            message: `This will restart ${namedList(vms, 'VM')}.`,
            confirmText: 'Restart',
            type: 'is-success',
          }),
          (url) =>
            axiosInstance
              .get(`${url}/restart`)
              .then((resp) => this.replaceVm(resp.data)),
        );
      },

      shutdownVm(names) {
        this.confirmVmAction(
          'shutdown',
          names,
          (vms) => ({
            title: `Shut Down the ${pluralWord(vms.length, 'VM')}`,
            message: `This will shut down ${namedList(vms, 'VM')}.`,
            confirmText: 'Shut Down',
            type: 'is-danger',
          }),
          (url) =>
            axiosInstance
              .get(`${url}/shutdown`)
              .then((resp) => this.replaceVm(resp.data)),
        );
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
          const vm = this.findVm(name);
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

      closeModal(modalName) {
        this.$refs[modalName].cancel('x');
      },

      redeployVm(vms) {
        let body = '';
        let postUrl = '';

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
          postUrl =
            'experiments/' +
            this.$route.params.id +
            '/vms/' +
            vm.name +
            '/redeploy';

          // the select stores the string 'true' or 'false'
          if (vm.inject === true || vm.inject === 'true') {
            body['injects'] = true;
          }

          this.redeployModal.actionsQueue.push({
            name: vm.name,
            url: postUrl,
            body: body,
          });
        });

        axiosInstance.post(postUrl, body).catch((err) => {
          useErrorNotification(err);
          this.isWaiting = false;
          this.resetExpModal();
        });
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
          onConfirm: () => {
            this.isWaiting = true;
            axiosInstance
              .patch(`experiments/${this.$route.params.id}/vms/${name}`, {
                interface: { index, vlan: disconnect ? '' : vlan.alias },
              })
              .then((resp) => this.replaceVm(resp.data))
              .catch((err) => useErrorNotification(err))
              .finally(() => (this.isWaiting = false));
          },
        });

        this.vlanModal.active = false;
      },

      // the CD-ROM request for the VM the optical disc modal is open for
      opticalDiscUrl() {
        return `experiments/${this.$route.params.id}/vms/${this.opticalDiscModal.vmName}/cdrom`;
      },

      insertOpticalDisc() {
        const isoPath = this.opticalDiscModal.disc;
        if (!isoPath) return;

        axiosInstance
          .post(this.opticalDiscUrl(), null, { params: { isoPath } })
          .catch((err) => useErrorNotification(err));
        this.resetOpticalDiscModal();
      },

      ejectOpticalDisc() {
        axiosInstance
          .delete(this.opticalDiscUrl())
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

      getApps() {
        let defaultApps = ['ntp', 'serial', 'startup', 'vrouter', 'scorch'];

        this.appsModal.triggerable = this.experiment.apps.filter(
          (a) => !defaultApps.includes(a),
        );
        this.appsModal.active = true;
      },

      startApps(apps) {
        this.triggeredApps.push(...apps);

        axiosInstance
          .post('experiments/' + this.$route.params.id + '/trigger', null, {
            params: { apps: apps.join() },
          })
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
            "Your role can't list disk images, so it can't pick an ISO " +
            'image to insert.';
          return;
        }

        modal.loading = true;
        let isos = [];
        let error = null;
        try {
          const resp = await axiosInstance.get('disks', {
            params: { diskType: 'ISO' },
          });
          isos = isoDisks(resp.data?.disks);
        } catch (err) {
          error = "The ISO images couldn't be listed.";
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

      // whether the role may take the action on every selected VM
      selectionAllows(action) {
        return this.vmSelectedArray.every((vm) =>
          actionPermitted(action, (resource, verb) =>
            roleAllowed(resource, verb, `${this.experiment.name}/${vm}`),
          ),
        );
      },

      // runs a toolbar action on the selected VMs, then clears the selection
      actOnSelection(action) {
        action(this.vmSelectedArray);
        this.vmSelectedArray = [];
        this.checkAll = false;
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
        if (skipped.length > 0) {
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: escapeHTML(skippedVMsText(skipped)),
            confirmText: 'Ok',
          });
        }
        return allowed;
      },

      // Asks to take an action on the named VMs its rules allow, saying which
      // it skips and why, then sends each VM's request: `ask(vms)` words the
      // question for the VMs it acts on, and `send(url, name)` makes one VM's
      // request, given the VM's URL.
      confirmVmAction(action, names, ask, send) {
        const vmList = this.actionableVMs(action, names);
        if (vmList.length === 0) return;

        this.$buefy.dialog.confirm({
          ...ask(vmList),
          cancelText: 'Cancel',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            this.resetExpModal();
            for (const name of vmList) {
              send(`experiments/${this.$route.params.id}/vms/${name}`, name)
                .catch((err) => useErrorNotification(err))
                .finally(() => (this.isWaiting = false));
            }
          },
        });
      },

      updateCaptureLabel(vm) {
        return vm.captures.length == 0
          ? 'start packet capture'
          : 'stop packet capture';
      },

      addSearchHistory() {
        if (this.searchHistory.length >= this.searchHistoryLength) {
          this.searchHistory.pop();
        }
        this.searchHistory.push(this.search.filter.trim());
        this.searchHistory = this.getUniqueItems(this.searchHistory);
      },

      showMountDialog(vm) {
        this.resetExpModal();

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
        let url = `experiments/${this.$route.params.id}/vms/${this.portForwardModal.vmName}/forwards`;
        const params = {
          src: this.portForwardModal.srcPort,
          host: this.portForwardModal.dstHost,
          dst: this.portForwardModal.dstPort,
        };

        try {
          await axiosInstance.post(url, null, { params });

          this.$buefy.toast.open({
            message: `Port forward ${params.src} → ${params.host}:${params.dst} created for the ${this.portForwardModal.vmName} VM.`,
            type: 'is-success',
            duration: 4000,
          });

          if (
            !this.roleAllowed('vms/forwards', 'list', this.expModal.fullName)
          ) {
            return;
          }

          let resp = await axiosInstance.get(url);

          this.showForwards(resp.data.listeners);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.resetPortForwardModal();
        }
      },

      async deletePortForward(vm, forward) {
        let url = `experiments/${this.$route.params.id}/vms/${vm}/forwards`;
        const params = { host: forward.dstHost, dst: forward.dstPort };

        try {
          await axiosInstance.delete(url, { params });

          let resp = await axiosInstance.get(url);

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

      async handleNetflow(start, create = true) {
        if (this.netflow.starting || this.netflow.stopping) return;

        if (!start) {
          this.stopNetflow();
          return;
        }

        // the websocket upgrade 404s until the backend registers the
        // capture, so wait for the POST to finish before connecting
        if (create) {
          this.netflow.starting = true;
          try {
            await axiosInstance.post(
              `experiments/${this.$route.params.id}/netflow`,
            );
          } catch (err) {
            this.netflow.starting = false;
            useErrorNotification(err);
            return;
          }
        }

        this.netflow.capturing = true;
        this.netflow.tooltip = 'Stop Netflow Capture';
        this.addNetflowLines([netflowMarker('Capture started')]);

        let path = `${import.meta.env.BASE_URL}api/v1/experiments/${this.$route.params.id}/netflow/ws`;

        let token = usePhenixStore().token;
        if (token) {
          path += `?token=${token}`;
        }

        let proto = location.protocol == 'https:' ? 'wss://' : 'ws://';
        let url = proto + location.host + path;

        this.socket = new WebSocket(url);
        const started = () => (this.netflow.starting = false);
        this.socket.addEventListener('open', started);
        this.socket.addEventListener('error', started);
        this.socket.addEventListener('message', (event) => {
          // one update per frame, however many flows it carries
          const flows = event.data
            .split(/\r?\n/)
            .filter(Boolean)
            .map((data) => netflowLine(JSON.parse(data)));
          this.addNetflowLines(flows);
        });
      },

      async stopNetflow() {
        this.netflow.stopping = true;
        try {
          await axiosInstance.delete(
            `experiments/${this.$route.params.id}/netflow`,
          );
          this.addNetflowLines([netflowMarker('Capture stopped')]);
        } catch (err) {
          if (err.response?.status !== 404) {
            useErrorNotification(err);
            return;
          }
          // the server has no capture to stop: it already stopped (from
          // another window, or with the experiment), or phenix restarted
          this.addNetflowLines([
            netflowMarker('Capture had already stopped on the server'),
          ]);
          this.$buefy.toast.open({
            message:
              'The netflow capture had already stopped (from another window, by the experiment stopping, or by a phenix restart).',
            type: 'is-info',
            duration: 6000,
          });
        } finally {
          this.netflow.stopping = false;
        }

        this.netflow.capturing = false;
        this.netflow.tooltip = 'Start Netflow Capture';
        if (this.socket) {
          this.socket.close();
          this.socket = null;
        }
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

      // Sizes the netflow table to the room left in the window, so the table
      // scrolls instead of the page and the footer stays on screen.
      fitNetflowView() {
        const room = roomInWindow(this.$refs.netflowView);
        if (room == null) return;
        this.netflowMaxHeight = `${Math.max(Math.floor(room), 100)}px`;
      },

      // keeps the most recent NETFLOW_MAX_LINES lines
      addNetflowLines(lines) {
        if (lines.length === 0) return;
        const all = this.netflow.lines;
        // the table appears with the first line
        if (all.length === 0) this.$nextTick(this.fitNetflowView);
        all.push(...lines);
        if (all.length > NETFLOW_MAX_LINES) {
          all.splice(0, all.length - NETFLOW_MAX_LINES);
        }
      },
    },

    watch: {
      checkAll(newVal) {
        if (newVal) {
          var visibleItems = this.$refs['vmTable'].visibleData;
          // add all visible items
          this.vmSelectedArray = [];
          for (var i = 0; i < visibleItems.length; i++) {
            let item = visibleItems[i];

            if (!item.external) {
              this.vmSelectedArray.push(item.name);
            }
          }
        } else {
          this.vmSelectedArray = [];
        }
      },
      activeTab(newVal) {
        // Clear search history and
        // search filter when switching tabs
        this.searchHistory = [];
        this.search.filter = '';

        if (newVal == 0 || newVal == 2) {
          this.searchPlaceholder = 'Find a VM';
          if (this.experiment.name) this.updateTable();
        } else if (newVal == NETFLOW_TAB) {
          this.searchPlaceholder = 'Search netflow';
          this.$nextTick(this.fitNetflowView);
        } else {
          this.searchPlaceholder = 'Find a File';
          this.$refs.filesTab?.reload();
        }
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
          filter: '',
        },
        // whether the websocket has sent the VM table yet
        vmsLoaded: false,
        // whether the page has asked the websocket for the VM table
        vmsRequested: false,
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
        // "vm/interface" keys of the capture buttons waiting on the server
        capturePending: {},
        // names of the VMs starting or stopping captures on all their
        // interfaces
        captureAllPending: {},
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
        experiment: [],
        // the Files tab's category picker, and the categories it offers
        fileCategory: null,
        fileCategories: [],
        // the redeploy modal's disk list, null until it has loaded
        disks: null,
        vlan: null,
        // shows the loading overlay while the page waits on the server
        isWaiting: false,
        showModifyStateBar: false,
        checkAll: false,
        vmSelectedArray: [],
        searchHistory: [],
        searchHistoryLength: 10,
        searchPlaceholder: 'Find a VM',
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
        activeTab: 0,
        vncZoom: 4,
        // sized to the window so the page never scrolls; null until measured
        netflowMaxHeight: null,
        netflow: {
          tooltip: 'Start Netflow Capture',
          capturing: false,
          starting: false,
          stopping: false,
          // captured flows and start/stop markers, oldest first
          lines: [],
        },
      };
    },
  };
</script>

<style scoped>
  /* a small spinner beside an IP or tap whose capture request is waiting,
     and in the CD-ROM picker while it lists ISO images */
  .capture-spinner {
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    margin-left: 0.35em;
    vertical-align: -0.1em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: capture-spin 0.6s linear infinite;
  }

  @keyframes capture-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .optical-disc-card {
    width: 32rem;
  }

  .optical-disc-current {
    margin-bottom: 0.75rem;
  }

  .optical-disc-state {
    margin: 0;
  }

  .optical-disc-loading {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .optical-disc-loading .capture-spinner {
    margin-left: 0;
  }

  .fa-layers-counter {
    /* counter on tag icon */
    transform: scale(0.7) translateX(50%) translateY(-50%);
  }
  :deep(.tabs ul) {
    margin-left: 0px !important;
  }

  :deep(.b-tabs .tab-content) {
    padding: 1rem 0 0 0;
  }

  /* scrolls within the window, so the page below stays reachable */
  .netflow-view {
    max-height: calc(100vh - 18rem);
    min-height: 100px;
    overflow: auto;
  }

  .netflow-table td {
    font-family: monospace;
    white-space: nowrap;
  }

  .netflow-table thead th {
    position: sticky;
    top: 0;
    z-index: 1;
  }

  .netflow-marker td {
    font-weight: bold;
    font-style: italic;
  }

  .netflow-summary {
    margin-bottom: 0.5rem;
    opacity: 0.8;
  }

  .vnc-tile-name {
    color: whitesmoke;
    display: block;
    background-color: grey;
    text-align: center;
    padding: 2px 0px;
  }
</style>
