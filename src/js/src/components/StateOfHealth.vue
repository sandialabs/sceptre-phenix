<template>
  <div>
    <b-modal :active.sync="detailsModal.active" :on-cancel="resetDetailsModal" has-modal-card full-screen>
      <div class="modal-card">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ detailsModal.vm }} Details</p>
        </header>
        <section class="modal-card-body">
          <template v-if="detailsModal.soh">
            <p>The following state of health has been reported for {{ detailsModal.vm }}.</p>
            <br>
            <div v-if="detailsModal.soh.cpuLoad">
              <p class="title is-5">CPU Load: {{ detailsModal.soh.cpuLoad }}</p>
            </div>
            <br>
            <div v-if="detailsModal.soh.networking">
              <p class="title is-5">Networking</p>
              <b-table
                :data="detailsModal.soh.networking"
                default-sort="timestamp">
                <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column field="success" label="Success" sortable v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column field="error" label="Error" sortable v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br>
            </div>
            <div v-if="detailsModal.soh.reachability">
              <p class="title is-5">Reachability</p>
              <b-table
                :data="detailsModal.soh.reachability"
                default-sort="timestamp">
                <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column field="host" label="Source" sortable v-slot="props">
                  {{ props.row.metadata.host }}
                </b-table-column>
                <b-table-column field="target" label="Target" sortable v-slot="props">
                  {{ props.row.metadata.target }}
                </b-table-column>
                <b-table-column field="success" label="Success" sortable v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column field="error" label="Error" sortable v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br>
            </div>
            <div v-if="detailsModal.soh.processes">
              <p class="title is-5">Processes</p>
              <b-table
                :data="detailsModal.soh.processes"
                default-sort="timestamp">
                <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column field="process" label="Process" sortable v-slot="props">
                  {{ props.row.metadata.proc }}
                </b-table-column>
                <b-table-column field="success" label="Success" sortable v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column field="error" label="Error" sortable v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br>
            </div>
            <div v-if="detailsModal.soh.listeners">
              <p class="title is-5">Listeners</p>
              <b-table
                :data="detailsModal.soh.listeners"
                default-sort="timestamp">
                <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column field="port" label="Port" sortable v-slot="props">
                  {{ props.row.metadata.port }}
                </b-table-column>
                <b-table-column field="success" label="Success" sortable v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column field="error" label="Error" sortable v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br>
            </div>
            <div v-if="detailsModal.soh.customTests">
              <p class="title is-5">Custom User Tests</p>
              <b-table
                :data="detailsModal.soh.customTests"
                default-sort="test">
                <b-table-column field="test" label="Test Name" sortable v-slot="props">
                  {{ props.row.metadata.test }}
                </b-table-column>
                <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column field="success" label="Success" sortable v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column field="error" label="Error" sortable v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br>
            </div>
          </template>
          <template v-else>
            <p>There is no state of health data available for {{ detailsModal.vm }}.</p>
          </template>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <template v-if="detailsModal.status.toLowerCase() == 'running'">
            <a :href="vncLoc(detailsModal.vm)" target="_blank">
              <b-tooltip label="open vnc for a running vm" type="is-light is-left" :delay="800">
                <b-button type="is-success">
                  <b-icon icon="tv" />
                </b-button>
              </b-tooltip>
            </a>
          </template>
          <template v-else>
            <b-tooltip label="vnc is only available on a running vm" type="is-light is-left" :delay="600">
              <b-button type="is-danger" disabled>
                <b-icon icon="tv" />
              </b-button>
            </b-tooltip>
          </template>
        </footer>
      </div>
    </b-modal>
    <hr>
    <div class="columns is-centered"> 
      <div class="column is-1">
        <router-link class="button is-dark" :to="{ name: 'experiment', params: { id: this.$route.params.id }}">
          <b-tooltip label="return to the experiment component" type="is-light is-right" :delay="1000">
            <b-icon icon="history"></b-icon>
          </b-tooltip>
        </router-link>
      </div>
      <div class="column has-text-centered">
        <span style="font-weight: bold; font-size: x-large;">State of Health for Experiment: {{ this.$route.params.id }}</span>
      </div>
      <div class="column is-1" />
    </div>
    <div>
      <b-tabs>
        <b-tab-item label="Topology Graph">
          <div class="columns is-vcentered">
            <div class="column" />
            <div class="column">
              <b-radio v-model="radioButton" native-value="running" type="is-light">Running</b-radio>
            </div>
            <div class="column">
              <b-radio v-model="radioButton" native-value="notrunning" type="is-light">Not running</b-radio>
            </div>
            <div class="column">
              <b-radio v-model="radioButton" native-value="notboot" type="is-light">Not booted</b-radio>
            </div>
            <div class="column">
              <b-radio v-model="radioButton" native-value="notdeploy" type="is-light">Not deployed</b-radio>
            </div>
            <div class="column">
              <b-button @click="resetNetwork" type="is-light">Refresh Network</b-button>
            </div>
            <div class="column">
              <div v-if="!running">
                <b-button type="is-light" disabled>Exp Not Running</b-button>
              </div>
              <div v-else-if="sohRunning">
                <div v-if="sohInitialized">
                  <b-button type="is-light" disabled>SOH Is Running</b-button>
                </div>
                <div v-else>
                  <b-button type="is-light" disabled>SOH Is Initializing</b-button>
                </div>
              </div>
              <div v-else-if="!sohInitialized">
                <b-button type="is-light" disabled>SOH Not Initialized</b-button>
              </div>
              <div v-else>
                <b-button @click="execSoH" type="is-light">Run SOH</b-button>
              </div>
            </div>
            <div class="column" />
          </div>
          <div style="margin-top: 10px; border: 2px solid whitesmoke; background: #333;">
            <div v-if="nodes == null">
              <section class="hero is-light is-bold is-large">
                <div class="hero-body">
                  <div class="container" style="text-align: center">
                    <h1 class="title">
                      There are no nodes matching your search criteria!
                    </h1>
                      <b-button type="is-success" outlined @click="resetNetwork()">Refresh Network</b-button>
                  </div>
                </div>
              </section>
            </div>
            <div v-else id="graph"></div>
          </div>
          <br>
          <div class="columns is-vcentered">
            <div class="column" />
            <div class="column is-one-fifth">
              <div class="columns is-variable is-1">
                <div class="column has-text-right">
                  <img :src="vlan" style="width:20px;height:20px;" />
                </div>
                <div class="column">
                  <span style="color: whitesmoke;">VLAN Segment</span>
                </div>
              </div>
            </div>
            <div class="column is-one-fifth">
              <div class="columns is-variable is-1">
                <div class="column has-text-right">
                  <b-icon icon="circle" style="color: #4F8F00" />
                </div>
                <div class="column">
                  <span style="color: whitesmoke;">Running</span>
                </div>
              </div>
            </div>
            <div class="column is-one-fifth">
              <div class="columns is-variable is-1">
                <div class="column has-text-right">
                  <b-icon icon="circle" style="color: #941100" />
                </div>
                <div class="column">
                  <span style="color: whitesmoke;">Not running</span>
                </div>
              </div>
            </div>
            <div class="column" />
          </div>
          <div class="columns is-vcentered">
            <div class="column" />
            <div class="column is-one-fifth">
              <div class="columns is-variable is-1">
                <div class="column has-text-right">
                  <b-icon icon="circle" style="color: black" />
                </div>
                <div class="column">
                  <span style="color: whitesmoke;">Not booted</span>
                </div>
              </div>
            </div>
            <div class="column is-one-fifth">
              <div class="columns is-variable is-1">
                <div class="column has-text-right">
                  <b-icon icon="circle" style="color: #FFD479" />
                </div>
                <div class="column">
                  <span style="color: whitesmoke;">Not deployed</span>
                </div>
              </div>
            </div>
            <div class="column is-one-fifth">
              <div class="columns is-variable is-1">
                <div class="column has-text-right">
                  <b-icon icon="circle" style="color: #005493" />
                </div>
                <div class="column">
                  <span style="color: whitesmoke;">External</span>
                </div>
              </div>
            </div>
            <div class="column" />
          </div>
        </b-tab-item>
        <b-tab-item v-if="flows" label="Network Volume">
          <div style="margin-top: 10px; border: 2px solid whitesmoke; background: #333;">
            <div id="chord"></div>
          </div>
        </b-tab-item>
        <b-tab-item v-if="messages" label="SoH Messages">
          <div class="columns is-centered is-multiline">
            <div v-for="( n, index ) in nodes" :key="index">
              <div class="column is-one-half">
                <div v-if="n.soh">
                  <h3 class="title is-3">{{ n.label }}</h3>
                  <div>
                    <h3 class="title is-5">CPU Load: {{ n.soh.cpuLoad }}</h3>
                  </div>
                  <br>
                  <div v-if="n.soh.reachability">
                    <h3 class="title is-5">Reachability</h3>
                    <b-table
                      :data="n.soh.reachability"
                      default-sort="host">
                      <b-table-column field="hostname" label="Host" sortable v-slot="props">
                        {{ props.row.hostname }}
                      </b-table-column>
                      <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                        {{ props.row.timestamp }}
                      </b-table-column>
                      <b-table-column field="error" label="Error" sortable v-slot="props">
                        {{ props.row.error }}
                      </b-table-column>
                    </b-table>
                    <br>
                  </div>
                  <div v-if="n.soh.processes">
                    <h3 class="title is-5">Processes</h3>
                    <b-table
                      :data="n.soh.processes"
                      default-sort="process">
                      <b-table-column field="process" label="Process" sortable v-slot="props">
                        {{ props.row.process }}
                      </b-table-column>
                      <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                        {{ props.row.timestamp }}
                      </b-table-column>
                      <b-table-column field="error" label="Error" sortable v-slot="props">
                        {{ props.row.error }}
                      </b-table-column>
                    </b-table>
                    <br>
                  </div>
                  <div v-if="n.soh.listeners">
                    <h3 class="title is-5">Listeners</h3>
                    <b-table
                      :data="n.soh.listeners"
                      default-sort="listener">
                      <b-table-column field="listener" label="Listener" sortable v-slot="props">
                        {{ props.row.listener }}
                      </b-table-column>
                      <b-table-column field="timestamp" label="Timestamp" sortable v-slot="props">
                        {{ props.row.timestamp }}
                      </b-table-column>
                      <b-table-column field="error" label="Error" sortable v-slot="props">
                        {{ props.row.error }}
                      </b-table-column>
                    </b-table>
                    <br>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </b-tab-item>
      </b-tabs>
    </div>
  </div>
</template>

<script>
import * as d3 from "d3";

import Linux    from "@/assets/linux.svg";
import CentOS   from "@/assets/centos.svg";
import RedHat   from "@/assets/redhat.svg";
import Windows  from "@/assets/windows.svg";
import Router   from "@/assets/router.svg";
import Firewall from "@/assets/firewall.svg";
import Printer  from "@/assets/printer.svg";
import VLAN     from "@/assets/vlan.svg";

export default {
  async beforeDestroy () {
    this.$options.sockets.onmessage = null;
  },

  async created () {
    this.$options.sockets.onmessage = this.handler;
    await this.updateNetwork();
    this.generateGraph();
    this.generateChord();
  },

  methods: {
    handler ( event ) {
      event.data.split( /\r?\n/ ).forEach( m => {
        let msg = JSON.parse( m );
        this.handle( msg );
      });
    },

    handle ( msg ) {
      switch ( msg.resource.type ) {
        case 'experiment': {
          if ( msg.resource.name != this.$route.params.id ) {
            return;
          }

          switch ( msg.resource.action ) {
            case 'stop': {
              this.resetNetwork();
              break;
            }

            case 'start': {
              this.resetNetwork();
              break;
            }
          }
        }

        case 'experiment/apps': {
          if ( msg.resource.name != this.$route.params.id ) {
            return;
          }

          switch ( msg.resource.action ) {
            case 'triggered': {
              if (msg.result && msg.result.app && msg.result.app === 'soh') {
                this.sohRunning = true;
                break;
              }
            }

            case 'triggerSuccess': {
              if (msg.result && msg.result.app && msg.result.app === 'soh') {
                this.resetNetwork();
                this.sohRunning = false;

                break;
              }
            }

            case 'triggerError': {
              if (msg.result && msg.result.app && msg.result.app === 'soh') {
                this.$buefy.toast.open ({
                  message: 'Triggering State of Health update failed.',
                  type: 'is-danger',
                });

                this.sohRunning = false;

                break;
              }
            }
          }
        }

        case 'experiment/vm': {
          // exp_name/vm_name
          let resource = msg.resource.name.split( '/' );
          let expName  = resource[0];
          let vmName   = resource[1];

          // Ignore this broadcast if it's not for this experiment.
          if ( expName != this.$route.params.id ) {
            return;
          }

          switch ( msg.resource.action ) {
            case 'stop': {
              for ( let i = 0; i < this.nodes.length; i++ ) {
                if ( this.nodes[i].label == vmName ) {
                  this.nodes[i].status = 'notrunning';
                  d3.selectAll('circle').attr( "fill", this.updateNodeColor );
                }
              }

              break;
            }
            case 'start': {
              for ( let i = 0; i < this.nodes.length; i++ ) {
                if ( this.nodes[i].label == vmName ) {
                  this.nodes[i].status = 'running';
                  d3.selectAll('circle').attr( "fill", this.updateNodeColor );
                }
              }
              
              break;
            }
            case 'delete': {
              for ( let i = 0; i < this.nodes.length; i++ ) {
                if ( this.nodes[i].label == vmName ) {
                  this.nodes[i].status = 'notdeploy';
                  d3.selectAll('circle').attr( "fill", this.updateNodeColor );
                }
              }
              
              break;
            }
          }
        }
      }
    },

    async updateNetwork ( filter = '' ) {
      let url = 'experiments/' + this.$route.params.id + '/soh';

      if ( filter ) {
        url = url + '?statusFilter=' + filter;
      }

      try {
        let resp = await this.$http.get( url );
        let state = await resp.json();

        this.running        = state.started;
        this.sohInitialized = state.soh_initialized;
        this.sohRunning     = state.soh_running;

        this.nodes = state.nodes;
        this.edges = state.edges;

        if ( state.host_flows != null ) {
          this.volume = Object.assign(
            state.host_flows,
            { names: state.hosts }
          )
          this.flows = true;
        } 
      } catch (err) {
        this.errorNotification(err);
      } finally {
        this.isWaiting = false;
      }
    },

    updateNodeImage( node ) {
      return "url(#" + node.image.toLowerCase() + ")";
    },

    updateNodeBorder( node ) {
      if ( node.soh && node.soh.errors ) {
        return '#FF9900'; // orange
      }

      return this.updateNodeColor( node );
    },

    updateNodeColor( node ) {
      const colors = {
        "running":    "#4F8F00", // green
        "notrunning": "#941100", // red
        "notboot":    "black",
        "notdeploy":  "#FFD479", // yellow
        "external":   "#005493", // blue
      }

      if (node.status === "external") {
        return colors[node.status];
      }

      if ( !this.running ) {
          if ( node.status.toLowerCase() == "ignore" ) {
          return "url(#switch)";
        }

        return;
      }

      if ( node.status.toLowerCase() == "ignore" ) {
        return "url(#switch)";
      }

      return colors[ node.status ];
    },

    updateEdgeColor( edge ) {
      if ( edge.type == "serial" ) {
        return '#A020F0' // purple
      }

      return '#999';
    },

    generateGraph () {
      if ( this.nodes == null ) {
        return;
      }

      const nodes = this.nodes.map( d => Object.create( d ) );
      // const links = this.edges.map( d => Object.create( d ) );

      const links = this.edges.filter( (d) => {
        switch ( this.showEdgeType ) {
          case 'all': {
            return true;
          }

          case 'network': {
            return d.type !== 'serial';
          }

          case 'serial': {
            return d.type === 'serial';
          }
        }
      }, this);

      const width = 600;
      const height = 400;

      const simulation = d3.forceSimulation( nodes )
        .force( "link", d3.forceLink( links ).id( d => d.id ) )
        .force( "charge", d3.forceManyBody() )
        .force( "center", d3.forceCenter( width / 2, height / 2 ) );

      d3.select( "#graph" ).select( "svg" ).remove();

      const svg = d3.select( "#graph" ).append( "svg" )
        .attr( "viewBox", [ 0, 0, width, height ] );

      const g = svg.append( "g" );

      svg.call( d3.zoom()
        .extent( [ [ 0, 0 ], [ width, height ] ] )
        .scaleExtent([  -5, 5 ] )
        .on( "zoom", function ( { transform } ) {
          g.attr( "transform", transform );
        })
      );

      const link = g.append( "g" )
        .selectAll( "line" )
        .data( links )
        .join( "line" )
        .attr( "stroke-opacity", 0.6 )
        .attr( "stroke", this.updateEdgeColor )
        .attr( "stroke-width", d => Math.sqrt( d.value ) );

      const defs = svg.append( "svg:defs" );

      defs.append( "svg:pattern" )
        .attr( "id", "linux" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", Linux )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "centos" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", CentOS )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "rhel" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", RedHat )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "windows" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", Windows )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "router" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", Router )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "firewall" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", Firewall )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "printer" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", Printer )
        .attr( "width", 30 )
        .attr( "height", 30 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      defs.append( "svg:pattern" )
        .attr( "id", "switch" )
        .attr( "width", 50 )
        .attr( "height", 50 )
        .append( "svg:image" )
        .attr( "xlink:href", VLAN )
        .attr( "width", 10 )
        .attr( "height", 10 )
        .attr( "x", 0 )
        .attr( "y", 0 );

      const node = g.append( "g" )
        .selectAll( "circle" )
        .data( nodes )
        .join( "circle" )
        .attr( "class", "circle" )
        .attr( "stroke", this.updateNodeBorder )
        .attr( "stroke-width", 1.5 )
        .attr( "r", 5 )
        .attr( "fill", this.updateNodeColor )
        .attr( "width", 5 )
        .attr( "height", 5 )
        .on( 'mouseenter', this.entered )
        .on( 'mouseleave', this.exited )
        .on( 'click', this.clicked )
        .call( this.drag( simulation ) );

      const label = g.selectAll( "text" )
        .data( nodes )
        .join( "text" )
          .text( d => d.label )
          .style( "text-anchor", "start" )
          .style( "fill", "whitesmoke" )
          .style( "font-size", "6px" );

      simulation.on( "tick", () => {
        link
          .attr( "x1", d => d.source.x )
          .attr( "y1", d => d.source.y )
          .attr( "x2", d => d.target.x )
          .attr( "y2", d => d.target.y );

        node
          .attr( "cx", d => d.x )
          .attr( "cy", d => d.y );

        label
          .attr( "x", d => d.x + 4 )
          .attr( "y", d => d.y + 8 );
      });
    },

    entered ( e, n ) {
      if ( !n.image || n.image.toLowerCase() == "switch" ) {
        return;
      }

      let circle = d3.select( e.target );
      
      circle
        .transition()
        .attr( "r", 15 )
        .attr( "fill", () => this.updateNodeImage( n ) );
    },

    exited ( e, n ) {
      let circle = d3.select( e.target );

      circle
        .transition()
        .attr( "r", 5 )
        .attr( "fill", () => this.updateNodeColor( n ) );
    },

    clicked ( e, n ) {
      if ( n.image.toLowerCase() == "switch" ) {
        return;
      }

      if ( n.status.toLowerCase() == 'notboot' ) {
        this.detailsModal.active = true;
      } else {
        this.detailsModal.active = true;
        this.detailsModal.vm = n.label;
        this.detailsModal.status = n.status;
        this.detailsModal.soh = n.soh;
      }    
    },

    color ( d ) {
      const scale = d3.scaleOrdinal( d3.schemeCategory10 );
      return d => scale( d.group );
    },

    drag ( simulation ) {
      function dragstarted ( event ) {
        if ( !event.active ) simulation.alphaTarget( 0.3 ).restart();
        event.subject.fx = event.subject.x;
        event.subject.fy = event.subject.y;
      }
      
      function dragged ( event ) {
        event.subject.fx = event.x;
        event.subject.fy = event.y;
      }
      
      function dragended ( event ) {
        if ( !event.active ) simulation.alphaTarget( 0 );
        event.subject.fx = null;
        event.subject.fy = null;
      }
      
      return d3.drag()
        .on( "start", dragstarted )
        .on( "drag", dragged )
        .on( "end", dragended );
    },

    generateChord () {
      const names = this.volume.names === undefined ? d3.range(this.volume.length) : this.volume.names;

      const tickStep = d3.tickStep(0, d3.sum(this.volume.flat()), 100);
      const formatValue = d3.format(".1~%");

      const height = 900;
      const width = 900;

      const innerRadius = Math.min(width, height) * .35;
      const outerRadius = innerRadius * 1.018;
      
      const chord = d3.chord()
        .padAngle(10 / innerRadius)
        .sortSubgroups(d3.descending)
        .sortChords(d3.descending);
      
      const arc = d3.arc()
        .innerRadius(innerRadius)
        .outerRadius(outerRadius);
      
      const ribbon = d3.ribbon()
        .radius(innerRadius - 1)
        .padAngle(1 / innerRadius);

      const color = d3.scaleOrdinal(d3.schemeCategory10);

      function ticks (startAngle, endAngle, value) {
        const k = (endAngle - startAngle) / value;
        return d3.range(0, value, tickStep).map(value => {
          return {value, angle: value * k + startAngle};
        });
      }

      d3.select( "#chord" ).select( "svg" ).remove();

      const svg = d3.select("#chord").append("svg")
        .attr("viewBox", [-width / 2, -height / 2, width, height]);

      const chords = chord(this.volume);

      const group = svg.append("g")
        .attr("font-size", 10)
        .attr("font-family", "sans-serif")
        .selectAll("g")
        .data(chords.groups)
        .join("g");
      
      group.append("path")
        .attr("fill", d => color(names[d.index]))
        .attr("stroke", d => color(names[d.index]))
        .attr("d", arc);

      group.append("text")
        .each(d => { d.angle = (d.startAngle + d.endAngle) / 2 ; })
        .attr("dy", ".35em")
        .attr("transform", d => `
          rotate(${(d.angle * 180 / Math.PI - 90 )})
          translate(${innerRadius + 18})
          ${d.angle > Math.PI ? "rotate(180)" : "" }`)
        .attr("text-anchor", d => d.angle > Math.PI ? "end" : null)
        .text(d => `${names[d.index]}
          ${formatValue(d.value)}`);

      const groupTick = group.append("g")
        .selectAll("g")
        .data(ticks)
        .join("g")
        .attr("transform", d => `rotate(${d.angle * 180 / Math.PI - 90}) translate(${outerRadius},0)`);

      groupTick.append("line")
        .attr("stroke", "currentColor")
        .attr("x2", 6);

      groupTick.append("text")
        .attr("x", 8)
        .attr("dy", "0.35em")
        .attr("fill", "whitesmoke")
        .attr("transform", d => d.angle > Math.PI ? "rotate(180) translate(-16)" : null)
        .attr("text-anchor", d => d.angle > Math.PI ? "end" : null)
        .text(d => formatValue(d.value));

      group.select("text")
        .attr("font-weight", "bold")
        .attr("fill", "whitesmoke")
        .text(function(d) {
          return this.getAttribute("text-anchor") === "end"
            ? `↑ ${names[d.index]}`
            : `${names[d.index]} ↓`;
        });
      
      svg.append("g")
        .attr("fill-opacity", 0.8)
        .selectAll("path")
        .data(chords)
        .join("path")
        .style("mix-blend-mode", "multiply")
        .attr("stroke", d => d3.rgb(color(names[d.source.index])).darker()) // might want to drop
        .attr("fill", d => color(names[d.source.index]))
        .attr("d", ribbon)
        .append("title")
        .text(d => `${names[d.target.index]} \t➔\t ${names[d.source.index]} \t ${d3.format(".5s")(d.source.value)}${d.source.index === d.target.index ? "" : `\n${names[d.source.index]} \t➔\t ${names[d.target.index]} \t ${d3.format(".5s")(d.target.value)}`}`)
    },

    async resetNetwork () {
      this.radioButton = '';
      await this.updateNetwork();
      this.generateGraph();
      this.generateChord();
    },

    resetDetailsModal () {
      this.detailsModal = {
        active: false,
        vm: '',
        status: '',
        soh: null
      }
    },

    async execSoH () {
      let url = 'experiments/' + this.$route.params.id + '/trigger?apps=soh';

      try {
        await this.$http.post( url );
      } catch (err) {
        this.errorNotification(err);
      }
    },

    vncLoc (vm) {
      return this.$router.resolve({name: 'vnc', params: {id: this.$route.params.id, name: vm, token: this.$store.getters.token}}).href;
    }
  },

  watch: {
    radioButton: async function ( filter ) {
      if ( filter != '' ) {
        await this.updateNetwork( filter );
        this.generateGraph();
        this.generateChord();
      }
    }
  },

  data() {
    return {
      running: false,
      sohInitialized: false,
      sohRunning: false,
      messages: false,
      flows: false,
      volume: [],
      nodes: [],
      edges: [],
      showEdgeType: 'all',
      radioButton: '',
      vlan: VLAN,
      detailsModal: {
        active: false,
        vm: '',
        status: '',
        soh: null
      },
      chordData: null,
    };
  }
}
</script>

<style scoped>
  label.radio:hover {
    color: whitesmoke;
  }

  .modal-card-head {
    background-color: #686868;
  }

  .modal-card-title {
    color: whitesmoke;
  }
</style>