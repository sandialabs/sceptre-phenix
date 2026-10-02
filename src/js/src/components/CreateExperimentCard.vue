<!-- The Experiments page's card for creating an experiment. It emits `create`
with the request body; the page sends it. -->
<template>
  <div class="modal-card create-exp-card">
    <header class="modal-card-head">
      <p class="modal-card-title">Create a New Experiment</p>
      <b-tooltip
        label="Documentation for creating experiments"
        type="is-dark"
        position="is-left"
        :delay="500">
        <a
          class="button is-light is-small create-exp-docs"
          aria-label="Documentation for creating experiments"
          :href="docsUrl"
          target="_blank"
          rel="noopener">
          <b-icon icon="book" />
        </a>
      </b-tooltip>
    </header>
    <section class="modal-card-body">
      <div class="field">
        <field-label
          id="create-exp-name"
          label="Experiment Name"
          :help="nameHelp" />
        <b-field :type="nameError ? 'is-danger' : null" :message="nameError">
          <b-input
            type="text"
            v-model="name"
            aria-labelledby="create-exp-name"
            v-focus />
        </b-field>
      </div>
      <div class="field">
        <field-label
          id="create-exp-topology"
          label="Experiment Topology"
          help="The topology the experiment is built from: its VMs, their networks and VLANs. Upload or create topologies on the Configs page." />
        <b-select
          v-model="topology"
          :placeholder="
            topologiesLoaded ? 'Select a topology' : 'Loading topologies…'
          "
          :loading="!topologiesLoaded"
          aria-labelledby="create-exp-topology"
          expanded>
          <option v-for="t in topologies" :key="t" :value="t">
            {{ t }}
          </option>
        </b-select>
      </div>
      <div class="field">
        <field-label
          id="create-exp-scenario"
          label="Experiment Scenario"
          help="Optional. A scenario is a collection of app configurations for a topology. Its apps are listed below once one is chosen; click an app to leave it out of this experiment." />
        <b-select
          v-model="scenario"
          :placeholder="scenarioPlaceholder"
          :loading="scenariosLoading"
          :disabled="!scenarioNames.length"
          aria-labelledby="create-exp-scenario"
          expanded>
          <template v-if="scenarioNames.length">
            <option :value="null">None</option>
            <option v-for="s in scenarioNames" :key="s" :value="s">
              {{ s }}
            </option>
          </template>
        </b-select>
      </div>
      <b-taglist v-if="scenarioApps.length" class="create-exp-apps">
        <b-tag
          v-for="(app, index) in scenarioApps"
          :key="app.name"
          class="is-clickable"
          :class="{ 'is-success': !app.disabled }"
          role="button"
          tabindex="0"
          :aria-pressed="String(!app.disabled)"
          :title="
            app.disabled ? 'Left out: click to include' : 'Click to leave out'
          "
          @click="toggleApp(index)"
          @keydown.enter.space.prevent="toggleApp(index)">
          {{ app.name }}
        </b-tag>
      </b-taglist>
      <b-collapse
        class="card create-exp-advanced"
        animation="slide"
        aria-id="create-exp-advanced"
        :model-value="false">
        <template #trigger="props">
          <button
            type="button"
            class="card-header create-exp-advanced-toggle"
            aria-controls="create-exp-advanced"
            :aria-expanded="String(props.open)">
            <span class="card-header-title">Advanced Options</span>
            <span class="card-header-icon">
              <b-icon
                size="is-small"
                :icon="props.open ? 'chevron-up' : 'chevron-down'" />
            </span>
          </button>
        </template>
        <div class="card-content">
          <div class="field">
            <field-label
              id="create-exp-deploy-mode"
              label="Deployment Mode"
              :help="deployModeHelp" />
            <b-select
              v-model="deployMode"
              aria-labelledby="create-exp-deploy-mode"
              expanded>
              <option value="">{{ serverDefaultDeployMode }}</option>
              <option v-for="mode in deployModes" :key="mode" :value="mode">
                {{ mode }}
              </option>
            </b-select>
          </div>
          <div v-if="bridgeMode != 'auto'" class="field">
            <field-label
              id="create-exp-bridge"
              label="Default Bridge Name"
              help="The Open vSwitch bridge for every VM interface that does not name its own, 15 characters at most. Leave it empty to share the default phenix bridge with other experiments." />
            <b-field
              :type="bridgeError ? 'is-danger' : null"
              :message="bridgeError">
              <b-input
                type="text"
                v-model="bridge"
                placeholder="phenix"
                aria-labelledby="create-exp-bridge" />
            </b-field>
          </div>
          <div class="field">
            <field-label
              id="create-exp-vlans"
              label="VLAN Range"
              help="The VLAN IDs, from 0 to 4094, minimega may give this experiment's VLANs. Leave both empty for minimega's default range." />
            <b-field
              class="create-exp-vlans"
              grouped
              :type="vlanError ? 'is-danger' : null"
              :message="vlanError">
              <b-numberinput
                min="0"
                max="4094"
                type="is-light"
                size="is-small"
                controls-alignment="right"
                controls-position="compact"
                placeholder="min"
                aria-label="VLAN range minimum"
                aria-minus-label="Decrease VLAN range minimum"
                aria-plus-label="Increase VLAN range minimum"
                v-model="vlanMin" />
              <b-numberinput
                min="0"
                max="4094"
                type="is-light"
                size="is-small"
                controls-alignment="right"
                controls-position="compact"
                placeholder="max"
                aria-label="VLAN range maximum"
                aria-minus-label="Decrease VLAN range maximum"
                aria-plus-label="Increase VLAN range maximum"
                v-model="vlanMax" />
            </b-field>
          </div>
          <div class="field">
            <field-label
              id="create-exp-branch"
              label="Git Workflow Branch Name"
              help="Links the experiment to a Git workflow branch: applying that branch's workflow config updates and restarts this experiment. Stored as the phenix.workflow/branch annotation." />
            <b-input
              type="text"
              v-model="branch"
              aria-labelledby="create-exp-branch" />
          </div>
          <annotation-list
            v-model="nodeAnnotations"
            id-prefix="create-exp-node-annotations"
            label="Annotations for Every VM"
            help="Added to every VM in this experiment's copy of the topology, where apps read them; the topology itself is unchanged, and a VM that has an annotation already keeps its own value."
            :catalog="nodeCatalog"
            :custom="customNode" />
          <annotation-list
            v-model="annotations"
            id-prefix="create-exp-annotations"
            label="Experiment Annotations"
            help="Stored in the experiment's metadata annotations, where apps can read them. phēnix sets topology, scenario and the workflow branch itself."
            :catalog="experimentCatalog"
            :custom="customExperiment"
            :managed="managedAnnotations" />
        </div>
      </b-collapse>
    </section>
    <footer class="modal-card-foot buttons is-right">
      <button class="button is-light" :disabled="!valid" @click="create">
        Create Experiment
      </button>
    </footer>
  </div>
</template>

<script>
  import AnnotationList from '@/components/AnnotationList.vue';
  import FieldLabel from '@/components/FieldLabel.vue';
  import axiosInstance from '@/utils/axios.js';
  import { docsPage } from '@/utils/docs.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import {
    CUSTOM_EXPERIMENT_ANNOTATION,
    CUSTOM_NODE_ANNOTATION,
    EXPERIMENT_ANNOTATIONS,
    MANAGED_EXPERIMENT_ANNOTATIONS,
    NODE_ANNOTATIONS,
    annotationErrors,
    experimentAnnotationsPayload,
    nodeAnnotationsPayload,
  } from '@/utils/experimentAnnotations.js';

  // names an experiment can't take: the server refuses `all` and the
  // minimega namespaces, and the page also refuses `create`
  const RESERVED_NAMES = ['all', 'create', 'minimega', '__phenix__'];

  // bridge names are Open vSwitch interface names
  const MAX_BRIDGE_NAME = 15;

  export default {
    name: 'CreateExperimentCard',
    components: { AnnotationList, FieldLabel },

    directives: {
      focus: {
        mounted(el) {
          (el.tagName == 'INPUT' ? el : el.querySelector('input')).focus();
        },
      },
    },

    props: {
      // names of the experiments that exist, which a new one may not reuse
      experimentNames: { type: Array, default: () => [] },
      // the server's /options
      options: { type: Object, default: () => ({}) },
    },

    emits: ['create'],

    data() {
      return {
        name: '',
        topologies: [],
        topologiesLoaded: false,
        topology: null,
        // scenario name to its apps, for the chosen topology
        scenarios: {},
        scenariosLoading: false,
        scenario: null,
        deployMode: '',
        bridge: '',
        vlanMin: null,
        vlanMax: null,
        branch: '',
        nodeAnnotations: [],
        annotations: [],
      };
    },

    created() {
      axiosInstance
        .get('topologies')
        .then(({ data }) => {
          this.topologies = data.topologies ?? [];
        })
        .catch((err) => useErrorNotification(err))
        .finally(() => {
          this.topologiesLoaded = true;
        });
    },

    computed: {
      docsUrl() {
        return `${docsPage('experiments')}#create-a-new-experiment`;
      },

      bridgeMode() {
        return this.options['bridge-mode'];
      },

      serverDefaultDeployMode() {
        const mode = this.options['deploy-mode'];
        return mode ? `server default (${mode})` : 'server default';
      },

      deployModes: () => ['no-headnode', 'only-headnode', 'all'],

      nodeCatalog: () => NODE_ANNOTATIONS,
      experimentCatalog: () => EXPERIMENT_ANNOTATIONS,
      customNode: () => CUSTOM_NODE_ANNOTATION,
      customExperiment: () => CUSTOM_EXPERIMENT_ANNOTATION,
      managedAnnotations: () => MANAGED_EXPERIMENT_ANNOTATIONS,

      nameHelp() {
        const help =
          'A unique name for the experiment, without spaces. Its VMs run in the minimega namespace of the same name.';
        return this.bridgeMode == 'auto'
          ? `${help} In auto bridge mode it also names the experiment's bridge, so it can be at most ${MAX_BRIDGE_NAME} characters.`
          : help;
      },

      deployModeHelp() {
        return (
          "Which cluster hosts run the experiment's VMs. no-headnode: every " +
          'host but the headnode (the headnode alone when it has no peers). ' +
          'only-headnode: just the headnode. all: the headnode and every ' +
          "host. The server default is phēnix's deploy-mode setting."
        );
      },

      scenarioNames() {
        return Object.keys(this.scenarios);
      },

      scenarioPlaceholder() {
        if (!this.topology) return 'Select a topology first';
        if (this.scenariosLoading) return 'Loading scenarios…';
        if (!this.scenarioNames.length) return 'No scenarios for this topology';
        return 'None';
      },

      scenarioApps() {
        return this.scenarios[this.scenario] ?? [];
      },

      nameError() {
        const name = this.name;
        if (!name) return null;
        if (/\s/.test(name)) return 'Experiment names cannot have spaces';
        if (RESERVED_NAMES.includes(name.toLowerCase())) {
          return `${name} is reserved and cannot name an experiment`;
        }
        if (this.experimentNames.includes(name)) {
          return 'An experiment with this name already exists';
        }
        if (this.bridgeMode == 'auto' && name.length > MAX_BRIDGE_NAME) {
          return `Experiment names must be ${MAX_BRIDGE_NAME} characters or less in auto bridge mode`;
        }
        return null;
      },

      bridgeError() {
        return this.bridge.length > MAX_BRIDGE_NAME
          ? `Default bridge names must be ${MAX_BRIDGE_NAME} characters or less`
          : null;
      },

      vlanError() {
        const min = this.vlanMin ?? null;
        const max = this.vlanMax ?? null;
        if ((min == null) != (max == null)) {
          return 'Enter both ends of the range, or neither';
        }
        if (min != null && max < min) {
          return 'The maximum cannot be below the minimum';
        }
        return null;
      },

      valid() {
        return (
          Boolean(this.name) &&
          !this.nameError &&
          Boolean(this.topology) &&
          !this.bridgeError &&
          !this.vlanError &&
          annotationErrors(this.nodeAnnotations).every((e) => !e) &&
          annotationErrors(this.annotations, this.managedAnnotations).every(
            (e) => !e,
          )
        );
      },
    },

    watch: {
      topology(topology) {
        this.loadScenarios(topology);
      },
    },

    methods: {
      async loadScenarios(topology) {
        this.scenario = null;
        this.scenarios = {};
        if (!topology) return;

        this.scenariosLoading = true;
        try {
          const { data } = await axiosInstance.get(
            `topologies/${encodeURIComponent(topology)}/scenarios`,
          );
          // a topology chosen since owns the field now
          if (topology != this.topology) return;
          this.scenarios = Object.fromEntries(
            Object.entries(data.scenarios ?? {}).map(([name, apps]) => [
              name,
              apps.map((app) => ({ name: app, disabled: false })),
            ]),
          );
        } catch (err) {
          if (topology == this.topology) useErrorNotification(err);
        } finally {
          if (topology == this.topology) this.scenariosLoading = false;
        }
      },

      toggleApp(index) {
        const app = this.scenarioApps[index];
        app.disabled = !app.disabled;
      },

      create() {
        if (!this.valid) return;

        const request = {
          name: this.name,
          topology: this.topology,
          scenario: this.scenario,
          vlan_min: +this.vlanMin,
          vlan_max: +this.vlanMax,
          workflow_branch: this.branch.trim(),
          deploy_mode: this.deployMode,
          disabled_apps: this.scenarioApps
            .filter((app) => app.disabled)
            .map((app) => app.name),
          default_bridge: this.bridge.trim(),
        };
        if (this.nodeAnnotations.length) {
          request.node_annotations = nodeAnnotationsPayload(
            this.nodeAnnotations,
          );
        }
        if (this.annotations.length) {
          request.annotations = experimentAnnotationsPayload(this.annotations);
        }

        this.$emit('create', request);
      },
    },
  };
</script>

<style scoped>
  .create-exp-card {
    width: 44rem;
  }

  /* the Docs button sits beside the title */
  .create-exp-card .modal-card-title {
    margin-bottom: 0;
  }

  /* $danger is under 3:1 on the card; this is 4.7:1 on the card body and
     6.3:1 on the Advanced Options panel */
  .create-exp-card :deep(.help.is-danger) {
    color: #ffc8d1;
  }

  /* the page's .content spaces out paragraphs, the inputs' buttons included */
  .create-exp-vlans :deep(p.control) {
    margin-bottom: 0;
  }

  .create-exp-apps {
    margin-top: -0.25rem;
  }

  .create-exp-advanced {
    margin-top: 1.5rem;
  }

  .create-exp-advanced-toggle {
    width: 100%;
    border: 0;
    padding: 0;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
</style>
