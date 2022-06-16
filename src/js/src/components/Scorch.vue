<template>
  <div class="content">
    <template v-if="runs">
      <div v-for="(run, id) in runs" :key="id"> 
        <hr>
        <scorch-run :exp="exp.name" :run="id" :loop="run.loop" :running="run.running" :nodes="run.nodes" :viewer="componentDetail"
                    :controller="scorchControl" :rewinder="loopHistory" />
      </div>
      <hr>
      <scorch-key />
    </template>
    <template v-else>
      <hr>
      <b-field position="is-right">
        <b-autocomplete v-model="searchName" placeholder="Find an Experiment" icon="search" :data="filteredData" @select="option => filtered = option">
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
        :data="filteredExperiments"
        :paginated="table.isPaginated"
        :per-page="table.perPage"
        :current-page.sync="table.currentPage"
        :pagination-simple="table.isPaginationSimple"
        :pagination-size="table.paginationSize"
        :default-sort-direction="table.defaultSortDirection"
        default-sort="name">
        <template slot="empty">
          <section class="section">
            <div class="content has-text-white has-text-centered">
              Your search turned up empty!
            </div>
          </section>
        </template>
        <template slot-scope="props">
          <b-table-column field="name" label="Experiment" width="400" sortable>
            <template v-if="adminUser()">
              <b-tooltip label="view SCORCH components" type="is-dark">
                <div class="field">
                  <div @click="runsView( props.row )">
                    {{ props.row.name }}
                  </div>
                </div>
              </b-tooltip>
            </template>
            <template v-else>
              {{ props.row.name }}
            </template>
          </b-table-column>
          <b-table-column field="status" label="Experiment Status" width="100" sortable centered>
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
          <b-table-column v-if="globalUser()" label="Scorch Status" width="100" centered>
            <b-tooltip :label="scorchControlLabel( props.row )" type="is-dark">
              <span class="tag is-medium" :class="scorchStatusDecorator( props.row )">
                <div class="field" @click="scorchControl( props.row, -1)">
                  {{ scorchStatus( props.row ) }}
                </div>
              </span>
            </b-tooltip>                
          </b-table-column>
          <b-table-column v-if="globalUser()" label="Terminal" width="100" centered>
            <button class="button is-small is-white" @click="showExperimentTerminal( props.row.name )" :disabled="!props.row.terminal">
              <b-icon icon="terminal"></b-icon>
            </button>
          </b-table-column>
        </template>
      </b-table>
    </template>
    <b-loading :is-full-page="true" :active.sync="isWaiting" :can-cancel="false"></b-loading>
    <b-modal :active.sync="terminal.modal" :on-cancel="resetTerminal" has-modal-card>
      <div class="modal-card" style="width:60em">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ terminalName() }}</p>
        </header>
        <section class="modal-card-body">
          <vue-terminal :loc="terminal.loc"></vue-terminal>
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
    <b-modal :active.sync="output.modal" :on-cancel="exitOutput" has-modal-card>
      <div class="modal-card" style="width:50em">
        <header class="modal-card-head x-modal-dark">
          <p class="modal-card-title x-config-text">{{ output.title }}</p>
        </header>
        <section class="modal-card-body x-modal-dark">
          <div class="control">
            <textarea class="textarea x-config-text has-fixed-size" rows="30" v-model="output.msg" readonly />
          </div>
        </section>
        <footer class="modal-card-foot x-modal-dark buttons is-right">
          <button class="button is-dark" @click="exitOutput">
            Exit
          </button>
        </footer>
      </div>
    </b-modal>
  </div>
</template>

<script>
  import ScorchKey from './ScorchKey.vue'
  import ScorchRun from './ScorchRun.vue'
  import Terminal  from './Terminal.vue'
  import EventBus  from '@/event-bus'

  export default {
    components: {
      'scorch-key':   ScorchKey,
      'scorch-run':   ScorchRun,
      'vue-terminal': Terminal
    },

    async created () {
      this.$options.sockets.onmessage = this.handler;
      await this.updateExperiments();

      if ( this.$route.params.id ) {
        for ( let i in this.experiments ) {
          if ( this.experiments[i].name == this.$route.params.id ) {
            this.runsView( this.experiments[i] );
            break;
          }
        }
      }
    },

    beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },

    mounted () {
      EventBus.$on( 'page-reload', ( route ) => {
        if ( route.name == 'scorch' ) {
          this.exp  = null;
          this.runs = null;
        }
      });
    },

    computed: {
      filteredExperiments: function() {
        let experiments = this.experiments;
        let nameRegex   = new RegExp( this.searchName, 'i' );
        let data        = [];
        
        for ( let i in experiments ) {
          let exp = experiments[ i ];
          if ( exp.name.match( nameRegex ) ) {
            if ( exp.start_time == '' ) {
              exp.start_time = 'N/A';
            }

            data.push( exp );
          }
        }

        return data;
      },
    
      filteredData () {
        let names = this.experiments.map( exp => { return exp.name; } );

        return names.filter(
          option => {
            return option
              .toString()
              .toLowerCase()
              .indexOf( this.searchName.toLowerCase() ) >= 0
          }
        )
      },

      paginationNeeded () {
        let user = localStorage.getItem( 'user' );

        if ( localStorage.getItem( user + '.lastPaginate' ) ) {
          this.table.isPaginated = localStorage.getItem( user + '.lastPaginate' )  == 'true';
        }

        if ( this.experiments.length <= 10 ) {
          this.table.isPaginated = false;
          return false;
        } else {
          return true;
        }
      },
    },

    methods: {
      globalUser () {
        return [ 'Global Admin' ].includes( this.$store.getters.role );
      },
      
      adminUser () {
        return [ 'Global Admin', 'Experiment Admin' ].includes( this.$store.getters.role );
      },
      
      experimentUser () {
        return [ 'Global Admin', 'Experiment Admin', 'Experiment User' ].includes( this.$store.getters.role );
      },

      changePaginate () {
        let user = localStorage.getItem( 'user' );
        localStorage.setItem( user + '.lastPaginate', this.table.isPaginated );
      },

      async updateExperiments () {
        try {
          let resp  = await this.$http.get( 'experiments' );
          let state = await resp.json();

          for ( let i in state.experiments ) {
            let exp = state.experiments[i];

            let resp = await this.$http.get( 'experiments/' + exp.name + '/apps' );
            let apps = resp.body;

            // only do stuff with this exp if it has scorch configured
            if ( 'scorch' in apps ) {
              exp.scorch = { running: apps['scorch'] };

              if ( exp.scorch.running ) {
                let resp = await this.$http.get('experiments/' + exp.name + '/scorch/pipelines', { 'headers': { 'Accept': 'application/json' } })
                exp.scorch.run = resp.body.running;
              }

              this.experiments.push( exp );

              // check for existing experiment terminals
              this.getTerminals( exp.name );
            }
          }
        } catch {
          this.$buefy.toast.open({
            message: 'Getting the experiments failed.',
            type: 'is-danger',
            duration: 4000
          });
        } finally {
          this.isWaiting  = false;
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
        return exp.running ? "Stop experiment " + exp.name : "Start experiment " + exp.name;
      },

      expControl ( exp ) {
        if ( exp.status == 'starting' || exp.status == 'stopping' ) {
          this.$buefy.toast.open({
            message: 'The ' + exp.name + ' experiment is currently ' + exp.status + '. You cannot make any changes at this time.',
            type: 'is-warning'
          });
          
          return;
        }

        if ( exp.running ) {
          this.$buefy.dialog.confirm({
            title: 'Stop the Experiment',
            message: 'This will stop the ' + exp.name + ' experiment.',
            cancelText: 'Cancel',
            confirmText: 'Stop',
            type: 'is-danger',
            hasIcon: true,
            onConfirm: async () => {
              try {
                await this.$http.post('experiments/' + exp.name + '/stop');
              } catch (err) {
                this.$buefy.toast.open({
                  message: 'Stopping the ' + exp.name + ' experiment failed with ' + err.status + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
              }
            }
          });
        } else {
          this.$buefy.dialog.confirm({
            title: 'Start the Experiment',
            message: 'This will start the ' + exp.name + ' experiment.',
            cancelText: 'Cancel',
            confirmText: 'Start',
            type: 'is-success',
            hasIcon: true,
            onConfirm: async () => {
              try {
                await this.$http.post('experiments/' + exp.name + '/start');
              } catch (err) {
                this.$buefy.toast.open({
                  message: 'Starting the ' + exp.name + ' experiment failed with ' + err.status + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
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
          return 'cancel run ' + exp.scorch.run;
        }

        return 'start run 0';
      },

      scorchStatus ( exp ) {
        return exp.scorch.running ? 'running' : 'stopped';
      },

      scorchControl ( exp, runID ) {
        if ( runID < 0 ) {
          if ( exp.scorch.running ) {
            this.$http.delete( 'experiments/' + exp.name + '/scorch/pipelines/' + exp.scorch.run );
          } else {
            this.$http.post( 'experiments/' + exp.name + '/scorch/pipelines/0' );
          }

          return
        }

        let run = this.runs[runID];

        if ( run.running ) {
          this.$http.delete( 'experiments/' + exp + '/scorch/pipelines/' + runID );
          // TODO: handle errors
        } else {
          this.$http.post( 'experiments/' + exp + '/scorch/pipelines/' + runID );
          // TODO: handle errors
        }
      },

      runsView ( exp ) {
        this.$http.get(
          'experiments/' + exp.name + '/scorch/pipelines', { 'headers': { 'Accept': 'application/json' } }
        ).then(
          resp => {
            this.exp  = exp;
            this.runs = [];

            let pipelines = resp.body.pipelines;
            let runningID = resp.body.running;

            for ( let i = 0; i < pipelines.length; i++ ) {
              let running = i == runningID;
              this.runs.push( { running, loop: 0, nodes: pipelines[i].pipeline } );
            }

            exp.scorch.run = runningID;
          }, err => {
            let msg = err.statusText;

            if ( err.body.message ) {
              msg = err.body.message;
            }

            this.$buefy.toast.open({
              message: 'Getting Scorch runs for ' + exp.name + ' failed: ' + msg,
              type: 'is-danger',
              duration: 4000
            });
          }
        )
      },

      componentDetail ( comp ) {
        switch ( comp.name ) {
          case 'configure': case 'start': case 'stop': case 'cleanup': {
            break;
          }

          case 'done': {
            if ( comp.status === "running" ) {
              this.output.title = comp.exp + ' - ' + 'Run: ' + comp.run;
              this.output.msg = 'Filebeat is processing Scorch component files from the current run.';
              this.output.modal = true;
            }

            break;
          }

          case 'loop': {
            this.loopView(comp.exp, comp.run, comp.loop + 1);
            break;
          }

          default: {
            let endpoint = 'experiments/' + comp.exp + '/scorch/components/' + comp.run + '/' + comp.loop + '/' + comp.stage + '/' + comp.name;

            this.$http.get(
              endpoint, { 'headers': { 'Accept': 'application/json' } }
            ).then(
              resp => {
                if ( resp.body.output ) {
                  this.output.title = comp.exp + ' - ' + 'Node: ' + comp.name + ' - ' + 'Run: ' + comp.run + ' - ' + 'Stage: ' + comp.stage;
                  this.output.msg = resp.body.output;
                  this.output.modal = true;
                } else if ( resp.body.stream ) {
                  this.output.title = comp.exp + ' - ' + 'Node: ' + comp.name + ' - ' + 'Run: ' + comp.run + ' - ' + 'Stage: ' + comp.stage;

                  this.getOutputStream( resp.body.stream );

                  this.output.modal = true;
                } else if ( resp.body.terminal ) {
                  let t = resp.body.terminal;

                  this.terminal.loc = t.loc;
                  this.terminal.exit = t.exit;
                  this.terminal.exp = t.exp;
                  this.terminal.ro = t.readOnly;
                  this.terminal.modal = true;
                } else {
                  this.$buefy.toast.open({
                    message: 'There is no output available for the ' + comp.name + ' node in the ' + comp.stage + ' stage',
                    type: 'is-info',
                    duration: 4000
                  });
                }
              }, err => {
                let msg = err.statusText;

                if ( err.body.message ) {
                  msg = err.body.message;
                }

                this.$buefy.toast.open({
                  message: 'Getting component details failed: ' + msg,
                  type: 'is-danger',
                  duration: 4000
                });
              }
            )
          }
        }
      },

      loopView ( exp, runID, loopID ) {
        this.$http.get(
          'experiments/' + exp + '/scorch/pipelines/' + runID + '/' + loopID, { 'headers': { 'Accept': 'application/json' } }
        ).then(
          resp => {
            let run = this.runs[runID];

            run.loop  = loopID;
            run.nodes = resp.body.pipeline;

            // using `Vue.set` to force reactivity
            this.$set(this.runs, runID, run);
          }, err => {
            let msg = err.statusText;

            if ( err.body.message ) {
              msg = err.body.message;
            }

            this.$buefy.toast.open({
              message: 'Getting pipeline for ' + exp + ' (run ' + run + ' loop ' + loop + ') failed: ' + msg,
              type: 'is-danger',
              duration: 4000
            });
          }
        )
      },

      getOutputStream ( loc ) {
        if ( this.$store.getters.auth ) {
          let path = '/api/v1/ws';

          if ( this.$store.getters.token ) {
            path += '?token=' + this.$store.getters.token;
          }

          this.$connect( '//' + location.host + path );

          let proto = window.location.protocol == "https:" ? "wss://" : "ws://";
          let url = proto + window.location.host + loc;
          this.output.socket = new WebSocket( url );

          this.output.socket.onmessage = ( event => {
            this.output.msg += event.data;
          })
        }
      },

      loopHistory ( exp, runID ) {
        let run = this.runs[runID];

        if (run.loop > 0) {
          this.loopView(exp, runID, run.loop - 1);
        }
      },

      getTerminals ( exp ) {
        this.$http.get(
          'experiments/' + exp + '/scorch/terminals', { 'headers': { 'Accept': 'application/json' } }
        ).then(
          resp => {
            if ( resp.body.terminals ) {
              resp.body.terminals.forEach( t => this.terminals[t.exp] = t );

              for ( let exp in this.terminals ) {
                this.experimentTerminal( exp, true );
              }
            }
          }, err => {
            let msg = err.statusText;

            if ( err.body.message ) {
              msg = err.body.message;
            }

            this.$buefy.toast.open({
              message: 'Getting terminals failed: ' + msg,
              type: 'is-danger',
              duration: 4000
            });
          }
        )
      },

      terminalName () {
        let name = 'Terminal (' + this.terminal.exp + ')';

        if ( this.terminal.ro ) {
          name += ' (read-only)';
        }

        return name;
      },

      resetTerminal () {
        this.terminal = {
          modal: false,
          exp: '',
          loc: '',
          exit: '',
          ro: false
        }
      },

      exitTerminal () {
        this.$http.post( this.terminal.exit );
        this.resetTerminal();
        this.experimentTerminal( this.terminal.exp, false );
        delete( this.terminals[ this.terminal.exp ] );
      },

      exitOutput () {
        this.$disconnect();
        
        if ( this.output.socket != null ) {
          this.output.socket.close();
          this.output.socket = null;
        }

        this.output.title = '';
        this.output.msg = [];
        this.output.modal = false;
      },

      experimentTerminal ( exp, enabled ) {
        for ( let i = 0; i < this.experiments.length; i++ ) {
          if ( this.experiments[ i ].name == exp ) {
            this.$set( this.experiments[ i ], "terminal", enabled );
          }
        }
      },

      showExperimentTerminal ( exp ) {
        for ( let e in this.terminals ) {
          if ( e == exp ) {
            this.componentDetail( this.terminals[ e ] );
          }            
        }
      },
      
      handler ( event ) {
        event.data.split( /\r?\n/ ).forEach( m => {
          if ( m ) {
            let msg = JSON.parse( m );
            this.handle( msg );
          }
        });
      },
    
      handle ( msg ) {
        switch ( msg.resource.type ) {
          case 'apps/scorch': {
            let tokens = msg.resource.name.split( '/' );

            let expName = tokens[0];
            let runID   = tokens[1];

            let exp = this.experiments;

            switch ( msg.resource.action ) {
              case 'start': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == expName ) {
                    exp[i].scorch.running = true;
                    exp[i].scorch.run = runID;

                    this.experiments = [ ...exp ];

                    break;
                  }
                }

                if ( this.exp && this.exp.name == expName ) {
                  let run = this.runs[runID];
                  run.running = true;
                  this.$set(this.runs, runID, run);
                }

                break;
              }

              case 'success': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == expName ) {
                    exp[i].scorch.running = false;

                    this.experiments = [ ...exp ];

                    break;
                  }
                }

                if ( this.exp && this.exp.name == expName ) {
                  let run = this.runs[runID];
                  run.running = false;
                  this.$set(this.runs, runID, run);
                }

                break;
              }

              case 'error': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == expName ) {
                    exp[i].scorch.running = false;

                    this.experiments = [ ...exp ];

                    break;
                  }
                }

                if ( this.exp && this.exp.name == expName ) {
                  let run = this.runs[runID];
                  run.running = false;
                  this.$set(this.runs, runID, run);
                }

                // TODO: do something with error message in `msg.result`
                console.log(msg.result.error);

                break;
              }

              case 'terminal-create': {
                this.terminals[ expName ] = msg.result;
                this.experimentTerminal( expName, true );

                break;
              }

              case 'terminal-exit': {
                this.experimentTerminal( expName, false );
                delete( this.terminals[ expName ] );

                break;
              }

              case 'pipeline-update': {
                if ( this.exp && this.exp.name == expName ) {
                  let loopID = parseInt( tokens[ 2 ] );
                  let run    = this.runs[runID];

                  if ( run.loop == loopID ) {
                    run.nodes = msg.result.pipeline;
                    this.$set(this.runs, runID, run);
                  }
                }

                break;
              }
            }

            break;
          }

          case 'experiment': {
            let exp = this.experiments;

            switch ( msg.resource.action ) {
              case 'create': {
                this.$http.get(
                  'experiments/' + msg.resource.name + '/apps', { 'headers': { 'Accept': 'application/json' } }
                ).then(
                  resp => {
                    let apps = resp.body;

                    if ( 'scorch' in apps ) {
                      msg.result.status = 'stopped';
                      msg.result.scorch = { running: false };

                      exp.push( msg.result );

                      this.experiments = [ ...exp ];
                    }
                  }
                )
            
                this.$buefy.toast.open({
                  message: 'The ' + msg.resource.name + ' experiment has been created.',
                  type: 'is-success',
                  duration: 4000
                });

                break;
              }

              case 'delete': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == msg.resource.name ) {
                    exp.splice( i, 1 );

                    this.experiments = [ ...exp ];

                    break;
                  }
                }
              
                this.$buefy.toast.open({
                  message: 'The ' + msg.resource.name + ' experiment has been deleted.',
                  type: 'is-success',
                  duration: 4000
                });

                break;
              }

              case 'start': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == msg.resource.name ) {
                    exp[ i ] = msg.result;
                    exp[ i ].status = 'started';
              
                    this.experiments = [ ...exp ];

                    break;
                  }
                }
              
                this.$buefy.toast.open({
                  message: 'The ' + msg.resource.name + ' experiment has been started.',
                  type: 'is-success',
                  duration: 4000
                });

                break;
              }

              case 'stop': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == msg.resource.name ) {
                    exp[ i ] = msg.result;
                    exp[ i ].status = 'stopped';
              
                    this.experiments = [ ...exp ];
                    delete( this.terminals[ msg.resource.name ] );
                    
                    break;
                  }
                }
              
                this.$buefy.toast.open({
                  message: 'The ' + msg.resource.name + ' experiment has been stopped.',
                  type: 'is-success',
                  duration: 4000
                });

                break;
              }

              case 'starting': // fallthru to `stopping`
              case 'stopping': {
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == msg.resource.name ) {
                    exp[ i ].status = msg.resource.action;
                    exp[ i ].percent = 0;
                
                    this.experiments = [ ...exp ];

                    break;
                  }
                }
                
                this.$buefy.toast.open({
                  message: 'The ' + msg.resource.name + ' experiment is being updated.',
                  type: 'is-warning'
                });

                break;
              }

              case 'progress': {
                let percent = ( msg.result.percent * 100 ).toFixed( 0 );
                
                for ( let i = 0; i < exp.length; i++ ) {
                  if ( exp[ i ].name == msg.resource.name ) {
                    exp[ i ].percent = parseInt( percent );
                
                    this.experiments = [ ...exp ];

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
        running: {},     // current scorch status for each experiment
        terminals: {},   // active terminals (TODO: for all experiments?)
        exp: null,       // experiment currently viewing runs for
        runs: null,      // runs for experiment currently viewing
        terminal: { // terminal currently being viewed
          modal: false,
          exp: '',
          loc: '',
          exit: '',
          ro: false
        },
        output: { // output currently being viewed
          modal: false,
          title: '',
          msg: '',
          socket: null
        },
        table: {
          isPaginated: false,
          perPage: 10,
          currentPage: 1,
          isPaginationSimple: true,
          paginationSize: 'is-small',
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

  .x-modal-dark {
    background-color: #5b5b5b;
  }

  .x-modal-dark >>> p {
    color: whitesmoke;
  }

  .x-modal-dark >>> textarea {
    background-color: #686868;
    color: whitesmoke;
  }

  .x-config-text {
    font-family: monospace;
  }
</style>
