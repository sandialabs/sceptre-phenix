<template>
  <div class="content">
    <div v-for="(run, id) in runs" :key="id"> 
      <hr>
      <scorch-run :exp="exp.name" :run="id" :loop="run.loop" :running="run.running" :nodes="run.nodes"
                  :viewer="componentDetail" :controller="scorchControl" :rewinder="loopHistory" />
    </div>
    <hr>
    <scorch-key />
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

  export default {
    components: {
      'scorch-key':   ScorchKey,
      'scorch-run':   ScorchRun,
      'vue-terminal': Terminal
    },

    async created () {
      this.$options.sockets.onmessage = this.handler;
      this.runsView( this.$route.params.id );
    },

    beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },

    methods: {
      scorchControl ( exp, runID ) {
        let run = this.runs[runID];

        if ( run.running ) {
          this.$http.delete( `experiments/${exp}/scorch/pipelines/${runID}` );
          // TODO: handle errors
        } else {
          this.$http.post( `experiments/${exp}/scorch/pipelines/${runID}` );
          // TODO: handle errors
        }
      },

      runsView ( exp ) {
        this.$http.get(
          `experiments/${exp}`, { 'headers': { 'Accept': 'application/json' } }
        ).then(
          resp => {
            this.exp = resp.body;

            this.$http.get(
              `experiments/${exp}/scorch/pipelines`, { 'headers': { 'Accept': 'application/json' } }
            ).then(
              resp => {
                this.runs = [];

                let pipelines = resp.body.pipelines;
                let runningID = resp.body.running;

                for ( let i = 0; i < pipelines.length; i++ ) {
                  let running = i == runningID;
                  this.runs.push( { running, loop: 0, nodes: pipelines[i].pipeline } );
                }
              }, err => {
                let msg = err.statusText;

                if ( err.body.message ) {
                  msg = err.body.message;
                }

                this.$buefy.toast.open({
                  message: `Getting Scorch runs for ${exp} failed: ${msg}`,
                  type:    'is-danger',
                  duration: 4000
                });
              }
            )
          }, err => {
            let msg = err.statusText;

            if ( err.body.message ) {
              msg = err.body.message;
            }

            this.$buefy.toast.open({
              message: `Getting details for experiment ${exp} failed: ${msg}`,
              type:    'is-danger',
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
              this.output.title = `${comp.exp} - Run: ${comp.run}`;
              this.output.msg   = 'Filebeat is processing Scorch component files from the current run.';
              this.output.modal = true;
            }

            break;
          }

          case 'loop': {
            this.loopView(comp.exp, comp.run, comp.loop + 1);
            break;
          }

          default: {
            let endpoint = `experiments/${comp.exp}/scorch/components/${comp.run}/${comp.loop}/${comp.stage}/${comp.name}`;

            this.$http.get(
              endpoint, { 'headers': { 'Accept': 'application/json' } }
            ).then(
              resp => {
                if ( resp.body.output ) {
                  this.output.title = `${comp.exp} - Node: ${comp.name} - Run: ${comp.run} - Stage: ${comp.stage}`;
                  this.output.msg   = resp.body.output;
                  this.output.modal = true;
                } else if ( resp.body.stream ) {
                  this.output.title = `${comp.exp} - Node: ${comp.name} - Run: ${comp.run} - Stage: ${comp.stage}`;
                  this.getOutputStream( resp.body.stream );
                  this.output.modal = true;
                } else if ( resp.body.terminal ) {
                  let t = resp.body.terminal;

                  this.terminal.loc   = t.loc;
                  this.terminal.exit  = t.exit;
                  this.terminal.exp   = t.exp;
                  this.terminal.ro    = t.readOnly;
                  this.terminal.modal = true;
                } else {
                  this.$buefy.toast.open({
                    message: `There is no output available for the ${comp.name} node in the ${comp.stage} stage`,
                    type:    'is-info',
                    duration: 4000
                  });
                }
              }, err => {
                let msg = err.statusText;

                if ( err.body.message ) {
                  msg = err.body.message;
                }

                this.$buefy.toast.open({
                  message: `Getting component details failed: ${msg}`,
                  type:    'is-danger',
                  duration: 4000
                });
              }
            )
          }
        }
      },

      loopView ( exp, runID, loopID ) {
        this.$http.get(
          `experiments/${exp}/scorch/pipelines/${runID}/${loopID}`, { 'headers': { 'Accept': 'application/json' } }
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
              message: `Getting pipeline for ${exp} (run ${run}, loop ${loop}) failed: ${msg}`,
              type:    'is-danger',
              duration: 4000
            });
          }
        )
      },

      getOutputStream ( loc ) {
        let path = loc;

        if (this.$store.getters.token) {
          path += `?token=${this.$store.getters.token}`;
        }

        let proto = window.location.protocol == "https:" ? "wss://" : "ws://";
        let url   = proto + window.location.host + path;

        this.output.socket = new WebSocket(url);

        this.output.socket.onmessage = (event => {
          this.output.msg += event.data;
        })
      },

      loopHistory ( exp, runID ) {
        let run = this.runs[runID];

        if (run.loop > 0) {
          this.loopView(exp, runID, run.loop - 1);
        }
      },

      terminalName () {
        let name = `Terminal (${this.terminal.exp})`;

        if ( this.terminal.ro ) {
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
      },

      exitOutput () {
        this.$disconnect();
        
        if ( this.output.socket != null ) {
          this.output.socket.close();
          this.output.socket = null;
        }

        this.output.title = '';
        this.output.msg   = [];
        this.output.modal = false;
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

            if ( !this.exp || this.exp.name !== expName ) {
              return;
            }

            switch ( msg.resource.action ) {
              case 'start': {
                let run     = this.runs[runID];
                run.running = true;

                this.$set(this.runs, runID, run);
                break;
              }

              case 'success': {
                let run     = this.runs[runID];
                run.running = false;

                this.$set(this.runs, runID, run);
                break;
              }

              case 'error': {
                let run     = this.runs[runID];
                run.running = false;

                this.$set(this.runs, runID, run);

                // TODO: do something with error message in `msg.result`
                console.log(msg.result.error);
                break;
              }

              case 'pipeline-update': {
                let loopID = parseInt(tokens[2]);
                let run    = this.runs[runID];

                if (run.loop == loopID) {
                  run.nodes = msg.result.pipeline;
                  this.$set(this.runs, runID, run);
                }

                break;
              }
            }

            break;
          }

          case 'experiment': {
            if (!this.exp || this.exp.name !== msg.resource.name) {
              return;
            }

            switch ( msg.resource.action ) {
              case 'delete': {
                this.$router.replace({name: 'scorch'});
              
                this.$buefy.toast.open({
                  message: `The ${msg.resource.name} experiment has been deleted`,
                  type:    'is-success',
                  duration: 4000
                });

                break;
              }
            }
          }
        }
      }
    },

    data () {
      return {
        exp:  null,
        runs: null,
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
        isWaiting: false
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
