<template>
  <div class="content">
    <hr>
    <b-field position="is-right">
      <b-autocomplete
        placeholder = "Find an Experiment"
        v-model     = "searchName"
        icon        = "search"
        :data       = "filteredData"
        @select     = "option => filtered = option">
        <template slot="empty">
          No results found
        </template>
      </b-autocomplete>
      <p class='control'>
        <button class='button' style="color:#686868" @click="searchName = ''">
          <b-icon icon="window-close"></b-icon>
        </button>
      </p>
    </b-field>
    <b-table
      :data                   = "filteredExperiments"
      :paginated              = "table.isPaginated"
      :per-page               = "table.perPage"
      :current-page.sync      = "table.currentPage"
      :pagination-simple      = "table.isPaginationSimple"
      :pagination-size        = "table.paginationSize"
      :default-sort-direction = "table.defaultSortDirection"
      default-sort            = "name">
      <template slot="empty">
        <section class="section">
          <div class="content has-text-white has-text-centered">
            Your search turned up empty!
          </div>
        </section>
      </template>
      <b-table-column field="name" label="Experiment" width="400" sortable v-slot="props">
        <template v-if="adminUser()">
          <b-tooltip label="view SCORCH components" type="is-dark">
            <router-link class="navbar-item" :to="{ name: 'scorchruns', params: { id: props.row.name } }">
              {{ props.row.name }}
            </router-link>
          </b-tooltip>
        </template>
        <template v-else>
          {{ props.row.name }}
        </template>
      </b-table-column>
      <b-table-column field="status" label="Experiment Status" width="100" sortable centered v-slot="props">
        <template v-if="props.row.status == 'starting'">
          <section>
            <b-progress size="is-medium" type="is-warning" show-value :value=props.row.percent format="percent"></b-progress>
          </section>
        </template>
        <template v-else-if="adminUser()">
          <b-tooltip :label="expControlLabel( props.row )" type="is-dark">
            <span class="tag is-medium" :class="expStatusDecorator( props.row.status )">
              <div class="field" @click="expControl( props.row )">
                {{ props.row.status }}
              </div>
            </span>
          </b-tooltip>                
        </template>
        <template v-else>
          <span class="tag is-medium" :class="statusDecorator( props.row.status )">
            {{ props.row.status }}
          </span>
        </template>
      </b-table-column>
      <b-table-column v-if="globalUser()" label="Scorch Status" width="100" centered v-slot="props">
        <b-tooltip :label="scorchControlLabel( props.row )" type="is-dark">
          <span class="tag is-medium" :class="scorchStatusDecorator( props.row )">
            <div class="field" @click="scorchControl( props.row, -1)">
              {{ scorchStatus( props.row ) }}
            </div>
          </span>
        </b-tooltip>
      </b-table-column>
      <b-table-column v-if="globalUser()" label="Terminal" width="100" centered v-slot="props">
        <button class="button is-small is-white" @click="showExperimentTerminal( props.row.name )" :disabled="!props.row.terminal">
          <b-icon icon="terminal"></b-icon>
        </button>
      </b-table-column>
    </b-table>
    <b-loading :is-full-page="true" :active.sync="isWaiting" :can-cancel="false"></b-loading>
    <b-modal :active.sync="terminal.modal" :on-cancel="resetTerminal" has-modal-card>
      <div class="modal-card" style="width:60em">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ terminalName() }}</p>
        </header>
        <section class="modal-card-body">
          <vue-terminal :wsPath="terminal.loc"></vue-terminal>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <b-tooltip label="this will close but not exit the terminal" type="is-light is-left" :delay="1000">  
            <button class="button is-light" @click="resetTerminal">Close</button>
          </b-tooltip>
          <div v-if="!terminal.ro">
            <b-tooltip label="this will EXIT the terminal" type="is-danger is-left" :delay="1000">
              <button class="button is-danger" @click="exitTerminal">Exit</button>
            </b-tooltip>
          </div>
        </footer>
      </div>
    </b-modal>
  </div>
</template>

<script>
  import Terminal from './Terminal.vue'
  import EventBus from '@/event-bus'

  export default {
    components: {
      'vue-terminal': Terminal
    },

    async created () {
      this.$options.sockets.onmessage = this.handler;
      await this.updateExperiments();
    },

    beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },

    mounted () {
      EventBus.$on('page-reload', (route) => {
        if (route.name == 'scorch') {
          this.updateExperiments();
        }
      });
    },

    computed: {
      filteredExperiments: function() {
        let experiments = this.experiments;
        let nameRegex   = new RegExp(this.searchName, 'i');
        let data        = [];
        
        for (let i in experiments) {
          let exp = experiments[i];
          if (exp.name.match(nameRegex)) {
            if (exp.start_time == '') {
              exp.start_time = 'N/A';
            }

            data.push(exp);
          }
        }

        return data;
      },
    
      filteredData () {
        let names = this.experiments.map(exp => {return exp.name;});

        return names.filter(option => {
          return option.toString().toLowerCase().indexOf(this.searchName.toLowerCase()) >= 0
        })
      }
    },

    methods: {
      globalUser () {
        return [ 'Global Admin' ].includes( this.$store.getters.role );
      },
      
      adminUser () {
        return [ 'Global Admin', 'Experiment Admin' ].includes( this.$store.getters.role );
      },

      async updateExperiments () {
        try {
          let resp  = await this.$http.get( 'experiments' );
          let state = await resp.json();

          let experiments = [];

          for (let i in state.experiments) {
            let exp = state.experiments[i];

            let resp = await this.$http.get(`experiments/${exp.name}/apps`);
            let apps = resp.body;

            // only do stuff with this exp if it has scorch configured
            if ('scorch' in apps) {
              exp.scorch = {running: apps['scorch']};

              if (exp.scorch.running) {
                let resp = await this.$http.get(`experiments/${exp.name}/scorch/pipelines`, {'headers': {'Accept': 'application/json'}})
                exp.scorch.run = resp.body.running;
              }

              experiments.push(exp);
            }
          }

          this.experiments = [...experiments];

          for (let i in this.experiments) {
            let exp = this.experiments[i];

            // check for existing experiment terminals
            this.getTerminals(exp.name);
          }
        } catch (err) {
          this.errorNotification(err);
        } finally {
          this.isWaiting = false;
        }
      },

      expStatusDecorator ( status ) {
        switch ( status ) {
          case 'started':
          case 'running':
            return 'is-success';
          case 'starting':
          case 'stopping':
            return 'is-warning';
          case 'stopped':
            return 'is-danger';
        }
      },

      expControlLabel ( exp ) {
        return exp.running ? `Stop experiment ${exp.name}` : `Start experiment ${exp.name}`;
      },

      expControl ( exp ) {
        if ( exp.status == 'starting' || exp.status == 'stopping' ) {
          this.$buefy.toast.open({
            message: `The ${exp.name} experiment is currently ${exp.status}. You cannot make any changes at this time.`,
            type:    'is-warning'
          });
          
          return;
        }

        if ( exp.running ) {
          this.$buefy.dialog.confirm({
            title:       'Stop the Experiment',
            message:     `This will stop the ${exp.name} experiment.`,
            cancelText:  'Cancel',
            confirmText: 'Stop',
            type:        'is-danger',
            hasIcon:     true,

            onConfirm: async () => {
              try {
                await this.$http.post(`experiments/${exp.name}/stop`);
              } catch (err) {
                this.errorNotification(err);
              }
            }
          });
        } else {
          this.$buefy.dialog.confirm({
            title:       'Start the Experiment',
            message:     `This will start the ${exp.name} experiment.`,
            cancelText:  'Cancel',
            confirmText: 'Start',
            type:        'is-success',
            hasIcon:     true,

            onConfirm: async () => {
              try {
                await this.$http.post(`experiments/${exp.name}/start`);
              } catch (err) {
                this.errorNotification(err);
              }
            }
          });
        }
      },

      scorchStatusDecorator ( exp ) {
        return exp.scorch.running ? 'is-success' : 'is-danger';
      },

      scorchControlLabel ( exp ) {
        if ( exp.scorch.running ) {
          return `cancel run ${exp.scorch.run}`;
        }

        return 'start run 0';
      },

      scorchStatus ( exp ) {
        return exp.scorch.running ? 'running' : 'stopped';
      },

      scorchControl (exp) {
        if (exp.scorch.running) {
          this.$http.delete(`experiments/${exp.name}/scorch/pipelines/${exp.scorch.run}`);
        } else {
          this.$http.post(`experiments/${exp.name}/scorch/pipelines/0`);
        }
      },

      getTerminals (exp) {
        this.$http.get(
          `experiments/${exp}/scorch/terminals`, {'headers': {'Accept': 'application/json'}}
        ).then(
          resp => {
            if (resp.body.terminals) {
              resp.body.terminals.forEach(t => this.terminals[t.exp] = t);

              for (let exp in this.terminals) {
                this.experimentTerminal(exp, true);
              }
            }
          }, err => {
            this.errorNotification(err);
          }
        )
      },

      terminalName () {
        let name = `Terminal (${this.terminal.exp})`;

        if (this.terminal.ro) {
          name += ' (read-only)';
        }

        return name;
      },

      resetTerminal () {
        this.terminal = {
          modal: false,
          exp:   '',
          loc:   '',
          exit:  '',
          ro:    false
        }
      },

      exitTerminal () {
        this.$http.post(this.terminal.exit);
        this.resetTerminal();
        this.experimentTerminal(this.terminal.exp, false);
        delete(this.terminals[this.terminal.exp]);
      },

      experimentTerminal (exp, enabled) {
        for (let i = 0; i < this.experiments.length; i++) {
          if (this.experiments[i].name == exp) {
            this.$set(this.experiments[i], 'terminal', enabled);
          }
        }
      },

      showExperimentTerminal (exp) {
        for (let e in this.terminals) {
          if (e == exp) {
            let comp     = this.terminals[e];
            let endpoint = `experiments/${comp.exp}/scorch/components/${comp.run}/${comp.loop}/${comp.stage}/${comp.name}`;

            this.$http.get(
              endpoint, {'headers': {'Accept': 'application/json'}}
            ).then(
              resp => {
                if (resp.body.terminal) {
                  let t = resp.body.terminal;

                  this.terminal.loc   = t.loc;
                  this.terminal.exit  = t.exit;
                  this.terminal.exp   = t.exp;
                  this.terminal.ro    = t.readOnly;
                  this.terminal.modal = true;
                } else {
                  // TODO: do we need to update this as an error? See similarly line 413ff.
                  this.$buefy.toast.open({
                    message:  `Unable to get current terminal for ${exp} experiment`,
                    type:     'is-info',
                    duration: 4000
                  });
                }
              }, err => {
                this.errorNotification(err);
              }
            )
          }            
        }
      },
      
      handler (event) {
        event.data.split(/\r?\n/).forEach(m => {
          if (m) {
            let msg = JSON.parse(m);
            this.handle(msg);
          }
        });
      },
    
      handle (msg) {
        switch (msg.resource.type) {
          case 'apps/scorch': {
            let tokens = msg.resource.name.split('/');

            let expName = tokens[0];
            let runID   = tokens[1];

            let exp = this.experiments;

            switch (msg.resource.action) {
              case 'start': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == expName) {
                    exp[i].scorch.running = true;
                    exp[i].scorch.run = runID;

                    this.experiments = [...exp];

                    break;
                  }
                }

                break;
              }

              case 'success': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == expName) {
                    exp[i].scorch.running = false;

                    this.experiments = [...exp];

                    break;
                  }
                }

                break;
              }

              case 'error': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == expName) {
                    exp[i].scorch.running = false;

                    this.experiments = [...exp];

                    break;
                  }
                }

                // TODO: do something with error message in `msg.result`
                console.log(msg.result.error);

                break;
              }

              case 'terminal-create': {
                this.terminals[expName] = msg.result;
                this.experimentTerminal(expName, true);

                break;
              }

              case 'terminal-exit': {
                this.experimentTerminal(expName, false);
                delete(this.terminals[expName]);

                break;
              }
            }

            break;
          }

          case 'experiment': {
            let exp = this.experiments;

            switch (msg.resource.action) {
              case 'create': {
                // new experiment created -- check to see if it has Scorch configured
                this.$http.get(
                  'experiments/' + msg.resource.name + '/apps', { 'headers': { 'Accept': 'application/json' } }
                ).then(
                  resp => {
                    let apps = resp.body;

                    // if experiment has scorch configured, add it to the list of Scorch experiments
                    if ('scorch' in apps) {
                      msg.result.status = 'stopped';
                      msg.result.scorch = {running: false};

                      exp.push(msg.result);

                      this.experiments = [...exp];

                      this.$buefy.toast.open({
                        message:  `The ${msg.resource.name} experiment has been created.`,
                        type:     'is-success',
                        duration: 4000
                      });
                    }
                  }
                )

                break;
              }

              case 'delete': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == msg.resource.name) {
                    exp.splice(i, 1);

                    this.experiments = [...exp];

                    this.$buefy.toast.open({
                      message:  `The ${msg.resource.name} experiment has been deleted.`,
                      type:     'is-success',
                      duration: 4000
                    });

                    break;
                  }
                }

                break;
              }

              case 'start': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == msg.resource.name) {
                    exp[i] = msg.result;
                    exp[i].status = 'started';
              
                    this.experiments = [...exp];

                    this.$buefy.toast.open({
                      message:  `The ${msg.resource.name} experiment has been started.`,
                      type:     'is-success',
                      duration: 4000
                    });

                    break;
                  }
                }

                break;
              }

              case 'stop': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == msg.resource.name) {
                    exp[i] = msg.result;
                    exp[i].status = 'stopped';
              
                    this.experiments = [...exp];
                    delete(this.terminals[msg.resource.name]);

                    this.$buefy.toast.open({
                      message:  `The ${msg.resource.name} experiment has been stopped.`,
                      type:     'is-success',
                      duration: 4000
                    });
                    
                    break;
                  }
                }

                break;
              }

              case 'starting': // fallthru to `stopping`
              case 'stopping': {
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == msg.resource.name) {
                    exp[i].status  = msg.resource.action;
                    exp[i].percent = 0;

                    this.experiments = [...exp];
                
                    this.$buefy.toast.open({
                      message: `The ${msg.resource.name} experiment is being updated.`,
                      type:    'is-warning'
                    });

                    break;
                  }
                }

                break;
              }

              case 'progress': {
                let percent = (msg.result.percent * 100).toFixed(0);
                
                for (let i = 0; i < exp.length; i++) {
                  if (exp[i].name == msg.resource.name) {
                    exp[i].percent = parseInt(percent);
                
                    this.experiments = [...exp];

                    break;
                  }
                }

                break;
              }
            }
          }
        }
      }
    },

    data () {
      return {
        experiments: [], // experiments with scorch configured
        running:     {}, // current scorch status for each experiment
        terminals:   {}, // active terminals (TODO: for all experiments?)
        terminal: { // terminal currently being viewed
          modal: false,
          exp:   '',
          loc:   '',
          exit:  '',
          ro:    false
        },
        table: {
          isPaginated:          false,
          perPage:              10,
          currentPage:          1,
          isPaginationSimple:   true,
          paginationSize:       'is-small',
          defaultSortDirection: 'asc'
        },
        searchName: '',
        isWaiting: true
      }
    }
  }
</script>

<style scoped>
  div.autocomplete >>> a.dropdown-item {
    color: #383838 !important;
  }
</style>
