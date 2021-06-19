<template>
  <div class="content">
    <b-modal :active.sync="expModal.active" has-modal-card>
      <div class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p class="modal-card-title">VM {{ expModal.vm.name ? expModal.vm.name : "unknown" }}</p>
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
    <b-field v-if="experimentUser() || experimentViewer()" position="is-right">
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
         <button class='button' style="color:#686868" @click="searchVMs('')">
          <b-icon icon="window-close"></b-icon>
        </button>
      </p>
      &nbsp; &nbsp;
      <p class="control">
        <b-button v-if="adminUser()" class="button is-success" slot="trigger" icon-right="play" @click="start"></b-button>
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
        <router-link v-if="adminUser()" class="button is-light" :to="{ name: 'soh', params: { id: this.$route.params.id }}">
          <b-icon icon="heartbeat"></b-icon>
        </router-link>
      </p>  
    </b-field>
    <div style="margin-top: -4em;">
      <b-tabs @input="tabsSwitched()" v-model="activeTab">
        <b-tab-item label="Table">          
          <b-table
            :key="table.key"
            :data="experiment.vms"
            :paginated="table.isPaginated && paginationNeeded"
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
              <template slot-scope="props">
                <b-table-column  field="multiselect" label="">              
                  <template v-slot:header="{ column }">
                    <b-tooltip label="Select/Unselect All" type="is-dark">
                    <b-checkbox @input="selectAllVMs" v-model="checkAll" type="is-info"/>
                    </b-tooltip>
                  </template>
                  <template>
                    <div>
                      <b-checkbox v-model="selectedRows" :native-value=props.row.name type="is-info"/>
                    </div>
                  </template>                  
                </b-table-column>
                <b-table-column field="name" label="VM" sortable>
                  <template v-if="adminUser()">
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
                <b-table-column field="host" label="Host" width="200" sortable>
                  <template v-if="adminUser()">
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
                  <template v-else>
                    {{ props.row.host }}
                  </template>
                </b-table-column>
                <b-table-column field="ipv4" label="IPv4">
                  <div v-for="(ip,index) in props.row.ipv4" :key="index">
                    {{ ip }}
                  </div>
                </b-table-column>
                <b-table-column field="cpus" label="CPUs" sortable centered>
                  <template v-if="adminUser()">
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
                    {{ props.row.cpus }}
                  </template>
                </b-table-column>
                <b-table-column field="ram" label="Memory" sortable centered>
                  <template v-if="adminUser()">
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
                    {{ props.row.ram }}
                  </template>
                </b-table-column>
                <b-table-column field="disk" label="Disk">
                  <template v-if="adminUser()">
                    <b-tooltip label="menu for assigning vm(s) disk" type="is-dark">
                      <b-select :value="props.row.disk" @input="( value ) => assignDisk( props.row.name, value )">
                        <option
                          v-for="( d, index ) in disks"
                          :key="index"
                          :value="d">
                            {{ d }}
                        </option>
                      </b-select>
                    </b-tooltip>
                  </template>
                  <template v-else>
                    {{ props.row.disk }}
                  </template>
                </b-table-column>
                <b-table-column v-if="experimentUser()" label="Boot" centered>
                  <template v-slot:header = "{ column }">                  
                      {{ column.label }}   
                      <div></div>                       
                      <div class="level" style="padding: 0px;" centered>                    
                        <b-tooltip label="Set to Boot" type="is-dark" :active="visibleItems()"> 
                          <div v-if="visibleItems()" @click="setBoot(false)">                  
                            <b-icon icon="bolt" style="color: #c46200;" size="is-small"></b-icon>  	
                          </div>  
                        </b-tooltip>                        
                        <b-tooltip label="Set to Do Not Boot" type="is-dark" :active="visibleItems()">             
                          <div v-if="visibleItems()" @click="setBoot(true)">                  
                            <b-icon icon="bolt" style="color: #ffffff;" size="is-small"></b-icon>  	
                          </div>  
                        </b-tooltip>                   
                      </div>
                  </template>
                  <b-tooltip :label="getBootLabel(props.row.name,props.row.dnb)" type="is-dark">
                    <div @click="updateDnb(props.row.name, !props.row.dnb)">
                      <font-awesome-icon :class="bootDecorator(props.row.dnb)" icon="bolt" />
                    </div>
                  </b-tooltip>
                </b-table-column>
              </template>
          </b-table>
          <br>
          <b-field v-if="paginationNeeded" grouped position="is-right">
            <div class="control is-flex">
              <b-switch v-model="table.isPaginated" @input="updateExperiment()" size="is-small" type="is-light">Paginate</b-switch>
            </div>
          </b-field>
        </b-tab-item>
        <b-tab-item label="Files">
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
            <template slot-scope="props">
                <b-table-column field="name" label="Name" sortable centered>                  
                          {{ props.row.name }}                      
                </b-table-column>
                <b-table-column field="date" label="Date" sortable centered>                  
                          {{ props.row.date }}                      
                </b-table-column>
                <b-table-column field="size" label="Size" sortable centered>                  
                          {{ props.row.size }}                      
                </b-table-column>
                 <b-table-column field="category" label="Category" sortable centered>                  
                          {{ props.row.category }}                      
                </b-table-column>
                <b-table-column field="download" label="Download" centered>   
                        <a :href="'/api/v1/experiments/'
                          + experiment.name 
                          + '/files/' 
                          + props.row.name
                          + '?token=' 
                          + $store.state.token" target="_blank"> 
                          <b-icon icon="file-download" size="is-small"></b-icon>                       
                          </a>                      
                </b-table-column>
            </template>
          </b-table>
        </b-tab-item>
      </b-tabs>
    </div>
    <b-loading :is-full-page="true" :active.sync="isWaiting" :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  export default {
    beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },

    async created () {
      this.$options.sockets.onmessage = this.handler;
      this.updateExperiment();
      
      if ( this.adminUser() ) {
        this.updateHosts();
        this.updateDisks();      
      }
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
        if ( this.table.total <= this.table.perPage ) {
          return  false;
        }

        return true;
      },
      
      filesPaginationNeeded () {
        if ( this.filesTable.total <= this.filesTable.perPage ) {
          return  false;
        }

        return true;
      }
    },

    methods: {
      adminUser () {
        return [ 'Global Admin', 'Experiment Admin' ].includes( this.$store.getters.role );
      },

      experimentUser () {
        return [ 'Global Admin', 'Experiment Admin', 'Experiment User' ].includes( this.$store.getters.role );
      },

      experimentViewer () {
        return [ 'Experiment Viewer' ].includes( this.$store.getters.role );
      },

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

      bootDecorator ( dnb ) {
        if ( dnb ) {
          return 'dnb';
        } else {
          return 'boot';
        }
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

        let pageSize = this.table.isPaginated ? this.table.perPage : this.table.total

        let params = '?show_dnb=true&filter=' + this.searchName
        params = params + '&sortCol=' + this.table.sortColumn
        params = params + '&sortDir=' + this.table.defaultSortDirection
        params = params + '&pageNum=' + this.table.currentPage
        params = params + '&perPage=' + pageSize
        this.$http.get( 'experiments/' + this.$route.params.id + params).then(
          response => {
            response.json().then( state => {
              this.experiment = state;
              this.table.total  = state.vm_count;               

              // Only add successful searches to the search history
              if (this.table.total > 0) {
                if (this.searchHistory > this.searchHistoryLength) {
                  this.searchHistory.pop()
                }
                this.searchHistory.push(this.searchName.trim())
                this.searchHistory = this.getUniqueItems(this.searchHistory)
              }

              this.isWaiting = false;
            });
          }, response => {
            this.isWaiting = false;
            this.$buefy.toast.open({
              message: 'Getting the experiments failed.',
              type: 'is-danger',
              duration: 4000
            });
          }
        );
      },
      
      updateHosts () {
        this.$http.get( 'hosts' ).then(
          response => {
            response.json().then(
              state => {
                if ( state.hosts.length == 0 ) {
                  this.isWaiting = true;
                } else {
                  for ( let i = 0; i < state.hosts.length; i++ ) {
                    if ( state.hosts[ i ].schedulable ) {
                      this.hosts.push( state.hosts[ i ].name );
                    }
                  }
                  
                  this.isWaiting = false;
                }
              }
            );
          }, response => {
            this.isWaiting = false;
            this.$buefy.toast.open({
              message: 'Getting the hosts failed.',
              type: 'is-danger',
              duration: 4000
            });
          }
        );
      },
      
      updateDisks () {
        this.$http.get( 'disks' ).then(
          response => {
            response.json().then(
              state => {
                if ( state.disks.length == 0 ) {
                  this.isWaiting = true;
                } else {
                  for ( let i = 0; i < state.disks.length; i++ ) {
                    this.disks.push( state.disks[ i ] );
                  }
                  
                  this.isWaiting = false;
                }
              }
            );
          }, response => {
            this.isWaiting = false;
            this.$buefy.toast.open({
              message: 'Getting the disks failed.',
              type: 'is-danger',
              duration: 4000
            });
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
        params = params + '&pageNum=' + this.filesTable.currentPage
        params = params + '&perPage=' + this.filesTable.perPage

        this.$http.get( 'experiments/' + this.$route.params.id + '/files' + params ).then(
          response => {
            response.json().then(
              state => {                             
                this.files = state.files
                this.filesTable.total = state.total

                // Format the file sizes
                for(let i = 0; i<this.files.length;i++){
                  this.files[i].size = this.formatFileSize(this.files[i].size)
                }

                // Only add successful searches to the search history
                if (this.files.length > 0) {
                if (this.searchHistory > this.searchHistoryLength) {
                  this.searchHistory.pop()
                }
                this.searchHistory.push(this.searchName.trim())
                this.searchHistory = this.getUniqueItems(this.searchHistory)
              }
                
                this.isWaiting = false;
              }
            );
          }, response => {
            this.isWaiting = false;
            this.$buefy.toast.open({
              message: 'Getting the files failed.',
              type: 'is-danger',
              duration: 4000
            });
          }
        );
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Starting experiment ' + this.$route.params.id + ' failed with ' + response.status + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Assigning the ' 
                           + name 
                           + ' VM to the ' 
                           + host 
                           + ' host failed with ' 
                           + response.status 
                           + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Canceling the ' 
                           + host 
                           + ' assignment for the ' 
                           + name 
                           + ' VM failed with ' 
                           + response.status 
                           + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Assigning ' 
                           + cpus 
                           + ' cpu(s) to the ' 
                           + name 
                           + ' VM failed with ' 
                           + response.status 
                           + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Assigning ' 
                           + ram 
                           + ' of memory to the ' 
                           + name 
                           + ' VM failed with ' 
                           + response.status 
                           + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Assigning the ' 
                           + disk 
                           + ' to the ' 
                           + name 
                           + ' VM failed with ' 
                           + response.status 
                           + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
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

      updateDnb ( name, dnb ) {
        if ( dnb ) {
          this.$buefy.dialog.confirm({
            title: 'Set Do NOT Boot',
            message: 'This will set the ' + name + ' VM to NOT boot when the experiment starts.',
            cancelText: 'Cancel',
            confirmText: 'Do NOT Boot',
            type: 'is-warning',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting = true;
              
              let update = { "dnb": dnb };

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
                }, response => {
                  this.$buefy.toast.open({
                    message: 'Setting the ' 
                             + name 
                             + ' VM to NOT boot when experiment starts failed with ' 
                             + response.status 
                             + ' status.',
                    type: 'is-danger',
                    duration: 4000
                  });
                  
                  this.isWaiting = false;
                }
              )
            }
          })
        } else {
          this.$buefy.dialog.confirm({
            title: 'Set Boot',
            message: 'This will set the ' + name + ' VM to boot when the experiment starts.',
            cancelText: 'Cancel',
            confirmText: 'Boot',
            type: 'is-success',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting = true;
              
              let update = { "dnb": dnb };

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
                }, response => {                  
                  this.$buefy.toast.open({
                    message: 'Setting the ' 
                             + name 
                             + ' VM to boot when experiment starts failed with ' 
                             + response.status 
                             + ' status.',
                    type: 'is-danger',
                    duration: 4000
                  });
                  
                  this.isWaiting = false;
                }
              )
            }
          })
        }
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
              }, response => {
                this.$buefy.toast.open({
                  message: 'Scheduling the host(s) with the ' 
                           + this.algorithm 
                           + ' for the ' 
                           + this.$route.params.id 
                           + ' experiment failed with ' 
                           + response.status 
                           + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
                
                this.isWaiting = false;
              }
            )
          }
        })
      },

      getUniqueItems(inputArray){

        let arrayHash = {};
        
        for(let i = 0; i<inputArray.length;i++)
        {
          // Skip really short items
          if (inputArray[i].length < 4){
            if (!inputArray[i].includes('dnb')){
              continue
            }
            
          }

          if(arrayHash[inputArray[i]] === undefined )
          {
            arrayHash[inputArray[i]] = true;
          
          }
        
        }
        
        return Object.keys(arrayHash).sort();

      },
      
      getBootLabel(vmName,dnb) {
        return dnb ? "Boot " + vmName : "Do Not Boot " + vmName;
        
      },

      visibleItems() {        
        return this.$refs["vmTable"].visibleData.length > 0
      },
      
      selectAllVMs  () {            
        
        var visibleItems = this.$refs["vmTable"].visibleData
        //If there are no visible items, there is nothing to select
        if(visibleItems.length == 0)
        {
          return 
        }    
        
        //If everything is selected, the unselect everything
        else if(this.selectedRows.length == visibleItems.length)
        {
          this.unSelectAllVMs();
          return
          
        }

        
        // If the select all checkbox is not checked, then unselect everything        
        if(!this.checkAll) {
          this.unSelectAllVMs();
          return
        }
        
        
        //Add all visible items
        this.selectedRows=[]
        
        for(var i=0; i<visibleItems.length; i++){
            this.selectedRows.push(visibleItems[i].name)
        }  
                
      },
        
      unSelectAllVMs(){
        
        this.checkAll = false;
        this.selectedRows=[]        
        
      },

      setBoot(dnb){
        let vms = []
        let attemptMessage = "";
        let successMessage = "";
        let failedMessage = "";

        //Determine the list of VMs to apply the boot request to
        if (this.selectedRows.length == 0 && this.searchName.length > 0){
            
            let visibleItems = this.$refs["vmTable"].visibleData
            
            for(let i = 0; i<visibleItems.length;i++){
              vms.push(visibleItems[i].name)
            }
        }
        else{
          for(let i = 0; i<this.selectedRows.length;i++){
              vms.push(this.selectedRows[i])
            }
        }

        if (vms.length == 0){
            return
        }

        if (dnb){
          attemptMessage = " to not boot when the experiment starts"
          successMessage = " The selected VMs were set to not boot "
          failedMessage = " The selected VMs were unable to be set to not boot "
        }
        else {
          attemptMessage = " to boot when the experiment starts"
          successMessage = " The selected VMs were set to boot "
          failedMessage = " The selected VMs were unable to be set to boot " 
        }

        this.$buefy.dialog.confirm({
            title:"Set Boot",
            message:"This will set " + vms.join(", ") + attemptMessage ,
            cancelText:"Cancel",
            confirmText:"Ok",
            type:"is-success",
            onConfirm: () => {
              
                let requestList = [];

                vms.forEach((vmName) => {
                  let update = {"name":vmName, "dnb": dnb };
                  requestList.push(update)
                })
              
                this.$http.patch(
                  'experiments/' + this.$route.params.id + '/vms', {"vms":requestList,"total":requestList.length}
                  ).then(
                  response  => {
                    let vms = this.experiment.vms;                  
              
                    for ( let i = 0; i < response.body.vms.length; i++ ) {
                      for (let j=0; j<vms.length;j++){
                        if ( response.body.vms[i].name == vms[j].name ) {
                          vms[j] = response.body.vms[i];
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
                      
                
                  },  response => {
                    
                    this.$buefy.toast.open({
                    message: failedMessage,
                    type: 'is-danger',
                    duration: 4000
                  });
                
                this.isWaiting = false;
              }
              )                  
                
            }
          })
          //clear the selection
          this.unSelectAllVMs()
           
      }, 
      
      formatFileSize(fileSize){
        if(fileSize < Math.pow(10,3)){
          return fileSize.toFixed(2) + ' B'
        } else if(fileSize >= Math.pow(10,3) && fileSize < Math.pow(10,6)){
          return (fileSize/Math.pow(10,3)).toFixed(2) + ' KB'
        } else if (fileSize >= Math.pow(10,6) && fileSize < Math.pow(10,9)){
          return (fileSize/Math.pow(10,6)).toFixed(2) + ' MB'
        } else if (fileSize >= Math.pow(10,9)) {
          return (fileSize/Math.pow(10,9)).toFixed(2) + ' GB'
        }
      }
    },

    data () {
      return {
        table: {
          key: 0,
          isPaginated: true,
          isPaginationSimple: true,
          currentPage: 1,
          perPage: 10,          
          total:  0,
          sortColumn: 'name',          
          paginationSize: 'is-small',
          defaultSortDirection: 'asc'
        },
        filesTable: {          
          isPaginated: true,
          isPaginationSimple: true,
          currentPage: 1,
          perPage: 10,          
          total:  0,
          sortColumn: 'date',          
          paginationSize: 'is-small',
          defaultSortDirection: 'desc'
        },
        expModal: {
          active: false,
          vm: []
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
        activeTab:0
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
