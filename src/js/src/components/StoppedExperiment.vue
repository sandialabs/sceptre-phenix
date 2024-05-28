<template>
  <div class="content">
    <b-modal :active.sync="expModal.active" has-modal-card>
      <div class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ expModal.vm.name ? expModal.vm.name : "unknown" }}</p>
        </header>
        <section class="modal-card-body">
          <p>Host: {{ expModal.vm.host }}</p>
          <p>IPv4: {{ expModal.vm.ipv4 | stringify }}</p>
          <p>CPU(s): {{ expModal.vm.cpus }}</p>
          <p>Memory: {{ expModal.vm.ram | ram }}</p>
          <p>Disk: {{ expModal.vm.disk }}</p>
          <p>Uptime: {{ expModal.vm.uptime | uptime }}</p>
          <p>Network(s): {{ expModal.vm.networks | stringify | lowercase }}</p>
          <p>Taps: {{ expModal.vm.taps | stringify | lowercase }}</p>
        </section>
        <footer class="modal-card-foot">
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="vlanModal.active" has-modal-card>
      <div class="modal-card" style="width:25em; height:30em">
        <header class="modal-card-head">
          <p class="modal-card-title">VLAN Assignments for <br> {{ this.$route.params.id }} Experiment</p>
        </header>
        <section class="modal-card-body">
          <div v-for="( vlan, index ) in vlanModal.vlans" :key="index">
            <table>
              <tr>
                <td style="width:50%"><font color="#202020">{{ vlan.alias }}</font></td>
                <td><b-numberinput min="0" max="4094" type="is-light" size="is-small" controls-alignment="right" controls-position="compact" v-model=vlan.vlan /></td>
              </tr>
            </table>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="updateVLANs">Update VLAN(s)</button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="fileViewerModal.active" :on-cancel="resetFileViewerModal" has-modal-card>
      <div class="modal-card" style="width:50em">
        <header class="modal-card-head x-modal-dark">
          <p class="modal-card-title x-config-text">{{ fileViewerModal.title }}</p>
        </header>
        <section class="modal-card-body x-modal-dark">
          <div class="control">
            <textarea class="textarea x-config-text has-fixed-size" rows="30" v-model="fileViewerModal.contents" readonly />
          </div>
        </section>
        <footer class="modal-card-foot x-modal-dark buttons is-right">
          <button class="button is-dark" @click="resetFileViewerModal">
            Exit
          </button>
        </footer>
      </div>
    </b-modal>
    <hr>
    <div class="level is-vcentered">
      <div class="level-item">
        <span style="font-weight: bold; font-size: x-large;">Experiment: {{ this.$route.params.id }}</span>&nbsp;
      </div>
      <div class="level-item" v-if="experiment.scenario">
        <span style="font-weight: bold;">Scenario: {{ experiment.scenario }}</span>&nbsp;
      </div>
      <div class="level-item" v-if="experiment.scenario">
        <span style="font-weight: bold;">Apps:</span>&nbsp;
        <b-taglist>
          <b-tag v-for="( a, index ) in experiment.apps" :key="index" type="is-light">
            {{ a }}  
          </b-tag>
        </b-taglist>
      </div>
    </div>
    <b-field v-if="roleAllowed('experiments', 'get', experiment.name)" position="is-right">
      <template v-if="selectedRows.length != 0">
        <b-tooltip label="Set to Boot" type="is-light">
          <p class="control">
            <b-button v-if="selectedRows.every(vm => roleAllowed('vms', 'patch', experiment.name + '/' + vm.name))" class="button is-light" slot="trigger" icon-right="bolt" style="color: #c46200;" @click="setBoot( false )" />
          </p>
        </b-tooltip>
        &nbsp; &nbsp;
        <b-tooltip label="Set to Do Not Boot" type="is-light">
          <p class="control">
            <b-button v-if="selectedRows.every(vm => roleAllowed('vms', 'patch', experiment.name + '/' + vm.name))" class="button is-light" slot="trigger" icon-right="bolt" style="color: #ffffff;" @click="setBoot( true )" />
          </p>
        </b-tooltip>
        &nbsp; &nbsp;
      </template>
      <b-tooltip label="assign VLAN ID to alias" type="is-light">
        <button class='button is-light' @click="vlanModal.active = true">
          <b-icon icon="network-wired"></b-icon>
        </button>
      </b-tooltip>
      &nbsp; &nbsp;
      <template v-if="this.activeTab == 1">
        <b-tooltip label="search on a specific category" type="is-light">
          <b-select :value="filesTable.category" @input="( value ) => assignCategory( value )" placeholder="All Categories">
            <option v-for="( category, index ) in filesTable.categories" :key="index" :value=category>{{ category }}</option>
          </b-select>
        </b-tooltip>
        &nbsp;
      </template>
      <b-autocomplete
        v-model="searchName"
        :placeholder="searchPlaceholder"
        icon="search"
        :data="searchHistory"
        @typing="searchVMs"
        @select="option => searchVMs(option)">   
          <template slot="empty">No results found</template>
      </b-autocomplete>
      <p class='control'>
         <button class='button' style="color:#686868" @click="searchVMs(''); filesTable.category = null">
          <b-icon icon="window-close"></b-icon>
        </button>
      </p>
      &nbsp; &nbsp;
      <p class="control">
        <b-button v-if="roleAllowed('experiments/start', 'update', experiment.name)" class="button is-success" slot="trigger" icon-right="play" @click="start"></b-button>
      </p>
      &nbsp; &nbsp;
      <p class="control">
        <b-tooltip label="menu for scheduling hosts to the experiment" type="is-light" multilined>
          <b-dropdown v-model="algorithm" class="is-right" aria-role="list">
            <button class="button is-light" slot="trigger">
              <b-icon icon="bars"></b-icon>
            </button>
            <b-dropdown-item v-for="( s, index ) in schedules" :key="index" :value="s" @click="updateSchedule">
              <font color="#202020">{{ s }}</font>
            </b-dropdown-item>
          </b-dropdown>
        </b-tooltip>
        &nbsp;
        <router-link v-if="roleAllowed('experiments', 'get', experiment.name)" class="button is-light" :to="{ name: 'soh', params: { id: this.$route.params.id }}">
          <b-icon icon="heartbeat"></b-icon>
        </router-link>
      </p>  
    </b-field>
    <div style="margin-top: -4em;">
      <b-tabs @input="tabsSwitched()" v-model="activeTab">
        <b-tab-item label="VMs" icon="desktop">      
          <b-table
            :key="table.key"
            :data="experiment.vms"
            :paginated="table.isPaginated"
            backend-pagination
            :total="table.total"
            :per-page="table.perPage"
            :current-page.sync="table.currentPage"
            @page-change="onPageChange"
            :pagination-simple="table.isPaginationSimple"
            :pagination-size="table.paginationSize"
            backend-sorting
            :default-sort-direction="table.defaultSortDirection"
            default-sort="name"
            @sort="onSort"
            ref="vmTable">
              <template slot="empty">
                <section class="section">
                  <div class="content has-text-white has-text-centered">
                    Your search turned up empty!
                  </div>
                </section>
              </template>
              <b-table-column field="multiselect" label="" >              
                <template v-slot:header="{ column }">
                  <b-tooltip label="Select/Unselect All" type="is-dark">
                  <b-checkbox @input="selectAllVMs" v-model="checkAll" type="is-primary"/>
                  </b-tooltip>
                </template>
                <template v-slot:default="props">
                  <div>
                    <b-checkbox :disabled="props.row.external" v-model="selectedRows" :native-value=props.row.name type="is-primary"/>
                  </div>
                </template>                  
              </b-table-column>
              <b-table-column field="name" label="Node" sortable v-slot="props">
                <template v-if="!props.row.external && roleAllowed('vms', 'get', experiment.name + '/' + props.row.name)">
                  <b-tooltip label="get info on the vm" type="is-dark">
                    <div class="field">
                      <div @click="expModal.active = true; expModal.vm = props.row">
                        {{ props.row.name }}
                      </div>
                    </div>
                  </b-tooltip>
                </template>
                <template v-else>
                  {{ props.row.name }}
                </template>
              </b-table-column>
              <b-table-column field="host" label="Host" width="200" sortable v-slot="props">
                <template v-if="!props.row.external && roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                  <b-tooltip label="assign the vm to a specific host" type="is-dark">
                    <b-field>
                      <b-select :value="props.row.host" expanded @input="( value ) => assignHost( props.row.name, value )">
                        <option
                          v-for="( h, index ) in hosts"
                          :key="index"
                          :value="h">
                          {{ h }}
                        </option>
                      </b-select>
                      <p class='control'>
                        <button class='button' 
                                @click="unassignHost( props.row.name, props.row.host )">
                          <b-icon icon="window-close"></b-icon>
                        </button>
                      </p>
                    </b-field>
                  </b-tooltip>
                </template>
                <template v-else-if="props.row.external">
                  EXTERNAL 
                </template>
                <template v-else>
                  {{ props.row.host }}
                </template>
              </b-table-column>
              <b-table-column field="ipv4" label="IPv4" v-slot="props">
                <div v-for="(ip,index) in props.row.ipv4" :key="index">
                  {{ ip || 'unknown' }}
                </div>
              </b-table-column>
              <b-table-column field="cpus" label="CPUs" width="100" sortable centered v-slot="props">
                <template v-if="!props.row.external && roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                  <b-tooltip label="menu for assigning vm(s) cpus" type="is-dark">
                    <b-select :value="props.row.cpus" expanded @input="( value ) => assignCpu( props.row.name, value )">
                      <option value="1">1</option>
                      <option value="2">2</option>
                      <option value="3">3</option>
                      <option value="4">4</option>
                    </b-select>
                  </b-tooltip>
                </template>
                <template v-else>
                  {{ props.row.cpus || 'unknown' }}
                </template>
              </b-table-column>
              <b-table-column field="ram" label="Memory" width="112" sortable centered v-slot="props">
                <template v-if="!props.row.external && roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                  <b-tooltip label="menu for assigning vm(s) memory" type="is-dark">
                    <b-select :value="props.row.ram" expanded @input="( value ) => assignRam( props.row.name, value )">
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
                </template>
                <template v-else>
                  {{ props.row.ram || 'unknown' }}
                </template>
              </b-table-column>
              <b-table-column field="disk" label="Disk" v-slot="props">
                <template v-if="!props.row.external && roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                  <b-tooltip :label="getDiskToolTip(props.row.disk)" type="is-dark">
                    <b-select :value="props.row.disk" expanded @input="( value ) => assignDisk( props.row.name, value )">
                      <option
                        v-for="( d, index ) in disks"
                        :key="index"
                        :value="d">
                          {{ getBaseName(d) }}
                      </option>
                    </b-select>
                  </b-tooltip>
                </template>
                <template v-else>
                  {{ getBaseName(props.row.disk) || 'unknown' }}
                </template>
              </b-table-column>
              <b-table-column field="inject_partition" label="Partition" sortable centered v-slot="props">
                  <template v-if="!props.row.external && roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                    <b-tooltip label="menu for assigning inject partition" type="is-dark">
                      <b-select :value="props.row.inject_partition" expanded @input="( value ) => assignPartition( props.row.name, value )">
                        <option v-for="n in 10" :value="n">{{ n }}</option>
                      </b-select>
                    </b-tooltip>
                  </template>
                  <template v-else>
                    {{ props.row.inject_partition }}
                  </template>
              </b-table-column>
              <b-table-column label="Boot" centered v-slot="props">
                <template v-if="roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                  <b-tooltip :label="getBootLabel( props.row )" type="is-dark">
                    <div @click="updateDnb( props.row )">
                      <font-awesome-icon :class="bootDecorator( props.row )" icon="bolt" />
                    </div>
                  </b-tooltip>
                </template>
              </b-table-column>
              <b-table-column label="Persistence" centered v-slot="props">
                <template v-if="roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name)">
                  <b-tooltip :label="getSnapshotLabel( props.row )" type="is-dark">
                    <div>
                      <b-select :value="props.row.snapshot" expanded @input="( value ) => updateSnapshot( props.row.name, value )">
                      <option value=true>Non-Persistent</option>
                      <option value=false>Persistent</option>
                    </b-select>
                   
                    </div>
                  </b-tooltip>
                </template>
              </b-table-column>
          </b-table>
          <br>
          <b-field v-if="paginationNeeded" grouped position="is-right">
            <div class="control is-flex">
              <b-switch v-model="table.isPaginated" @input="updateExperiment(); changePaginate();" size="is-small" type="is-light">Paginate</b-switch>
            </div>
          </b-field>
        </b-tab-item>
        <b-tab-item label="Files" icon="file-alt">
          <b-table            
            :data="files"
            :paginated="filesTable.isPaginated  && filesPaginationNeeded"
            backend-pagination
            :total="filesTable.total"
            :per-page="filesTable.perPage"
            :current-page.sync="filesTable.currentPage"  
            @page-change="onFilesPageChange"   
            :pagination-simple="filesTable.isPaginationSimple"
            :pagination-size="filesTable.paginationSize"  
            backend-sorting
            :default-sort-direction="filesTable.defaultSortDirection"
            default-sort="date"
            @sort="onFilesSort">
            <template slot="empty">
              <section class="section">
                <div class="content has-text-white has-text-centered">
                  No Files Are Available!
                </div>
              </section>
            </template>
            <b-table-column field="name" label="Name" sortable v-slot="props">                  
              <template v-if="props.row.plainText">
                <b-tooltip label="view file" type="is-dark">
                  <div class="field">
                    <div @click="viewFile( props.row )">
                      {{ props.row.name }}
                    </div>
                  </div>
                </b-tooltip>
              </template>
              <template v-else>
                {{ props.row.name }}
              </template>
            </b-table-column>
            <b-table-column field="path" label="Path" centered v-slot="props">
              <b-tooltip :label="'/phenix/images/' + experiment.name + '/files/' + props.row.path" type="is-dark">
                <b-icon icon="info-circle" size="is-small" />
              </b-tooltip>
            </b-table-column>
            <b-table-column field="categories" label="Category" v-slot="props">
              <b-taglist>
                <b-tag v-for="( c, index ) in props.row.categories" :key="index" type="is-light">{{ c }}</b-tag>
              </b-taglist>
            </b-table-column>
            <b-table-column field="date" label="Date" sortable centered v-slot="props">                  
              {{ props.row.date }}                      
            </b-table-column>
            <b-table-column field="size" label="Size" sortable centered v-slot="props">                  
              {{ props.row.size | fileSize }}                      
            </b-table-column>
            <b-table-column field="actions" label="Actions" centered v-slot="props">
              <a :href="fileDownloadURL(props.row.name, props.row.path)" target="_blank">
                <b-icon icon="file-download" size="is-small"></b-icon>
              </a>
            </b-table-column>
          </b-table>
          <br>
          <b-field v-if="filesPaginationNeeded" grouped position="is-right">
            <div class="control is-flex">
              <b-switch v-model="filesTable.isPaginated" @input="updateFiles(); changeFilesPaginate();" size="is-small" type="is-light">Paginate</b-switch>
            </div>
          </b-field>
        </b-tab-item>
      </b-tabs>
    </div>
    <b-loading :is-full-page="true" :active="isWaiting" :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import _ from 'lodash';

  export default {
    beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },

    async created () {
      this.$options.sockets.onmessage = this.handler;
      this.updateExperiment();
    },

    computed: {
      vms: function() {
        let vms = this.experiment.vms;
        
        var name_re = new RegExp( this.searchName, 'i' );
        var data = [];
        
        for ( let i in vms ) {
          let vm = vms[ i ];
          if ( vm.name.match( name_re ) ) {
            data.push( vm );
          }
        }
        
        return vms;
      },

      filteredData () {
        if (this.experiment.length == 0) {
          return []
        }
        
        let names = this.experiment.vms.map( vm => { return vm.name; } );
        
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
        var user = localStorage.getItem( 'user' );

        if ( localStorage.getItem( user + '.lastPaginate' ) ) {
          this.table.isPaginated = localStorage.getItem( user + '.lastPaginate' )  == 'true';
        }

        if ( this.table.total <= this.table.perPage ) {
          return false;
        } else {
          return true;
        }
      },
      
      filesPaginationNeeded () {
        var user = localStorage.getItem( 'user' );

        if ( localStorage.getItem( user + '.lastPaginate' ) ) {
          this.filesTable.isPaginated = localStorage.getItem( user + '.lastPaginate' )  == 'true';
        }

        if ( this.filesTable.total <= this.filesTable.perPage ) {
          return false;
        } else {
          return true;
        }
      }
    },

    methods: {

      searchVMs: _.debounce(function(  term ) {
        if (term === null) {
          term  = '';
        }
        this.searchName = term;
        if (this.activeTab == 0){
          this.updateExperiment();
          return
        }
        
        this.updateFiles()
        
      },250),

      bootDecorator ( vm ) {
        if (vm.external) {
          return 'dnb';
        }

        if ( vm.dnb ) {
          return 'dnb';
        } else {
          return 'boot';
        }
      },
      getSnapshotStatus ( vm, persistanceLabel ) {
        if (vm.external) {
          return true;
        }

        if ( vm.snapshot && persistanceLabel ) {
          return true;
        } else if (vm.snapshot && !persistanceLabel) {
          return false;
        }
        else if (!vm.snapshot && persistanceLabel){
          return false;
        }
        else {

          return true;
        }
      },

      changePaginate () {
        var user = localStorage.getItem( 'user' );
        localStorage.setItem( user + '.lastPaginate', this.table.isPaginated );
      },

      changeFilesPaginate () {
        var user = localStorage.getItem( 'user' );
        localStorage.setItem( user + '.lastPaginate', this.filesTable.isPaginated );
      },

      onPageChange  ( page ) {
        this.table.currentPage = page;
        this.updateExperiment();
      },

      onSort  ( column, order ) {
        this.table.sortColumn = column;
        this.table.defaultSortDirection = order;
        this.updateExperiment();
      },

      onFilesPageChange  ( page ) {
        this.filesTable.currentPage = page;
        this.updateFiles();
      },
      
      onFilesSort  ( column, order ) {
        this.filesTable.sortColumn = column;
        this.filesTable.defaultSortDirection = order;
        this.updateFiles();
      }, 

      handler ( event ) {
        event.data.split( /\r?\n/ ).forEach( m => {
          let msg = JSON.parse( m );
          this.handle( msg );
        });
      },
    
      handle ( msg ) {
        switch ( msg.resource.type ) {
          case 'experiment': {
            // We only care about experiment publishes pertaining to the
            // schedule action when in a stopped experiment.
            if ( msg.resource.action != 'schedule' ) {
              return;
            }

            let vms = this.experiment.vms;

            for ( let i = 0; i < msg.result.schedule.length; i++ ) {
              for ( let j = 0; i < vms.length; j++ ) {
                if ( vms[ j ].name == msg.result.schedule[ i ].vm ) {
                  vms[ j ].host = msg.result.schedule[ i ].host;
                  break;
                }
              }
            }

            this.experiment.vms = [ ...vms ];
          
            this.$buefy.toast.open({
              message: 'The VMs for this experiment have been scheduled.',
              type: 'is-success'
            });

            break;
          }

          case 'experiment/vm': {
            // We only care about experiment VM publishes pertaining to
            // the update action when in a stopped experiment.
            if ( msg.resource.action != 'update' ) {
              return;
            }
            
            let vms = this.experiment.vms;

            for ( let i = 0; i < vms.length; i++ ) {
              if ( vms[i].name == msg.result.name ) {
                vms[ i ] = msg.result;

                break;
              }
            }
        
            this.experiment.vms = [ ...vms ];
          
            this.$buefy.toast.open({
              message: 'The VM ' + msg.result.name + ' has been successfully updated.',
              type: 'is-success'
            });            

            break;
          }
        }
      },
      
      updateExperiment () {
        let params = '?show_dnb=true&filter=' + this.searchName
        params = params + '&sortCol=' + this.table.sortColumn
        params = params + '&sortDir=' + this.table.defaultSortDirection

        if ( this.table.isPaginated ) {
          params = params + '&pageNum=' + this.table.currentPage
          params = params + '&perPage=' + this.table.perPage
        }

        this.$http.get( 'experiments/' + this.$route.params.id + params).then(
          response => {
            response.json().then( state => {
              this.experiment = state;
              this.table.total = state.vm_count;
              
              this.vlanModal.vlans = this.experiment.vlans.map( vlan => { return vlan; } );

              // Only add successful searches to the search history
              if (this.table.total > 0) {
                if (this.searchHistory > this.searchHistoryLength) {
                  this.searchHistory.pop()
                }
                this.searchHistory.push(this.searchName.trim())
                this.searchHistory = this.getUniqueItems(this.searchHistory)
              }

              if ( this.roleAllowed('hosts', 'list') ) {
                this.updateHosts()
              }
              if ( this.roleAllowed('disks', 'list') ) {
                this.updateDisks()
              }
            });
          }, err => {
            this.errorNotification(err);
          }
        ).finally(
          () => { this.isWaiting = false }
        );
      },
      
      updateHosts () {
        this.$http.get( 'hosts' ).then(
          response => {
            response.json().then(
              state => {
                for ( let i = 0; i < state.hosts.length; i++ ) {
                  if ( state.hosts[ i ].schedulable ) {
                    this.hosts.push( state.hosts[ i ].name );
                  }

                  this.hosts.sort()
                  this.isWaiting = false;
                }
              }
            );
          }, err => {
            this.errorNotification(err);
          }
        );
      },
      
      updateDisks () {
        this.$http.get( 'disks' + '?expName=' + this.$route.params.id ).then(
          response => {
            response.json().then(
              state => {
                if ( state.disks.length == 0 ) {
                  this.isWaiting = true;
                } else {
                  for ( let i = 0; i < state.disks.length; i++ ) {
                    this.disks.push( state.disks[ i ] );
                  }

                  this.disks.sort()
                  this.isWaiting = false;
                }
              }
            );
          }, err => {
            this.errorNotification(err);
          }
        );
      },
      
      tabsSwitched() {

        // Clear search history and 
        // search filter when switching tabs
        this.searchHistory = []
        this.searchName = ""

        
        if (this.activeTab == 0){
          this.searchPlaceholder = "Find a VM"         
          this.updateExperiment()          
        }
        else {
          this.searchPlaceholder = "Find a File" 
          this.updateFiles()         
        }

      },

      updateFiles () {               
        let params = '?filter=' + this.searchName
        params = params + '&sortCol=' + this.filesTable.sortColumn
        params = params + '&sortDir=' + this.filesTable.defaultSortDirection

        if ( this.table.isPaginated ) {
          params = params + '&pageNum=' + this.table.currentPage
          params = params + '&perPage=' + this.table.perPage
        }

        this.$http.get( 'experiments/' + this.$route.params.id + '/files' + params ).then(
          response => {
            response.json().then(
              state => {                             
                this.files = state.files
                this.filesTable.total = state.total

                for ( let i = 0; i < state.files.length; i++ ) {
                  this.filesTable.categories.push( ...state.files[i].categories );
                }

                this.filesTable.categories = this.getUniqueItems(this.filesTable.categories);

                if (this.filesTable.category) {
                  let files = this.files;
                  this.files = [];
                  for (let i = 0; i < files.length; i++) {
                    if (files[i].categories.includes(this.filesTable.category)) {
                      this.files.push(files[i]);
                    }
                  }
                }

                // Only add successful searches to the search history
                if (this.files.length > 0) {
                  if (this.searchHistory > this.searchHistoryLength) {
                    this.searchHistory.pop()
                  }

                  this.searchHistory.push(this.searchName.trim())
                  this.searchHistory = this.getUniqueItems(this.searchHistory)
                }
              }
            );
          }, err => {
            this.errorNotification(err);
          }
        );
      },

      viewFile ( file ) {
        this.isWaiting = true;

        this.$http.get(
          `experiments/${this.$route.params.id}/files/${file.name}?path=${file.path}`,
          { 'headers': { 'Accept': 'text/plain' } },
        ).then(
          response => {
            this.fileViewerModal.title = file.path;
            this.fileViewerModal.contents = response.bodyText;
            this.fileViewerModal.active = true;
          }, err => {
            this.errorNotification(err);
          }
        ).finally(
          () => { this.isWaiting = false; }
        );
      },

      resetFileViewerModal () {
        this.fileViewerModal.active = false;
        this.fileViewerModal.title = null;
        this.fileViewerModal.contents = null;
      },

      updateVLANs () {
        this.vlanModal.active = false;
        let vlans = {};

        for ( let i = 0; i < this.vlanModal.vlans.length; i++ ) {
          let obj = this.vlanModal.vlans[i];
          if ( obj.vlan !== 0 ) {
            vlans[obj.alias] = obj.vlan;
          }
        }

        let body = JSON.stringify(vlans);

        this.$http.patch(
          'experiments/' + this.$route.params.id, body
        ).then(
          _ => {
            this.$buefy.toast.open({
              message: 'Updating the VLAN Assignment for the ' + this.$route.params.id + ' Experiment was successful.',
              type: 'is-success',
              duration: 4000
            });
          }, err => {
            this.errorNotification(err);
          }
        );
      },

      assignCategory ( value ) {
        this.filesTable.category = value;
        this.updateFiles();
      },

      start () {
        this.$buefy.dialog.confirm({
          title: 'Start the Experiment',
          message: 'This will start the ' + this.$route.params.id + ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Start',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            this.$http.post(
              'experiments/' + this.$route.params.id + '/start'
            ).then(
              response => { 
                console.log('the ' + this.$route.params.id + ' experiment was started.'); 
                this.$router.replace('/experiments/');                
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            );
          }
        })
      },

      assignHost ( name, host ) {        
        this.$buefy.dialog.confirm({
          title: 'Assign a Host',
          message: 'This will assign the ' + name + ' VM to the ' + host + ' host.',
          cancelText: 'Cancel',
          confirmText: 'Assign Host',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            let update = { "host": host };
            
            this.$http.patch(
              'experiments/' + this.$route.params.id + '/vms/' + name, update
            ).then(
              response => {
                let vms = this.experiment.vms;
                
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[ i ].name == response.body.name ) {
                    vms[ i ] = response.body;
                    break;
                  }
                }
              
                this.experiment.vms = [ ...vms ];
              
                this.isWaiting = false;
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            )
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          }
        })
      },

      unassignHost ( name,host ) {
        this.$buefy.dialog.confirm({
          title: 'Unassign a Host',
          message: 'This will cancel the host assignment for ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Unassign Host',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            let update = { "host": '' };

            this.$http.patch(
              'experiments/' + this.$route.params.id + '/vms/' + name, update
            ).then(
              response => {
                let vms = this.experiment.vms;
                
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[ i ].name == response.body.name ) {
                    vms[ i ] = response.body;
                    break;
                  }
                }
              
                this.experiment.vms = [ ...vms ];
              
                this.isWaiting = false;              
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            )
          }
        })
      },

      assignCpu ( name, cpus ) {
        this.$buefy.dialog.confirm({
          title: 'Assign CPUs',
          message: 'This will assign ' + cpus + ' cpu(s) to the ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign CPUs',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            let update = { "cpus": cpus };

            this.$http.patch(
              'experiments/' + this.$route.params.id + '/vms/' + name, update
            ).then(
              response => {
                let vms = this.experiment.vms;
                
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[ i ].name == response.body.name ) {
                    vms[ i ] = response.body;
                    break;
                  }
                }
              
                this.experiment.vms = [ ...vms ];
              
                this.isWaiting = false;              
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            )
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          }
        })
      },

      assignRam ( name, ram ) {
        this.$buefy.dialog.confirm({
          title: 'Assign Memory',
          message: 'This will assign ' + ram + ' of memory to the ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Memory',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            let update = { "ram": ram };

            this.$http.patch(
              'experiments/' + this.$route.params.id + '/vms/' + name, update
            ).then(
              response => {
                let vms = this.experiment.vms;
                
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[ i ].name == response.body.name ) {
                    vms[ i ] = response.body;
                    break;
                  }
                }
              
                this.experiment.vms = [ ...vms ];
              
                this.isWaiting = false;              
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            )
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          }
        })
      },

      assignDisk ( name, disk ) {
        this.$buefy.dialog.confirm({
          title: 'Assign a Disk Image',
          message: 'This will assign the ' + disk + ' disk image to the ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Disk',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            let update = { "disk": disk };

            this.$http.patch(
              'experiments/' + this.$route.params.id + '/vms/' + name, update
            ).then(
              response => {
                let vms = this.experiment.vms;
                
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[ i ].name == response.body.name ) {
                    vms[ i ] = response.body;
                    break;
                  }
                }
              
                this.experiment.vms = [ ...vms ];
              
                this.isWaiting = false;              
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            )
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          }
        })
      },
      assignPartition ( name, partition ) {
        this.$buefy.dialog.confirm({
          title: 'Assign an Image Partition',
          message: 'This will assign the image partition ' + partition + ' to the ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Partition',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            let update = { "inject_partition": partition };

            this.$http.patch(
              'experiments/' + this.$route.params.id + '/vms/' + name, update
            ).then(
              response => {
                let vms = this.experiment.vms;
                
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[ i ].name == response.body.name ) {
                    vms[ i ] = response.body;
                    break;
                  }
                }
              
                this.experiment.vms = [ ...vms ];
              
                this.isWaiting = false;              
              }, err => {
                this.errorNotification(err);                
                this.isWaiting = false;
              }
            )
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          }
        })
      },

      updateDnb ( vm ) {
        if (vm.external) {
          return;
        }

        this.isWaiting = true;
        
        let update = { "dnb": !vm.dnb };

        this.$http.patch(
          'experiments/' + this.$route.params.id + '/vms/' + vm.name, update
        ).then(
          response => {
            let vms = this.experiment.vms;
          
            for ( let i = 0; i < vms.length; i++ ) {
              if ( vms[ i ].name == response.body.name ) {
                vms[ i ] = response.body;
                break;
              }
            }
        
            this.experiment.vms = [ ...vms ];
        
            this.isWaiting = false;              
          }, err => {
            this.errorNotification(err);              
            this.isWaiting = false;
          }
        )
      },

      updateSnapshot( name, persistence ) {
    let persistenceMessage = ""
    if (persistence == "true"){
      persistenceMessage = "Non-Persistent"
    }
    else {
      persistenceMessage = "Persistent"
    }
    if (persistence == "true"){
        persistence = true
    }
    else {
      persistence = false
    }
    this.$buefy.dialog.confirm({
      title: 'Assign Image Persistence',
      message: 'This will assign the ' + name + ' VM\'s disk to be ' +  persistenceMessage,
      cancelText: 'Cancel',
      confirmText: 'Confirm',
      type: 'is-success',
      hasIcon: true,
      onConfirm: () => {
        this.isWaiting = true;
        
        let update = { "snapshot": persistence};

        this.$http.patch(
          'experiments/' + this.$route.params.id + '/vms/' + name, update
        ).then(
          response => {
            let vms = this.experiment.vms;
            
            for ( let i = 0; i < vms.length; i++ ) {
              if ( vms[ i ].name == response.body.name ) {
                vms[ i ] = response.body;
                break;
              }
            }
          
            this.experiment.vms = [ ...vms ];
          
            this.isWaiting = false;              
          }, err => {
            this.errorNotification(err);                
            this.isWaiting = false;
          }
        )
      },
      onCancel: () => {
        // force table to be rerendered so selected value resets
        this.table.key += 1;
      }
    })
  },
        updateSchedule () {
          this.$buefy.dialog.confirm({
            title: 'Assign a Host Schedule',
            message: 'This will schedule host(s) with the ' 
                    + this.algorithm 
                    + ' algorithm for the ' 
                    + this.$route.params.id 
                    + ' experiment.',
            cancelText: 'Cancel',
            confirmText: 'Assign Schedule',
            type: 'is-success',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting = true;

              this.$http.post(
                'experiments/' + this.$route.params.id + '/schedule', { "algorithm": this.algorithm }
              ).then(
                response => {
                  let vms = this.experiment.vms;
                  
                  for ( let i = 0; i < vms.length; i++ ) {
                    if ( vms[ i ].name == response.body.name ) {
                      vms[ i ] = response.body;
                      break;
                    }
                  }
                
                  this.experiment.vms = [ ...vms ];
                
                  this.isWaiting = false;              
                }, err => {
                  this.errorNotification(err);                
                  this.isWaiting = false;
                }
              )
            }
          })
      },

      getUniqueItems(inputArray) {
        let arrayHash = {};
        
        for(let i = 0; i<inputArray.length;i++) {
          // Skip really short items
          if (inputArray[i].length < 4) {
            if (!inputArray[i].includes('dnb')) {
              continue
            }
          }

          if(arrayHash[inputArray[i]] === undefined) {
            arrayHash[inputArray[i]] = true;
          }
        }
        
        return Object.keys(arrayHash).sort();
      },
      
      getBootLabel (vm) {
        return vm.dnb ? `Boot ${vm.name}` : `Do Not Boot ${vm.name}`;
      },
      getSnapshotLabel (vm) {
        return vm.snapshot ? `${vm.name}'s disk will not persist` : `${vm.name}'s disk will persist`;
      },
      
      selectAllVMs () {            
        var visibleItems = this.$refs["vmTable"].visibleData

        if (visibleItems.length == 0) {
          // if there are no visible items, there is nothing to select
          return;
        } else if (this.selectedRows.length == visibleItems.length) {
          // if everything is selected, the unselect everything
          this.unSelectAllVMs();
          return;
        }
        
        // if the select all checkbox is not checked, then unselect everything
        if (!this.checkAll) {
          this.unSelectAllVMs();
          return;
        }        
        
        // add all visible items
        this.selectedRows = [];
        
        for (var i = 0; i < visibleItems.length; i++){
          let item = visibleItems[i];

          if (!item.external) {
            this.selectedRows.push(item.name)
          }
        }
      },
        
      unSelectAllVMs(){
        this.checkAll     = false;
        this.selectedRows = [];
      },

      setBoot( dnb ) {
        let vms = []

        let successMessage = "";
        let failedMessage = "";

        //Determine the list of VMs to apply the boot request to
        if ( this.selectedRows.length == 0 && this.searchName.length > 0 ) {
          let visibleItems = this.$refs["vmTable"].visibleData
          
          for ( let i = 0; i < visibleItems.length; i++ ) {
            vms.push( visibleItems[ i ].name );
          }
        } else {
          for ( let i = 0; i < this.selectedRows.length; i++ ) {
            vms.push( this.selectedRows[ i ] );
          }
        }

        if ( vms.length == 0 ) {
          return;
        }

        if ( dnb ) {
          successMessage = "The selected VMs were set to not boot";
          failedMessage = "The selected VMs were unable to be set to not boot";
        } else {
          successMessage = "The selected VMs were set to boot";
          failedMessage = "The selected VMs were unable to be set to boot";
        }
              
        let requestList = [];

        vms.forEach( ( vmName ) => {
          let update = { "name": vmName, "dnb": dnb };
          requestList.push( update );
        })
              
        this.$http.patch(
          'experiments/' + this.$route.params.id + '/vms', { "vms": requestList,"total": requestList.length }
          ).then( response => {
            let vms = this.experiment.vms;                  
      
            for ( let i = 0; i < response.body.vms.length; i++ ) {
              for ( let j = 0; j < vms.length; j++ ) {
                if ( response.body.vms[ i ].name == vms[ j ].name ) {
                  vms[ j ] = response.body.vms[ i ];
                  break;
                } 
              }                      
            }   
    
            this.experiment.vms = [ ...vms ];              
            this.isWaiting = false; 
                    
            this.$buefy.toast.open({
              message: successMessage,
              type: 'is-success',
              duration: 4000
            });
          }, err => {
            this.errorNotification(err);            
            this.isWaiting = false;
        });

        // clear the selection
        this.unSelectAllVMs()
      }, 

      getBaseName (diskName) { 
        return diskName.substring( diskName.lastIndexOf("/") + 1 );
      },

      getDiskToolTip (fullPath) {       
        return this.disks.indexOf(fullPath) == -1 ? "menu for assigning vm(s) disk" : fullPath
      },

      fileDownloadURL(name, path) {
        return this.$router.resolve({name: 'file', params: {id: this.$route.params.id, name: name, path: path, token: this.$store.getters.token}}).href;
      }
    },

    data () {
      return {
        table: {
          key: 0,
          isPaginated: false,
          isPaginationSimple: true,
          currentPage: 1,
          perPage: 10,          
          total:  0,
          sortColumn: 'name',          
          paginationSize: 'is-small',
          defaultSortDirection: 'asc'
        },
        filesTable: {          
          isPaginated: false,
          isPaginationSimple: true,
          currentPage: 1,
          perPage: 10,          
          total:  0,
          sortColumn: 'date',          
          paginationSize: 'is-small',
          defaultSortDirection: 'desc',
          categories: [],
          category: null
        },
        expModal: {
          active: false,
          vm: []
        },
        fileViewerModal: {
          active: false,
          title: null,
          contents: null
        },
        schedules: [
          'isolate_experiment',
          'round_robin',
        ],
        experiment: [],
        files: [],
        hosts: [],
        disks: [],
        searchName: '',
        filtered: null,
        algorithm: null,
        dnb: false,
        isWaiting: true,
        searchHistory: [],        
        searchHistoryLength:10,
        checkAll:false,
        selectedRows: [],
        searchPlaceholder:"Find a VM",
        activeTab:0,
        vlanModal: {
          active: false,
          vlans: []
        }
      }
    }
  }
</script>

<style scoped>
  b-dropdown {
    color: #383838;
  }
  
  svg.fa-bolt.boot {
    color: #c46200;
  }

  svg.fa-bolt.dnb {
    color: #ffffff;
  }

  div.autocomplete >>> a.dropdown-item {
    color: #383838 !important;
  }
</style>
