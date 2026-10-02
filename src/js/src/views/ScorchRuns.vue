<template>
  <div class="content">
    <div class="runs-header">
      <div class="buttons">
        <router-link class="button is-dark" :to="{ name: 'scorch' }">
          <b-icon icon="arrow-left" />
          <span>Back to SCORCH</span>
        </router-link>
        <router-link
          v-if="roleAllowed('experiments', 'get', expName)"
          class="button is-dark"
          :to="{ name: 'experiment', params: { id: expName } }">
          <span>Go to experiment</span>
          <b-icon icon="arrow-right" />
        </router-link>
      </div>
      <b-tooltip :label="`The experiment is ${expState.label}`" type="is-dark">
        <span class="runs-title tag is-large" :class="expState.tag">
          Experiment: {{ expName }}
        </span>
      </b-tooltip>
      <div class="buttons">
        <b-tooltip
          v-if="roleAllowed('experiments/trigger', 'create', expName)"
          label="Starts all defined runs in order one by one (not in parallel)"
          type="is-light"
          multilined>
          <button
            class="button is-success"
            :disabled="!canStartAll"
            @click="startAll">
            <b-icon icon="play" />
            <span>Start all</span>
          </button>
        </b-tooltip>
        <button
          v-if="roleAllowed('experiments/trigger', 'delete', expName)"
          class="button is-danger"
          :disabled="!canStopAll"
          @click="stopAll">
          <b-icon icon="stop" />
          <span>Stop all</span>
        </button>
        <b-tooltip
          v-if="roleAllowed('experiments/trigger', 'create', expName)"
          label="clear the status of every run that is not running"
          type="is-light is-left">
          <button
            class="button is-dark"
            :disabled="!canClearAll"
            @click="clearAll">
            <b-icon icon="eraser" />
            <span>Clear all</span>
          </button>
        </b-tooltip>
      </div>
    </div>
    <div v-for="(run, id) in runs" :key="id">
      <hr />
      <scorch-run
        :exp="expName"
        :run="id"
        :name="run.name"
        :loop="run.loop"
        :running="run.running"
        :pending="run.pending || run.queued"
        :has-cleanup="run.hasCleanup"
        :can-clean-up="!anyBusy"
        :nodes="run.nodes"
        :viewer="componentDetail"
        :controller="scorchControl"
        :cleaner="cleanupRun"
        :clearer="clearRun"
        :clearable="roleAllowed('experiments/trigger', 'create', expName)"
        :can-clear="canClear(run)"
        :rewinder="loopHistory" />
    </div>
    <hr />
    <scorch-key />
    <b-modal
      v-model="terminal.modal"
      :can-cancel="terminal.ro"
      @close="resetTerminal"
      has-modal-card>
      <div class="modal-card" style="width: 60em">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ terminalName() }}</p>
        </header>
        <section class="modal-card-body">
          <vue-terminal :wsPath="terminal.loc"></vue-terminal>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <div v-if="terminal.ro">
            <b-tooltip
              label="this will close but not exit the terminal"
              type="is-light is-left"
              :delay="1000">
              <button class="button is-light" @click="resetTerminal">
                Close
              </button>
            </b-tooltip>
          </div>
          <div v-else>
            <b-tooltip
              label="this will EXIT the terminal"
              type="is-danger is-left"
              :delay="1000">
              <button class="button is-danger" @click="exitTerminal">
                Exit
              </button>
            </b-tooltip>
          </div>
        </footer>
      </div>
    </b-modal>
    <b-modal v-model="output.modal" @close="exitOutput" has-modal-card>
      <div class="modal-card" style="width: 50em">
        <header class="modal-card-head x-modal-dark output-head">
          <p class="modal-card-title">{{ output.title }}</p>
          <dl class="output-details">
            <div>
              <dt>Experiment:</dt>
              <dd>{{ expName }}</dd>
            </div>
            <div>
              <dt>Run:</dt>
              <dd>{{ outputRun }}</dd>
            </div>
            <div v-if="output.comp?.stage">
              <dt>Stage:</dt>
              <dd>{{ output.comp.stage }}</dd>
            </div>
            <div v-if="outputStatus">
              <dt>Status:</dt>
              <dd>
                <span class="tag" :class="outputStatus.tag">
                  {{ outputStatus.label }}
                </span>
              </dd>
            </div>
          </dl>
        </header>
        <section class="modal-card-body x-modal-dark output-body">
          <b-loading :is-full-page="false" :model-value="output.loading" />
          <div class="control">
            <textarea
              class="textarea x-config-text has-fixed-size"
              rows="30"
              :placeholder="output.loading ? 'Loading output…' : ''"
              v-model="output.msg"
              readonly />
          </div>
        </section>
        <footer class="modal-card-foot x-modal-dark buttons is-right">
          <button class="button is-dark" @click="exitOutput">Exit</button>
        </footer>
      </div>
    </b-modal>
  </div>
</template>

<script>
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import axiosInstance from '@/utils/axios.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import { createPageLoader } from '@/utils/pageLoader.js';
  import { scorchRunsKey } from '@/utils/pageData.js';
  import { usePhenixStore } from '@/store.js';
  import { roleAllowed } from '@/utils/rbac.js';

  import ScorchKey from '@/components/scorch/ScorchKey.vue';
  import ScorchRun from '@/components/scorch/ScorchRun.vue';
  import Terminal from '@/components/MiniTerminal.vue';

  // how long a run's button spins waiting for the server to report that the
  // run started or stopped, in case that update never arrives
  const PENDING_TIMEOUT_MS = 15000;

  // how the output window names a component's status
  const STATUS_LABELS = {
    start: { label: 'starting', tag: 'is-info' },
    running: { label: 'running', tag: 'is-info' },
    background: { label: 'running in background', tag: 'is-info' },
    success: { label: 'completed', tag: 'is-success' },
    failure: { label: 'failed', tag: 'is-danger' },
    unstable: { label: 'unstable', tag: 'is-warning' },
    paused: { label: 'paused', tag: 'is-warning' },
  };

  // how the header shows the experiment's state
  const EXPERIMENT_STATES = {
    started: { label: 'running', tag: 'is-success' },
    stopped: { label: 'stopped', tag: 'is-danger' },
    starting: { label: 'starting', tag: 'is-warning' },
    stopping: { label: 'stopping', tag: 'is-warning' },
  };

  // whether any component of the pipeline has run
  const hasStatus = (nodes) =>
    (nodes ?? []).some((node) => node.status && node.status !== 'unknown');

  export default {
    setup() {
      return { roleAllowed };
    },

    components: {
      'scorch-key': ScorchKey,
      'scorch-run': ScorchRun,
      'vue-terminal': Terminal,
    },

    created() {
      addWsHandler(this.handle);
      // cached per experiment, so returning to a pipeline shows it at once
      // while a fresh copy loads
      const exp = this.expName;
      this.loader = createPageLoader({
        key: scorchRunsKey(exp),
        fetch: async (signal) => {
          const opts = { signal, headers: { Accept: 'application/json' } };
          const resp = await axiosInstance.get(
            `experiments/${exp}/scorch/pipelines`,
            opts,
          );
          return resp.data;
        },
        apply: (pipelines) => {
          if (pipelines.experiment?.status) {
            this.expStatus = pipelines.experiment.status;
          }
          this.runs = (pipelines.pipelines ?? []).map((p, i) => ({
            name: p.name,
            running: i == pipelines.running,
            pending: false,
            queued: false,
            clearing: false,
            hasCleanup: p.hasCleanup ?? false,
            nodes: p.pipeline,
            loop: 0,
          }));
        },
      });
      this.loader.start();
    },

    beforeUnmount() {
      removeWsHandler(this.handle);
      this.loader.stop();
      // close the streaming output socket if the modal is still open
      this.exitOutput();
      // drop the queue and the fallback timers
      this.runs?.forEach((run) => {
        this.settle(run);
        run.queued = false;
      });
    },

    computed: {
      expName() {
        return this.$route.params.id;
      },

      canStartAll() {
        return (this.runs ?? []).some(
          (run) => !run.running && !run.pending && !run.queued,
        );
      },

      canStopAll() {
        return (this.runs ?? []).some((run) => run.running || run.queued);
      },

      // the viewed output's run, by name
      outputRun() {
        const comp = this.output.comp;
        if (!comp) return '';

        const name = this.runs?.[comp.run]?.name || comp.run;
        return comp.loop > 0 ? `${name} (loop ${comp.loop})` : `${name}`;
      },

      // the viewed component's current status, kept up to date by pipeline
      // updates while the output is open
      outputStatus() {
        const comp = this.output.comp;
        if (!comp) return null;

        const run = this.runs?.[comp.run];
        const node =
          run?.loop === comp.loop
            ? run.nodes?.find(
                (n) => n.name === comp.name && n.stage === comp.stage,
              )
            : null;
        return STATUS_LABELS[(node ?? comp).status] ?? null;
      },

      // how the header shows the experiment's state
      expState() {
        return (
          EXPERIMENT_STATES[this.expStatus] ?? {
            label: 'loading',
            tag: 'is-dark',
          }
        );
      },

      canClearAll() {
        return (this.runs ?? []).some((run) => this.canClear(run));
      },

      // SCORCH executes one run at a time per experiment
      anyBusy() {
        return (this.runs ?? []).some((run) => run.running || run.pending);
      },
    },

    methods: {
      scorchControl(exp, runID) {
        const run = this.runs?.[runID];
        if (!run || run.pending) return;

        const url = `experiments/${exp}/scorch/pipelines/${runID}`;
        this.request(
          run,
          run.running ? axiosInstance.delete(url) : axiosInstance.post(url),
        );
      },

      // a run can be cleared once it is idle and has a status to clear
      canClear(run) {
        return (
          !run.running &&
          !run.pending &&
          !run.queued &&
          !run.clearing &&
          (run.loop > 0 || hasStatus(run.nodes))
        );
      },

      // forgets the statuses of the run's components; the server sends the
      // cleared pipeline for each loop over the websocket
      async clearRun(exp, runID) {
        const run = this.runs?.[runID];
        if (!run || !this.canClear(run)) return;

        run.clearing = true;
        try {
          await axiosInstance.post(
            `experiments/${exp}/scorch/pipelines/${runID}/clear`,
          );
          if (run.loop > 0) this.loopView(exp, runID, 0);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          run.clearing = false;
        }
      },

      clearAll() {
        this.runs.forEach((run, id) => {
          if (this.canClear(run)) this.clearRun(this.expName, id);
        });
      },

      // runs only the run's cleanup stage
      cleanupRun(exp, runID) {
        const run = this.runs?.[runID];
        if (!run || run.pending) return;

        this.request(
          run,
          axiosInstance.post(
            `experiments/${exp}/scorch/pipelines/${runID}/cleanup`,
          ),
        );
      },

      // The run's button spins until the server reports over the websocket
      // that the run started or stopped, or, if that never comes, for
      // PENDING_TIMEOUT_MS.
      request(run, request) {
        run.pending = true;
        run.pendingTimer = setTimeout(() => {
          run.pending = false;
          this.startNext();
        }, PENDING_TIMEOUT_MS);

        request.catch((err) => {
          this.settle(run);
          useErrorNotification(err);
        });
      },

      settle(run) {
        clearTimeout(run.pendingTimer);
        run.pending = false;
      },

      // Starts every run that is not running, one after another since SCORCH
      // executes one run at a time; a run already running finishes first.
      startAll() {
        this.runs.forEach((run) => {
          if (!run.running && !run.pending) run.queued = true;
        });
        this.startNext();
      },

      startNext() {
        if (!this.runs || this.anyBusy) return;

        const id = this.runs.findIndex((run) => run.queued);
        if (id < 0) return;

        this.runs[id].queued = false;
        this.scorchControl(this.expName, id);
      },

      // stops the running runs and drops the ones still waiting to start
      stopAll() {
        this.runs.forEach((run, id) => {
          run.queued = false;
          if (run.running && !run.pending) this.scorchControl(this.expName, id);
        });
      },

      // reloads the experiment's SCORCH runs
      runsView() {
        return this.loader.load();
      },

      componentDetail(comp) {
        if (!comp) {
          return;
        }

        switch (comp.name) {
          // the stage nodes have no output of their own
          case 'configure':
          case 'start':
          case 'stop':
          case 'cleanup':
            break;

          case 'done': {
            if (comp.status === 'running') {
              this.output.title = 'Archiving artifacts';
              this.output.comp = { ...comp, stage: null };
              this.output.msg =
                'Artifacts generated by this run are being processed and archived.';
              this.output.modal = true;
            }

            break;
          }

          case 'loop': {
            this.loopView(comp.exp, comp.run, comp.loop + 1);
            break;
          }

          default: {
            if (comp.status === 'unknown') {
              // clear stale UI for unknown status
              this.exitOutput();
              this.resetTerminal(true);
              return;
            }

            const endpoint = `experiments/${comp.exp}/scorch/components/${comp.run}/${comp.loop}/${comp.stage}/${comp.name}`;

            // Open the output window at once, loading, so the click shows it
            // was taken; a terminal or a node without output closes it again.
            this.exitOutput();
            const request = ++this.output.request;
            this.output.title = comp.name;
            this.output.comp = comp;
            this.output.loading = true;
            this.output.modal = true;

            axiosInstance
              .get(endpoint, {
                headers: { Accept: 'application/json' },
              })
              .then((resp) => {
                // closed, or another node opened, while this one loaded
                if (this.output.request !== request) return;
                this.output.loading = false;

                if (resp.data.output) {
                  this.output.msg = resp.data.output;
                } else if (resp.data.stream) {
                  this.getOutputStream(resp.data.stream);
                } else if (resp.data.terminal) {
                  this.exitOutput();

                  let t = resp.data.terminal;

                  this.terminal.loc = t.loc;
                  this.terminal.exit = t.exit;
                  this.terminal.exp = t.exp;
                  this.terminal.ro = t.readOnly;
                  this.terminal.modal = true;
                } else {
                  this.exitOutput();
                  this.$buefy.toast.open({
                    message: `There is no output available for the ${comp.name} node in the ${comp.stage} stage`,
                    type: 'is-info',
                    duration: 4000,
                  });
                }
              })
              .catch((err) => {
                if (this.output.request !== request) return;
                this.exitOutput();
                useErrorNotification(err);
              });
          }
        }
      },

      loopView(exp, runID, loopID) {
        axiosInstance
          .get(`experiments/${exp}/scorch/pipelines/${runID}/${loopID}`, {
            headers: { Accept: 'application/json' },
          })
          .then((resp) => {
            const run = this.runs[runID];
            run.loop = loopID;
            run.nodes = resp.data.pipeline ?? [];
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },

      getOutputStream(loc) {
        let path = loc;

        const store = usePhenixStore();
        if (store.token) {
          path += `?token=${store.token}`;
        }

        let proto = window.location.protocol == 'https:' ? 'wss://' : 'ws://';
        let url = proto + window.location.host + path;

        this.output.socket?.close();
        this.output.msg = '';
        this.output.socket = new WebSocket(url);

        this.output.socket.onmessage = (event) => {
          this.output.msg += event.data;
        };
        this.output.socket.onerror = () => {
          this.output.msg +=
            '\n[output stream failed; close and reopen to retry]';
        };
      },

      loopHistory(exp, runID) {
        let run = this.runs[runID];

        if (run.loop > 0) {
          this.loopView(exp, runID, run.loop - 1);
        }
      },

      terminalName() {
        let name = `Terminal (${this.terminal.exp})`;

        if (this.terminal.ro) {
          name += ' (read-only)';
        }

        return name;
      },

      resetTerminal(force = false) {
        if (force || this.terminal.ro) {
          this.terminal = {
            modal: false,
            exp: '',
            loc: '',
            exit: '',
            ro: false,
          };
        }
      },

      exitTerminal() {
        // terminal.exit is a server-provided path that already includes the
        // base path; clear baseURL so axios doesn't prepend it again
        axiosInstance
          .post(this.terminal.exit, null, { baseURL: '' })
          .catch(useErrorNotification)
          .finally(() => {
            this.resetTerminal(true);
          });
      },

      exitOutput() {
        if (this.output.socket != null) {
          this.output.socket.close();
          this.output.socket = null;
        }

        this.output.title = '';
        this.output.comp = null;
        this.output.msg = '';
        this.output.modal = false;
        this.output.loading = false;
        // outdates any fetch still running
        this.output.request++;
      },

      handle(msg) {
        switch (msg.resource.type) {
          case 'apps/scorch': {
            let tokens = msg.resource.name.split('/');

            let expName = tokens[0];
            let runID = tokens[1];

            if (expName !== this.expName) return;

            // the first load is still in flight and will bring this state
            if (this.runs === null) return;

            // a run added since the page loaded: reload to pick it up
            if (!this.runs[runID]) {
              if (!this.loader.loading) this.runsView();
              return;
            }

            const run = this.runs[runID];

            switch (msg.resource.action) {
              case 'start': {
                run.running = true;
                this.settle(run);
                break;
              }

              case 'success': {
                run.running = false;
                this.settle(run);
                this.startNext();
                break;
              }

              case 'error': {
                run.running = false;
                this.settle(run);
                this.startNext();

                showError(
                  msg.result?.error ??
                    `SCORCH run ${runID} failed for experiment ${expName}`,
                );
                break;
              }

              case 'pipeline-update': {
                const loopID = parseInt(tokens[2]);
                if (run.loop == loopID) {
                  run.nodes = msg.result.pipeline ?? [];
                }

                break;
              }
            }

            break;
          }

          case 'experiment': {
            if (msg.resource.name !== this.expName) return;

            switch (msg.resource.action) {
              case 'start': {
                this.expStatus = 'started';
                this.exitOutput();
                this.resetTerminal(true);
                this.runsView();
                break;
              }

              case 'stop': {
                this.expStatus = 'stopped';
                this.runsView();
                break;
              }

              case 'starting':
              case 'stopping': {
                this.expStatus = msg.resource.action;
                break;
              }

              case 'errorStarting': {
                this.expStatus = 'stopped';
                break;
              }

              case 'errorStopping': {
                this.expStatus = 'started';
                break;
              }

              case 'delete': {
                this.$router.replace({ name: 'scorch' });

                this.$buefy.toast.open({
                  message: `The ${msg.resource.name} experiment has been deleted`,
                  type: 'is-success',
                  duration: 4000,
                });

                break;
              }
            }
          }
        }
      },
    },

    data() {
      return {
        runs: null,
        terminal: {
          // terminal currently being viewed
          modal: false,
          exp: '',
          loc: '',
          exit: '',
          ro: false,
        },
        output: {
          // output currently being viewed
          modal: false,
          title: '',
          // the pipeline node whose output is shown
          comp: null,
          msg: '',
          socket: null,
          // set while the output is being fetched
          loading: false,
          // counts output fetches, so an outdated one is ignored
          request: 0,
        },
        // the experiment's state: started, stopped, starting or stopping
        expStatus: null,
      };
    },
  };
</script>

<style scoped>
  /* navigation left, experiment name centered, page actions right */
  .runs-header {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    align-items: center;
    gap: 1rem;
  }

  .runs-header .buttons {
    justify-self: end;
    margin-bottom: 0;
  }

  .runs-header > .buttons:first-child {
    justify-self: start;
  }

  .runs-header .buttons .button {
    margin-bottom: 0;
  }

  .runs-title {
    font-weight: bold;
    font-size: x-large;
  }

  .x-modal-dark {
    background-color: #5b5b5b;
  }

  .x-modal-dark :deep(p) {
    color: whitesmoke;
  }

  .x-modal-dark :deep(textarea) {
    background-color: #686868;
    color: whitesmoke;
  }

  /* the component name, with a line each for what it belongs to beneath */
  .output-head {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.5rem;
  }

  /* outlined, so it reads as the name of the node */
  .output-head .modal-card-title {
    overflow-wrap: anywhere;
    flex-grow: 0;
    border: 2px solid whitesmoke;
    border-radius: 6px;
    padding: 0.2rem 0.6rem;
  }

  .output-body {
    position: relative;
  }

  .output-details {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    margin: 0;
    color: whitesmoke;
  }

  .output-details div {
    display: flex;
    gap: 0.5rem;
    align-items: center;
  }

  .output-details dt {
    font-weight: bold;
  }

  .output-details dd {
    margin: 0;
  }

  .x-config-text {
    font-family: monospace;
  }
</style>
