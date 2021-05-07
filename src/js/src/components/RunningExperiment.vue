<template>
  <div  class="content">
    <b-modal :active.sync="expModal.active" :on-cancel="resetExpModal" has-modal-card>
      <div  class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p  class="modal-card-title">{{ expModal.vm.name ? expModal.vm.name : "unknown" }} VM</p>
        </header>
        <section class="modal-card-body">
          <p>Host:  {{ expModal.vm.host }}</p>
          <p>IPv4:  {{ expModal.vm.ipv4 | stringify }}</p>
          <p>CPU(s):  {{ expModal.vm.cpus }}</p>
          <p>Memory:  {{ expModal.vm.ram | ram }}</p>
          <p>Disk:  {{ expModal.vm.disk }}</p>
          <p>Uptime:  {{ expModal.vm.uptime | uptime }}</p>
          <p>Network(s):  {{ expModal.vm.networks | stringify | lowercase }}</p>
          <p>Taps:  {{ expModal.vm.taps | stringify | lowercase }}</p>
          <p  v-if="expModal.snapshots">
            Snapshots:       
            <br>
            <p  v-for="( snap, index ) in expModal.snapshots" :key="index">
              <b-tooltip label="restore this snapshot" type="is-light is-right">
                <b-icon icon="play-circle"  style="color:#686868" @click.native="restoreSnapshot( expModal.vm.name, snap )"></b-icon>
              </b-tooltip>
              {{ snap }}
            </p>
          </p>          
      </section>
      <footer class="modal-card-foot  buttons is-right">
        <div v-if="adminUser() && !showModifyStateBar">
          <template v-if="!expModal.vm.running">
            <b-tooltip label="start" type="is-light">
              <b-button class="button is-success" icon-left="play"  @click="startVm( expModal.vm.name )">
              </b-button>
            </b-tooltip>
          </template>
          <template v-else>
            <b-tooltip label="pause" type="is-light">
              <b-button class="button is-warning" icon-left="pause" @click="pauseVm(  expModal.vm.name )">
              </b-button>
            </b-tooltip>
          </template>
        </div>
        <div v-if="experimentUser() && !showModifyStateBar && expModal.vm.running">
             &nbsp; 
          <b-tooltip  label="create memory snapshot" type="is-light">
            <b-button class="button is-light" icon-left="database" @click="notImplemented()">
             </b-button>
          </b-tooltip>
         </div>
         <div v-if="experimentUser() && !showModifyStateBar && expModal.vm.running">
              &nbsp;  
          <b-tooltip  label="create backing image" type="is-light">
            <b-button class="button is-light" icon-left="save" @click="diskImage(expModal.vm.name)">
            </b-button>
          </b-tooltip>
          </div>
         <div v-if="experimentUser() && !showModifyStateBar && expModal.vm.running">
              &nbsp;  
              <b-tooltip  label="create vm snapshot" type="is-light">
               <b-button  class="button is-light" icon-left="camera" @click="captureSnapshot(expModal.vm.name)">
               </b-button>
              </b-tooltip>
          </div>   
          <div  v-if="experimentUser() && !showModifyStateBar && expModal.vm.running">
              &nbsp;  
             <b-tooltip label="record screenshot" type="is-light">
               <b-button  class="button is-light" icon-left="video" @click="notImplemented()">
               </b-button>
             </b-tooltip>
           </div>             
            <div v-if="experimentUser() && !showModifyStateBar">
              &nbsp;  
              <b-tooltip  label="modify state" type="is-light">
               <b-button  class="button is-light" icon-left="edit" @click="showModifyStateBar = true">
               </b-button>
              </b-tooltip>
            </div>               
            <div v-if="experimentUser() && showModifyStateBar">
                &nbsp; 
              <b-tooltip  label="redeploy" type="is-light">
                <b-button class="button is-success" icon-left="history" @click="redeploy(expModal.vm.name)">
                </b-button>
              </b-tooltip>               
               &nbsp;
              <b-tooltip  label="reset disk state" type="is-light">
                 <b-button class="button is-success" icon-left="undo-alt" @click="resetVmState(expModal.vm.name)">
                 </b-button>
              </b-tooltip>
             
                &nbsp;
              <b-tooltip  label="restart" type="is-light">
                 <b-button class="button is-success" icon-left="sync-alt" @click="restartVm(expModal.vm.name)">
                 </b-button>
              </b-tooltip>
             
                &nbsp;              
                <b-tooltip label="shutdown" type="is-light">
                  <b-button class="button is-danger"  icon-left="power-off" @click="shutdownVm(expModal.vm.name)">
                  </b-button>
                </b-tooltip>              
               
                &nbsp;
                <b-tooltip label="kill" type="is-light">
                  <b-button class="button is-danger"  icon-left="skull-crossbones" @click="killVm(expModal.vm.name)">
                  </b-button>
                </b-tooltip>              
             
               &nbsp;
               <b-tooltip label="close  toolbar" type="is-light">
                 <b-button class="button is-light" icon-left="window-close" @click="showModifyStateBar = false">
                 </b-button>
               </b-tooltip>
             </div>
      </footer>
    </div>
  </b-modal>
  <b-modal  :active.sync="vlanModal.active" has-modal-card>
    <div class="modal-card" style="width:25em">
      <header class="modal-card-head">
        <p class="modal-card-title">Change the VLAN</p>
      </header>
      <section  class="modal-card-body">
        <font color="#202020">
          Move  interface {{ vlanModal.vmNetIndex }} from {{ vlanModal.vmFromNet | lowercase }} to a new one for the {{ vlanModal.active ? vlanModal.vmName : "unknown" }} VM.
        </font>
        <br><br>
          <b-field>
            <b-select v-model="vlan" expanded>
              <option value='0'>disconnect</option>
              <option v-for="(  n, index ) in experiment.vlans" 
                :key="index" 
                :value="n">
                {{ n.alias | lowercase }} ({{ n.vlan }})
              </option>
            </b-select>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-success" 
            @click="changeVlan( vlanModal.vmNetIndex, vlan, vlanModal.vmFromNet, vlanModal.vmName )">
            Change
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="redeployModal.active" :on-cancel="resetRedeployModal" has-modal-card ref="reDeploy">
      <div  class="modal-card" style="width:auto">
        <header class="modal-card-head">
          <p  class="modal-card-title">Redeploy the VMs</p>
        </header>
        <section class="modal-card-body">
          <div  v-if="redeployModal.vm.length > 0">
           <div v-for="(vmI,index)  in redeployModal.vm" :key="index" class="level">
             <div class="level-item">               
               <font  color="#202020">
                 <hr v-if="parseInt(index) > 0" style="color:#595959;background-color:#595959">
                 Modify current settings and redeploy {{ vmI.name }}
                 <br><br>
                 CPUs: 
                  <b-tooltip  label="menu for assigning cpus" type="is-dark">
                    <b-select :value="vmI.cpus" expanded @input="( value ) => vmI.cpus = value">
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
                &nbsp;
                Memory: 
                 <b-tooltip label="menu for assigning memory" type="is-dark">
                   <b-select  :value="vmI.ram" expanded @input="( value ) => vmI.ram = value">
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
                <br><br>
                Disk:
                <b-tooltip label="menu for assigning disk" type="is-dark">
                  <b-select :value="vmI.disk" @input="( value ) =>  vmI.disk = value">
                    <option
                      v-for="(  d, index ) in disks"
                      :key="index"
                      :value="d">
                        {{ d }}
                    </option>
                  </b-select>
                </b-tooltip>
                <br><br>
                Replicate Original Injection(s):
                <b-tooltip label="menu for replicating injections" type="is-dark">
                  <b-select :value="vmI.inject" expanded  @input="( value ) => vmI.inject = value">
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </b-select>
                </b-tooltip>
               </font>
             </div> 
           </div>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
           <button  class="button" type="button" 
              @click="closeModal('reDeploy')">
              Cancel
           </button>              
          <button class="button is-success" 
            @click="redeployVm( redeployModal.vm )">
            Redeploy
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="diskImageModal.active" has-modal-card :on-cancel="resetDiskImageModal" ref="diskImage">
      <div  class="modal-card" style="width:auto">
        <header class="modal-card-head">
          <p  class="modal-card-title">Create a Disk Image</p>
        </header>
        <section class="modal-card-body">
         <div v-if="diskImageModal.vm.length > 0">
              <div  v-for="(vmI,index) in diskImageModal.vm" :key="index" class="level">
                 <div class="level-item">             
                  <font color="#202020">
                    <hr v-if="parseInt(index) > 0" style="color:#595959;background-color:#595959">
                        Create disk image of the {{ vmI.name }} VM with filename:                 
                        <br><br>
                        <b-field :type="vmI.nameErrType" :message="vmI.nameErrMsg" autofocus>
                          <b-input  type="text" v-model="vmI.filename"   focus></b-input>
                         </b-field>
                  </font>          
                </div>                                 
              </div>
          </div>
        </section>        
        <footer class="modal-card-foot buttons is-right">
          <button class="button"  type="button" 
              @click="closeModal('diskImage')">
              Cancel
        </button>  
          <button class="button is-success" :disabled="!validate()" @click="backingImage(diskImageModal.vm)">
            Create
          </button>
        </footer>
      </div>
    </b-modal>
    <hr>
    <div class="level is-vcentered">
      <div  class="level-item">
        <span style="font-weight: bold; font-size: x-large;">Experiment: {{ this.$route.params.id }}</span>&nbsp;
      </div>
      <div  class="level-item" v-if="experiment.scenario">
        <span style="font-weight: bold;">Scenario: {{ experiment.scenario }}</span>&nbsp;
      </div>
      <div  class="level-item" v-if="experiment.scenario">
        <span style="font-weight: bold;">Apps:</span>&nbsp;
        <b-taglist>
          <b-tag  v-for="( a, index ) in experiment.apps" :key="index" type="is-light">
            {{ a }}  
          </b-tag>
        </b-taglist>
      </div>
    </div>    
    
    <div class="level">    
      <div  class="level-left"></div>
      <div  class="level-right">
        <div class="level-item" style="margin-bottom: -.3em;">
        <b-field v-if="isMultiVmSelected && (experimentUser() || experimentViewer())" position="is-center">
            <div v-if="adminUser() && !showModifyStateBar">
                <b-tooltip label="start" type="is-light">
                  <b-button class="button is-success" icon-left="play"  @click="processMultiVmAction(vmActions.start)">
                  </b-button>
                </b-tooltip>
                &nbsp;
                <b-tooltip label="pause" type="is-light">
                  <b-button class="button is-warning" icon-left="pause" @click="processMultiVmAction(vmActions.pause)">
                 </b-button>
                </b-tooltip>
            </div>
          <div  v-if="experimentUser() && !showModifyStateBar">
              &nbsp;  
              <b-tooltip  label="create memory snapshot" type="is-light">
                <b-button class="button is-light" icon-left="database" @click="processMultiVmAction(vmActions.createMemorySnapshot)">
                </b-button>
              </b-tooltip>
            </div>
            <div v-if="experimentUser() && !showModifyStateBar">
              &nbsp;  
              <b-tooltip  label="create backing image" type="is-light">
                <b-button class="button is-light" icon-left="save" @click="processMultiVmAction(vmActions.createBacking)">
                </b-button>
              </b-tooltip>
            </div>            
            <div v-if="experimentUser() && !showModifyStateBar">
              &nbsp;  
              <b-tooltip  label="create vm snapshot" type="is-light">
               <b-button  class="button is-light" icon-left="camera" @click="processMultiVmAction(vmActions.captureSnapshot)">
               </b-button>
              </b-tooltip>
            </div> 
            <div v-if="experimentUser() && !showModifyStateBar">
              &nbsp;  
              <b-tooltip  label="record screenshot" type="is-light">
                <b-button class="button is-light" icon-left="video" @click="processMultiVmAction(vmActions.recordScreenshots)">
                </b-button>
              </b-tooltip>
            </div>
            <div v-if="experimentUser() && !showModifyStateBar">
              &nbsp;  
              <b-tooltip  label="modify state" type="is-light">
               <b-button  class="button is-light" icon-left="edit" @click="showModifyStateBar = true">
               </b-button>
              </b-tooltip>
            </div>               
              <div  v-if="experimentUser() && showModifyStateBar">
                &nbsp; 
                <b-tooltip label="redeploy" type="is-light">
                  <b-button class="button is-success" icon-left="history" @click="processMultiVmAction(vmActions.redeploy)">
                  </b-button>
                </b-tooltip>              
                &nbsp;
                <b-tooltip label="reset disk state" type="is-light">
                  <b-button class="button is-success" icon-left="undo-alt"  @click="processMultiVmAction(vmActions.resetState)">
                  </b-button>
                </b-tooltip>
             
                &nbsp;
                <b-tooltip label="restart" type="is-light">
                  <b-button class="button is-success" icon-left="sync-alt" @click="processMultiVmAction(vmActions.restart)">
                  </b-button>
                </b-tooltip>
             
                &nbsp;
                <b-tooltip label="shutdown" type="is-light">
                  <b-button class="button is-danger"  icon-left="power-off" @click="processMultiVmAction(vmActions.shutdown)">
                  </b-button>
                </b-tooltip>

                &nbsp;
                <b-tooltip label="kill" type="is-light">
                  <b-button class="button is-danger"  icon-left="skull-crossbones" @click="processMultiVmAction(vmActions.kill)">
                  </b-button>
                </b-tooltip>              
 
                &nbsp;
                <b-tooltip label="close toolbar" type="is-light">
                  <b-button class="button is-light" icon-left="window-close"  @click="showModifyStateBar = false">
                  </b-button>
                </b-tooltip>
             </div>
                                                                                                                                                                                                                     
        </b-field>
       </div>
        &nbsp;&nbsp;
       <div class="level-item"  style="margin-bottom: -1em;">
        <b-field v-if="experimentUser() || experimentViewer()" position="is-right">
          <b-autocomplete
            v-model="search.filter"
            placeholder="Find a VM"
            icon="search"
            :data="filteredData"
            @input="searchVMs"
            @select="option => searchVMs(option)">
            <template slot="empty">No results found</template>
          </b-autocomplete>
          <p  class='control'>
            <button class='button' style="color:#686868" @click="searchVMs('')">
              <b-icon icon="window-close"></b-icon>
            </button>
          </p>
          &nbsp; &nbsp;
          <p  class="control">
            <b-button v-if="adminUser()" class="button is-danger" slot="trigger" icon-right="stop" @click="stop"></b-button>
			 &nbsp;
			<router-link v-if="adminUser()" class="button is-light" :to="{ name: 'soh', params: { id: this.$route.params.id }}">
			  <b-icon icon="heartbeat"></b-icon>
			</router-link>
          </p>
        </b-field>
       </div>
      </div>
    </div>
    <div style="margin-top: -4em;">
      <b-tabs @change="updateFiles">
        <b-tab-item label="Table">
          <b-table
            :data="experiment.vms"
            :paginated="table.isPaginated && paginationNeeded"
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
            <template slot="empty">
              <section  class="section">
                <div class="content has-text-white has-text-centered">
                  Your  search turned up empty!
                </div>
              </section>
            </template>
            <template slot-scope="props">
               <b-table-column  field="multiselect" label="">              
                 <template v-slot:header="{ column }">
                   <b-tooltip label="Select/Unselect All" type="is-dark">
                   <input type="checkbox" @change="selectAllVMs" v-model="checkAll">  
                   </b-tooltip>
                 </template>
                <template v-if="!props.row.busy">
                  <div>
                    <input type="checkbox" :value="props.row.name" v-model="vmSelectedArray" >
                  </div>
                </template>
                <template v-else>
                 BUSY 
                </template>
              </b-table-column>
              <b-table-column field="name"  label="VM Name" width="150" sortable centered>
                <template v-if="experimentUser()">
                  <b-tooltip  label="start/stop/redeploy the vm" type="is-dark">
                    <span class="tag is-medium" :class="decorator( props.row.state, props.row.busy )">
                      <div  class="field">
                        <div @click="getInfo( props.row )">
                          {{  props.row.name }}
                        </div>
                      </div>
                    </span>
                  </b-tooltip>
                </template>
                <template v-else>
                  <b-tooltip  label="get info for the vm" type="is-dark">
                    <span class="tag is-medium" :class="decorator( props.row.running, !props.row.busy )">
                      <div  class="field">
                        <div @click="expModal.active = true; expModal.vm = props.row">
                          {{  props.row.name }}
                        </div>
                      </div>
                    </span>
                  </b-tooltip>
                </template>
                <section v-if="props.row.busy">
                  <p  />
                  <b-progress size="is-small" type="is-warning" show-value  :value=props.row.percent format="percent"></b-progress>
                </section>
              </b-table-column>
              <b-table-column field="screenshot"  label="Screenshot">
                <template v-if="props.row.running && !props.row.busy && !props.row.screenshot">
                  <a  :href="'/api/v1/experiments/' 
                    + $route.params.id 
                    + '/vms/' 
                    + props.row.name 
                    + '/vnc?token=' 
                    + $store.state.token" target="_blank">
                    <img src="@/assets/not-available.png">
                  </a>
                </template>
                <template v-else-if="props.row.running && !props.row.busy && props.row.screenshot">
                  <a  :href="'/api/v1/experiments/' 
                    + $route.params.id 
                    + '/vms/' 
                    + props.row.name 
                    + '/vnc?token=' 
                    + $store.state.token" target="_blank">
                    <img :src="props.row.screenshot">
                  </a>
                </template>
                <template v-else-if="props.row.busy">
                  <b-tooltip  label="Screenshot not available while busy with action" type="is-dark">
                  <img  src="@/assets/not-available.png">
                  </b-tooltip>
                </template>
                <template v-else>
                  <img  src="@/assets/not-running.png">
                </template>
              </b-table-column>
              <b-table-column field="host"  label="Host" width="150" sortable>
                {{ props.row.host }}
              </b-table-column>   
              <b-table-column field="ipv4"  label="IPv4" width="150">
               <template  v-if="experimentUser() && props.row.running && !props.row.busy"> 
                <b-tooltip :label="updateCaptureLabel(props.row)" type="is-dark">
                <div class="field">
                  <div  v-for="(ip,index) in props.row.ipv4"
                     :class="tapDecorator( props.row.captures, index )" 
                     :key="index" 
                     @click="handlePcap( props.row, index )">
                    {{ ip }}
                  </div>
                </div>
                </b-tooltip>
               </template>
               <template  v-else>
                  {{  props.row.ipv4 | stringify | lowercase }}
               </template>
              </b-table-column>
              <b-table-column field="network" label="Network">
                <template v-if="experimentUser() && props.row.running && !props.row.busy">                  
                  <b-tooltip  label="change vlan(s)" type="is-dark">
                    <div class="field">
                      <div  v-for="( n, index ) in props.row.networks" 
                        :key="index" 
                        @click="vlanModal.active = true 
                        vlanModal.vmName = props.row.name; 
                        vlanModal.vmFromNet = n; 
                        vlanModal.vmNet = props.row.networks; 
                        vlanModal.vmNetIndex = index">
                        {{ n | lowercase }}
                      </div>
                    </div>
                  </b-tooltip>
                </template>
                <template v-else>
                  {{  props.row.networks | stringify | lowercase }}
                </template>
              </b-table-column>
              <b-table-column field="taps"  label="Taps">
                <template v-if="experimentUser() && props.row.running && !props.row.busy">
                  <b-tooltip  :label="updateCaptureLabel(props.row)" type="is-dark">
                    <div class="field">
                      <div  v-for="( t, index ) in props.row.taps" 
                        :class="tapDecorator( props.row.captures, index )" 
                        :key="index" 
                        @click="handlePcap( props.row, index )">
                        {{ t | lowercase }}
                      </div>
                    </div>
                  </b-tooltip>
                </template>
                <template v-else>
                  {{  props.row.taps | stringify | lowercase }}
                </template>
              </b-table-column>
              <b-table-column field="uptime"  label="Uptime" width="165">
                {{ props.row.uptime | uptime }}
              </b-table-column>
            </template>
          </b-table>
          <br>
          <b-field  v-if="paginationNeeded" grouped position="is-right">
            <div class="control is-flex">
              <b-switch v-model="table.isPaginated" @input="switchPagination" size="is-small" type="is-light">Paginate</b-switch>
            </div>
          </b-field>
        </b-tab-item>
        <b-tab-item label="Files">
          <template v-if="files &&  !files.length">
            <section class="hero is-light is-bold is-large">
              <div  class="hero-body">
                <div class="container" style="text-align: center">
                  <h1 class="title">
                    There are no files available.
                  </h1>
                </div>
              </div>
            </section>
          </template>
          <template v-else>
            <ul class="fa-ul" style="list-style:none">
              <li v-for="(  f, index ) in files" :key="index">
                <font-awesome-icon class="fa-li" icon="file-download" />
                <a :href="'/api/v1/experiments/' 
                  + experiment.name 
                  + '/files/' 
                  + f 
                  + '?token=' 
                  + $store.state.token" target="_blank">
                  {{  f }}
                </a>
              </li>
            </ul>
          </template>
        </b-tab-item>
      </b-tabs>
    </div>
    <b-loading :is-full-page="true" :active.sync="isWaiting" :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  export  default {
    async beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },

    async created () {
      this.$options.sockets.onmessage = this.handler;
      this.updateExperiment();
    },

    computed: {
      filteredData () {
        return this.search.vms.filter( vm => {
          return vm.toLowerCase().indexOf( this.search.filter.toLowerCase() ) >= 0
        })
      },

      paginationNeeded  () {
        if ( this.table.total <= this.table.perPage ) {
          return  false;
        }

        return true;
      },

      /*validate () {
        var regexp = /^[ a-zA-Z0-9-_ ]*$/;
    
        if ( !regexp.test( this.diskImageModal.name ) ) {
          this.diskImageModal.nameErrType = 'is-danger';
          this.diskImageModal.nameErrMsg  = 'image names can only contain alphanumeric, dash, and underscore; we will add the file extension';
          return false;
     	}
      },*/ 
 
      isMultiVmSelected ()  {        
        if (this.vmSelectedArray == undefined || this.vmSelectedArray.length ==0) {
          return  false;        
        }
        return true;
      },
          
    },

    methods: {
      adminUser ()  {
        return [ 'Global Admin', 'Experiment Admin' ].includes( this.$store.getters.role );
      },

      experimentUser  () {
        return [ 'Global Admin', 'Experiment Admin', 'Experiment User' ].includes( this.$store.getters.role );
      },

      experimentViewer  () {
        return [ 'Experiment Viewer' ].includes( this.$store.getters.role );
      },

      searchVMs( term ) {
        if ( term == null ) {
			term = '';
        }
        this.search.filter = term;
        this.updateTable();
      },

      switchPagination( enabled ) {
        this.table.isPaginated = enabled;
        this.updateTable();
      },

      updateTable ()  {
        let number = this.table.currentPage;
        let size = this.table.perPage;

        if ( !this.table.isPaginated ) {
          number  = 0;
          size  = this.experiment.vm_count;
        }

        let msg = {
          resource: {
            type: 'experiment/vms',
            name: this.$route.params.id,
            action: 'list'
          },
          request:  {
            sort_column: this.table.sortColumn,
            sort_asc: this.table.sortOrder === 'asc',
            page_number: number,
            page_size: size,
            filter: this.search.filter
          }
        };

        this.$socket.send( JSON.stringify( msg ) );
      },

      onPageChange  ( page ) {
        this.table.currentPage = page;
        this.updateTable();
      },

      onSort  ( column, order ) {
        this.table.sortColumn = column;
        this.table.sortOrder = order;
        this.updateTable();
      },

      handler ( event ) {
        event.data.split( /\r?\n/ ).forEach( m => {
          let msg = JSON.parse( m );
          this.handle(  msg );
        });
      },
    
      handle  ( msg ) {
        switch ( msg.resource.type ) {
          case  'experiment/vms': {
            if ( msg.resource.action != 'list' ) {
              return;
            }

            
            this.experiment.vms = [ ...msg.result.vms ];

            if ( this.search.filter ) {
              this.table.total  = msg.result.total;
            } else {
              this.table.total  = this.experiment.vm_count;
            }

            this.isWaiting = false;

            break;
          }

          case  'experiment/vm': {
            let vm = msg.resource.name.split( '/' );
            let vms = this.experiment.vms;

            switch ( msg.resource.action ) {
              case  'update': {                
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == msg.result.name ) {
                    vms[i] = msg.result;                   
                    break;
                  }
                }

                break;
              }

              case  'delete': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms.splice( i, 1 );
                    break;
                  }
                }

                this.$buefy.toast.open({
                  message:  'The ' + vm[ 1 ] + ' VM was killed.',
                  type: 'is-success'
                });

                break;
              }

              case  'start': {
                break;
              }

              case  'starting': {
                break;
              }

              case  'stop': {
                break;
              }

              case  'stopping': {
                break;
              }

              case  'redeploying': {
                break;
              }
                    
              case  'shutdown':{                
                break;
                    
              }
			  
              case  'redeployed': {
                this.$buefy.toast.open({
                  message:  'Redeployed ' + vm[ 1 ],
                  type: 'is-success'
                });
                var i=0;
                for (i=0; this.redeployModal.actionsQueue.length; i++) {
                  if  (this.redeployModal.actionsQueue[i].name == vm [ 1 ]) {
                    break;
                  }
                }
                this.redeployModal.actionsQueue.splice( i, 1 );
                if(this.redeployModal.actionsQueue.length > 0) {
                  let url = this.redeployModal.actionsQueue[0].url;
                  let body  = this.redeployModal.actionsQueue[0].body;
                  let name  = this.redeployModal.actionsQueue[0].name;
                  this.$http.post(url,  body)
                   .then(null,response  => {
                     this.$buefy.toast.open({
                     message: 'Redeploying the ' + name + ' VM failed with ' + response.status + ' status.',
                     type: 'is-danger',
                     duration: 4000
                   });
                  })
                } else { 
                  this.redeployModal.active = false;  
                  this.resetRedeployModal();
                  this.isWaiting  = false;
                }
 
                break;
              }
            }

            break;
          }

          case  'experiment/vm/commit': {
            let vm = msg.resource.name.split( '/' );
            let vms = this.experiment.vms;

            switch ( msg.resource.action ) {

              case  'commit': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = false;
                    vms[i] = msg.result.vm;

                      let disk  = msg.result.disk;

                      this.$buefy.toast.open({
                        message: 'The backing image with name ' + disk + ' for the ' + vm[ 1 ] + ' VM was successfully created.',
                        type: 'is-success',
                        duration: 4000
                      });
                    }
                
                    this.experiment.vms = [ ...vms ];
                  }
                  break;
                }

              case  'committing': {
                 //this.$buefy.toast.open({
                 //     message: 'COMMITING',
                 //     duration: 200
                 //   });

                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = true;
                    vms[i].percent = 0;
					
                    let disk = msg.result.disk;
                
                    this.$buefy.toast.open({
                      message:  'A backing image with name ' + disk + ' for the ' + vm[ 1 ] + ' VM is being created.',
                      type: 'is-warning',
                      duration: 4000
                    });
                
                    this.experiment.vms = [ ...vms ];
                  }

                  break;
                }

                break;
              }

              case  'progress': {
                 //this.$buefy.toast.open({
                 //     message: 'PROGRESS',
                 //     duration: 200
                 //   });
                let percent = ( msg.result.percent * 100 ).toFixed( 0 );

                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = true; //incase committing message is missed
                    vms[i].percent = percent;
                    this.experiment.vms = [ ... vms ];

                    break;
                  }
                }

                break;
              }
            }

            break;
          }

          case  'experiment/vm/screenshot': {
            let vm = msg.resource.name.split( '/' );
            let vms = this.experiment.vms;

            switch ( msg.resource.action ) {
              case  'update': {                
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].screenshot = msg.result.screenshot;
                    break;
                  }
                }

                this.experiment.vms = [ ...vms ];                

                break;
              }
            }

            break;
          }

          case  'experiment/vm/capture': {
            let vm = msg.resource.name.split( '/' );
            let vms = this.experiment.vms;

            switch ( msg.resource.action ) {
              case  'start': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {                    
                    if ( vms[i].captures == null ) {
                      vms[i].captures = [];
                    }

                    vms[i].captures.push ({ 
                      "vm": vm[ 1 ],  
                      "interface":  msg.result.interface, 
                      "filename": msg.result.filename 
                    });

                    break;
                  }
                }

                this.experiment.vms = [ ...vms ]; 

                this.$buefy.toast.open({
                  message:  'Packet capture was started for the ' + vm[ 1 ] + ' VM.',
                  type: 'is-success'
                });

                break;
              }

              case  'stop': {                
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].captures = [];
                    break;
                  }
                }

                this.$buefy.toast.open({
                  message:  'Packet capture was stopped for the ' + vm[ 1 ] + ' VM.',
                  type: 'is-success'
                });

                break;
              }
            }

            break;
          }

          case  'experiment/vm/snapshot': {
            let vm = msg.resource.name.split( '/' );
            let vms = this.experiment.vms;

            switch ( msg.resource.action ) {
              case  'create': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = false;             
                    this.$buefy.toast.open({
                      message:  'The snapshot for the ' + vm[ 1 ] + ' VM was successfully created.',
                      type: 'is-success',
                      duration: 4000
                    });
                  }

                  this.experiment.vms = [ ...vms  ];
                }

                break;
              }

              case  'creating': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = true;
                    vms[i].percent = 0;
                    this.$buefy.toast.open({
                      message:  'A snapshot for the ' + vm[ 1 ] + ' VM is being created.',
                      type: 'is-warning',
                      duration: 4000
                    });
                  }
              
                  this.experiment.vms = [ ...vms  ];
                }

                break;
              }

              case  'progress': {
                let percent = ( msg.result.percent * 100 ).toFixed( 0 );

                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].percent = percent;
                    this.experiment.vms = [ ... vms ];
                    break;
                  }
                }

                break;
              }

              case  'restore': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = false;          
                    this.$buefy.toast.open({
                      message:  'The ' + vm[ 1 ] + ' VM was successfully reverted to a previous snapshot.',
                      type: 'is-success',
                      duration: 4000
                    });
                  }

                  this.experiment.vms = [ ...vms  ];
                }

                break;
              }

              case  'restoring': {
                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = true;
                    vms[i].percent = 0;              
                    this.$buefy.toast.open({
                      message:  'A snapshot for the ' + vm[ 1 ] + ' VM is being restored.',
                      type: 'is-warning',
                      duration: 4000
                    });
                  }
              
                  this.experiment.vms = [ ...vms  ];
                }

                break;
              }
            }

            break;
          }
        }
      },
    
      async updateExperiment  () {
        try {
          let resp  = await this.$http.get('experiments/' + this.$route.params.id);
          let state = await resp.json();

          this.experiment = state;
          this.search.vms = state.vms.map(  vm => { return vm.name } );
          this.table.total  = state.vm_count;          

          this.updateTable(); 
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
    
      updateDisks ()  {
        this.disks = [];
      
        this.$http.get( 'disks' ).then(
          response  => {
            response.json().then(
              state =>  {
                if ( state.disks.length == 0 ) {
                  this.isWaiting  = true;
                } else {
                  for ( let i = 0;  i < state.disks.length; i++ ) {
                    this.disks.push( state.disks[i] );
                  }
                  
                  this.isWaiting  = false;
                }
              }
            );
          },  response => {
            console.log('Getting the disks failed with ' + response.status);
            this.isWaiting = false;
            this.$toast.open({
              message:  'Getting the disks failed.',
              type: 'is-danger',
              duration: 4000
            });
          }
        );
      },

      updateFiles ()  {
        this.files = [];

        this.$http.get( 'experiments/' + this.$route.params.id + '/files' ).then(
          response  => {
            response.json().then(
              state =>  {
                for ( let i = 0; i < state.files.length; i++ ){
                  this.files.push(  state.files[i] );
                }

                this.isWaiting = false;
              }
            );
          },  () => {
            this.$buefy.toast.open({
              message:  'Getting the files failed.',
              type: 'is-danger',
              duration: 4000
            });

            this.isWaiting = false;
          }
        );
      },

      getInfo ( vm  ) {
        if(vm.busy){
          this.$buefy.dialog.alert({
            title: 'VM Busy',
            message: ['VM',vm.name,'is currently busy and not available for another action'].join(' '),
            confirmText: 'Ok'
          })       
          return
        }
        
          this.$http.get(
            'experiments/' + this.$route.params.id + '/vms/' + vm.name + '/snapshots'
          ).then(
            response => { 
              return  response.json().then(
                json => {
                  if  ( json.snapshots.length > 0 ) {
                    this.expModal.snapshots = json.snapshots;
                  }
                }
              )
            }, response => {
              this.$buefy.toast.open({
                message: 'Getting info for the ' + name + ' VM failed with ' + response.status + ' status.',
                type: 'is-danger',
                duration: 4000
              });

              this.isWaiting  = false;
            }
          );

          this.expModal.vm  = vm;
          this.expModal.active  = true;
        
      },

      snapshots ( vm  ) {
        this.$http.get(
          'experiments/'  + this.$route.params.id + '/vms/' + vm.name + '/snapshots'
        ).then(
          response  => { 
            return response.json().then(
              json  => {
                if ( json.snapshots.length > 0 ) {
                  return  true;
                }
              }
            )
          },  response => {
            this.$buefy.toast.open({
              message:  'Retrieving the snapshots for the ' + name + ' VM failed with ' + response.status + ' status.',
              type: 'is-danger',
              duration: 4000
            });

            this.isWaiting = false;
          }
        );
      },

      captureSnapshot ( name  ) {
        if (! Array.isArray(name)) {
          name  = [name];
        }
        var dateTime = new Date();
        var time = dateTime.getFullYear() 
          + '-' 
          + ( '0' + (dateTime.getMonth()+1) ).slice(-2) 
          + '-' 
          + ( '0' + dateTime.getDate()  ).slice(-2) 
          + '_' 
          + ( '0' + dateTime.getHours() ).slice(-2) 
          + ( '0' + dateTime.getMinutes() ).slice(-2);

        this.$buefy.dialog.confirm({
          title:  'Create a VM Snapshot',
          message:  'This will create a snapshot for the VMs ' + name,
          cancelText: 'Cancel',
          confirmText:  'Create',
          type: 'is-success',
          hasIcon:  true,
          onConfirm:  () => {            
            this.resetExpModal(); 
            name.forEach((vmName) => { 
              this.$http.post(
                'experiments/' + this.$route.params.id + '/vms/' + vmName + '/snapshots',
                { "filename": time }, { timeout: 0 }
              ).then(
                response => {
                  if  ( response.status == 204 ) {
                    console.log('create snapshot for vm ' + vmName);
                  }
                }, response => {
                  this.$buefy.toast.open({
                    message: 'Creating the snapshot for the ' + vmName + ' VM failed with ' + response.status + ' status.',
                    type: 'is-danger',
                    duration: 4000
                  });
                }
              );
            })
          }
        })
      },

      restoreSnapshot ( name, snapshot  ) {
        this.$buefy.dialog.confirm({
          title:  'Restore a VM Snapshot',
          message:  'This will revert the ' + name + ' VM to ' + snapshot + '.',
          cancelText: 'Cancel',
          confirmText:  'Revert',
          type: 'is-success',
          hasIcon:  true,
          onConfirm:  () => {            
            this.resetExpModal();

            this.$http.post(
              'experiments/'  + this.$route.params.id + '/vms/' + name + '/snapshots/' + snapshot,
              {}, { timeout:  0 }
            ).then(
              response  => {
                if ( response.status == 204 ) {
                  console.log('restore  snapshot for vm ' + name);
                }
              },  response => {
                this.$buefy.toast.open({
                  message:  'Restoring the ' 
                  + snapshot  
                  + ' snapshot  for the ' 
                  + name  
                  + ' VM  failed with ' 
                  + response.status 
                  + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });
              }
            );
          }
        })
      },
    
      diskImage (name)  {
        var now = new Date();
        var date = now.getFullYear()
          + ''  + ( '0' + now.getMonth() + 1 ).slice( -2 )
          + ''  + now.getDate();
        var time = ( '0' + now.getHours() ).slice( -2 )
          + ''  + ( '0' + now.getMinutes() ).slice( -2 )
          + ''  + ( '0' + now.getSeconds() ).slice( -2 );
        if (! Array.isArray(name)) {
          name  = [name];
        }
        let vms = this.experiment.vms;
        name.forEach((arg,) => {
          for ( let i = 0; i < vms.length; i++ ) {
            if ( vms[i].name == arg ){
			  var filename=""; 
			  if ( /(.*)_\d{14}/.test( vms[i].disk ) ) {
			    filename = vms[i].disk.substring( 0, vms[i].disk.indexOf( '_' ) ) + '_' + date + time;
			  } else {
			    filename = vms[i].disk.substring( 0, vms[i].disk.indexOf( '.' ) ) + '_' + date + time;
			  }
                          filename = vms[i].name +"_"+ filename.substring(filename.lastIndexOf( '/')+1 ); 
                          this.diskImageModal.vm.push({
                            dateTime:date+time+"" ,
                            name:vms[i].name ,
                            filename:filename ,
                            nameErrType:"" ,
                            nameErrMsg:""
                          });
			}
		  }
        })
        
        this.diskImageModal.active = true;
      },
    
      backingImage (vm) {
        let vmList = "";
        vm.forEach((arg,) => {
          vmList = vmList + arg.name + ", ";
        })
	vmList = vmList.slice(0,-2)
	this.$buefy.dialog.confirm({
          title: 'Create a Disk Images',
          message: 'This will create a backing image for the VMs ' + vmList,
          cancelText: 'Cancel',
          confirmText: 'Create',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.diskImageModal.active = false;
            this.resetDiskImageModal();
            this.resetExpModal();
            let url = "";
            let name = "";
            let body = "";
	    vm.forEach((arg,) => {
              url = 'experiments/' + this.$route.params.id + '/vms/' + arg.name + '/commit';
              body = { "filename": arg.filename  + '.qc2' };
              name = arg.name;
            
              this.$http.post(url,body,{ timeout: 0 }).then(
                response => {
                   console.log('backing image for vm ' + name + ' failed with ' + response.status);
                }, response => {
                  this.$buefy.toast.open({
                     message: 'Creating the backing image for the ' + name + ' VM failed with ' + response.status + ' status.',
                     type: 'is-danger',
                     duration: 4000
                   });
                }
              );
            })
	  }
        })
        
        this.diskImageModal.active = true;
      },
      killVm ( name ) {
        if (! Array.isArray(name)) {
          name  = [name];
        }
        let vmList = [];
        let vmExcludeList = [];
        let vms = this.experiment.vms;
        name.forEach((arg,) => {
          for ( let i = 0; i < vms.length; i++ ) {
            if ( vms[i].name == arg ){
              if( vms[i].running ) {
                vmList.push(arg);
              } else {
                vmExcludeList.push(arg);
              }
            } 
          }
        })
        if ( vmExcludeList.length > 0) {
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: 'VMs '+ vmExcludeList.join(", ") +' are either paused or killed',
            confirmText: 'Ok'
          })
        }
        if (vmList.length >0) { 
          this.$buefy.dialog.confirm({
            title: 'Kill the VMs',
            message: 'This will kill the VMs ' 
            + vmList.join(", ") 
            + '. You will not be able to restore this VM until you restart the ' 
            + this.$route.params.id 
            + ' experiment!',
            cancelText: 'Cancel',
            confirmText: 'KILL THEM!',
            type: 'is-danger',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting= true;
              this.resetExpModal();
              vmList.forEach((arg,) => {
                this.$http.delete(
                'experiments/' + this.$route.params.id + '/vms/' + arg
                ).then(
                  response  => {
                    if ( response.status == 204 ) {
                      let vms = this.experiment.vms;
                      for ( let i = 0; i < vms.length; i++ ) {
                        if ( vms[i].name == arg ) {
                          vms.splice( i, 1 );
                          break;
                        }
                      }
                      this.experiment.vms = [ ...vms  ];
                      this.isWaiting  = false;
                    }
                  },  response => {
                    this.$buefy.toast.open({
                      message: 'Killing the ' + arg + ' VM failed with ' + response.status + ' status.',
                      type: 'is-danger',
                      duration: 4000
                    });
                    this.isWaiting = false;
                  }
                );
              })
            }
          })
        }        
      },

      stop  () {      
        this.$buefy.dialog.confirm({
          title:  'Stop the Experiment',
          message:  'This will stop the ' + this.$route.params.id + ' experiment.',
          cancelText: 'Cancel',
          confirmText:  'Stop',
          type: 'is-danger',
          hasIcon:  true,
          onConfirm:  () => {
            this.isWaiting= true;

            this.$http.post(
              'experiments/'  + this.$route.params.id + '/stop' 
            ).then(
              ()  => {
                this.$router.replace('/experiments/');                
              },  response => {
                this.$buefy.toast.open({
                  message:  'Stopping experiment ' + this.$route.params.id + ' failed with ' + response.status + ' status.',
                  type: 'is-danger',
                  duration: 4000
                });

                this.isWaiting = false;
              }
            );
          }
        })
      },

      decorator ( state,  busy ) {
        if ( busy ) {
          return  'is-warning'
        }

        switch (state) {
          case  "RUNNING":
            return 'is-success'
            
            
          case  "PAUSED":
            return 'is-warning'
            
            
          case  "QUIT":
            return 'is-danger'
             
             
            
        }
      },

      tapDecorator  ( captures, iface ) {        
        if ( captures.length > 0 ) {
          for ( let i = 0;  i < captures.length; i++ ) {
            if ( captures[i].interface === iface ) {
              return  'is-success'
            }
          }
        }
      },

      handlePcap  ( vm, iface ) {  
        
        var dateTime = new Date();
        var time = dateTime.getFullYear() 
          + '-' 
          + ( '0' + ( dateTime.getMonth() +1  ) ).slice( -2 ) 
          + '-' 
          + ( '0' + dateTime.getDate()  ).slice( -2 ) 
          + '_' 
          + ( '0' + dateTime.getHours() ).slice(  -2 ) 
          + ( '0' + dateTime.getMinutes() ).slice(  -2 );

        this.$http.get(
          'experiments/'  + this.$route.params.id + '/vms/' + vm.name + '/captures'
        ).then(
          response  => { 
            return response.json().then(
              json  => {
                let captures  = json.captures;
                let capturing = false;

                if ( captures ) {
                  for ( let i = 0;  i < captures.length; i++ ) {
                    if ( captures[i].interface === iface ) {
                      capturing = true;
                      break;
                    }
                  }
                }

                if ( capturing ) {
                  this.$buefy.dialog.confirm({
                    title: 'Stop All Packet Captures',
                    message: 'This will stop all packet captures for the ' + vm.name + ' VM.',
                    cancelText: 'Cancel',
                    confirmText: 'Stop',
                    type: 'is-danger',
                    hasIcon: true,
                    onConfirm: () => {
                      this.isWaiting  = true;

                      this.$http.delete(
                        'experiments/' + this.$route.params.id + '/vms/' + vm.name + '/captures' 
                      ).then(
                        response => {
                          if  ( response.status == 204 ) {
                            let vms = this.experiment.vms;

                            for ( let i = 0; i < vms.length; i++ ) {
                              if  ( vms[i].name == response.body.name ) {
                                vms[i] = response.body;
                                break;
                              }
                            }

                            this.experiment.vms = [ ...vms ]
                            this.isWaiting = false;
                          }
                        }, response => {
                          this.$buefy.toast.open({
                            message: 'Stopping all packet captures for the ' 
                            + vm.name 
                            + ' VM failed with ' 
                            + response.status 
                            + ' status.',
                            type: 'is-danger',
                            duration: 4000
                          });

                          this.isWaiting  = false;
                        }
                      )
                    }
                  })         
                } else if ( vm.networks[ iface ] == 'disconnected' ) {
                  this.$buefy.toast.open({
                    message: 'Cannot capture traffic on a disconnected interface.',
                    type: 'is-danger',
                    duration: 4000
                  });
                } else {
                  this.$buefy.dialog.confirm({
                    title: 'Start a Packet Capture',
                    message: 'This will start a packet capture for the ' + vm.name + ' VM, interface ' + iface + '.',
                    cancelText: 'Cancel',
                    confirmText: 'Start',
                    type: 'is-success',
                    hasIcon: true,
                    onConfirm: () => {
                      this.isWaiting  = true;

                      this.$http.post(
                        'experiments/' 
                        + this.$route.params.id 
                        + '/vms/' 
                        + vm.name 
                        + '/captures', { "interface": iface, "filename": [vm.name,iface,time].join("_") + ".pcap" } 
                      ).then(
                        response => {
                          if  ( response.status == 204 ) {
                            let vms = this.experiment.vms;

                            for ( let i = 0; i < vms.length; i++ ) {
                              if  ( vms[i].name == response.body.name ) {
                                vms[i] = response.body;
                                break;
                              }
                            }

                            this.experiment.vms = [ ...vms ]
                            this.isWaiting = false;
                          }
                        }, response => {
                          this.$buefy.toast.open({
                            message: 'Starting packet capture for the ' + vm.name + ' VM failed with ' + response.status + ' status.',
                            type: 'is-danger',
                            duration: 4000
                          });

                          this.isWaiting  = false;
                        }
                      )
                    }
                  })
                }
              }
            )
          }
        );
      },

      startVm (name)  {
        if (! Array.isArray(name)) {
          name  = [name];
        }
        let vmList = [];
        let vmExcludeList = [];
        let vms = this.experiment.vms;
        name.forEach((arg,) => {
          for ( let i = 0; i < vms.length; i++ ) 
          {
            if ( vms[i].name == arg ){
              if( !vms[i].running ) {
                vmList.push(arg);
              } else {
                vmExcludeList.push(arg);
              }
            } 
          }
        })
        if ( vmExcludeList.length > 0) {
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: 'VMs '+ vmExcludeList.join(", ") +' are already running',
            confirmText: 'Ok'
          })
        }
        if (vmList.length >0) { 
          this.$buefy.dialog.confirm({
            title: 'Start the VMs',
            message: 'This will start the VMs ' + vmList.join(", "),
            cancelText: 'Cancel',
            confirmText: 'Start',
            type: 'is-success',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting = true;
              this.resetExpModal();
               vmList.forEach((arg,) => { 
                 this.$http.post(
                   'experiments/' + this.$route.params.id + '/vms/' + arg + '/start' 
                 ).then(
                   response =>  {
                     let vms = this.experiment.vms;
                    for ( let i = 0; i < vms.length; i++ ) {
                      if  ( vms[i].name == response.body.name ) {
                        vms[i] = response.body;
                        break;
                      }
                    }
                    this.experiment.vms = [ ...vms ];
                    this.isWaiting = false;
                  },  response => {
                    this.$buefy.toast.open({
                      message: 'Starting the ' + arg + ' VM failed with ' + response.status + ' status.',
                      type: 'is-danger',
                      duration: 4000
                    });
                    this.isWaiting = false;
                  }
                );
              })
            }
          })
        }       
      },
	  
      pauseVm (name)  {
        if (! Array.isArray(name)) {
          name  = [name];
        }  
        let vmList = [];
        let vmExcludeList = [];
        let vms = this.experiment.vms;
        name.forEach((arg,) => {
          for ( let i = 0; i < vms.length; i++ ) {
            if ( vms[i].name == arg ){
              if( vms[i].running ) {
                vmList.push(arg);
              } else {
                vmExcludeList.push(arg);
              }
            } 
          }
        })
        if ( vmExcludeList.length > 0) {
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: 'VMs ' + vmExcludeList.join(", ") + ' are not running',
            confirmText: 'Ok'
          })
        } 
        if (vmList.length > 0) {
          this.$buefy.dialog.confirm({
            title: 'Pause the VMs',
            message: 'This will pause the VMs ' + vmList.join(", "),
            cancelText: 'Cancel',
            confirmText: 'Pause',
            type: 'is-success',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting = true;
              this.resetExpModal();
              vmList.forEach((arg,) => { 
                this.$http.post(
                  'experiments/' + this.$route.params.id + '/vms/' + arg + '/stop' 
                ).then(
                  response  => {
                    let vms = this.experiment.vms;
                    for ( let i = 0; i < vms.length; i++ ) {
                      if  ( vms[i].name == response.body.name ) {
                        vms[i] = response.body;
                        break;
                      }
                    } 
                    this.experiment.vms = [ ...vms ];
                    this.isWaiting = false;
                  },  response => {
                    this.$buefy.toast.open({
                      message: 'Pausing the ' + arg + ' VM failed with ' + response.status + ' status.',
                      type: 'is-danger',
                      duration: 4000
                    });
                    this.isWaiting = false;
                  } 
                );
              })
            }      
          })
        }
      },
        
      resetVmState  (name) {
        if (! Array.isArray(name)) {
          name  = [name];
        } 
        
        let vmList = [];
        let vmExcludeList = [];
        let vms = this.experiment.vms;
        name.forEach((vmName) => {
          for ( let i = 0;  i < vms.length; i++ ) {
            if ( vms[i].name == vmName ){
              if( vms[i].state.toUpperCase()  != "PAUSED" ) {
                vmList.push(vmName)
              } else  {
                vmExcludeList.push(vmName)
              }
            } 
          }
        })
        if ( vmExcludeList.length > 0) {          
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: 'VMs ' + vmExcludeList.join(", ") +' are currently paused.  Unable to reset paused VMs',
            confirmText: 'Ok'
          })
        } 
        
        if (vmList.length > 0) {
          this.$buefy.dialog.confirm({
              title:  'Reset VMs Disk State',
              message:  'This will reset the disk state for VMs ' + vmList.join(", "),
              cancelText: 'Cancel',
              confirmText:  'Reset',
              type: 'is-success',
              hasIcon:  true,
              onConfirm:  () => {
                this.isWaiting = true; 
                this.resetExpModal();
                vmList.forEach((vmName) => { 
                  this.$http.get(
                    'experiments/' + this.$route.params.id + '/vms/' + vmName + '/reset' 
                  ).then(
                    response => {
                      let vms = this.experiment.vms;
                      for ( let i = 0;  i < vms.length; i++ ) {
                        if ( vms[i].name == response.body.name ) {
                          vms[i]  = response.body;
                          break;
                        }
                      } 
                      this.experiment.vms = [ ...vms  ];
                      this.isWaiting  = false;
                    }, response => {
                      this.$buefy.toast.open({
                        message: 'Reseting the ' + vmName + ' VM failed with ' + response.status + ' status.',
                        type: 'is-danger',
                        duration: 4000
                      });
                      this.isWaiting  = false;
                    } 
                  );
                })        
            }
          })
        }
             
      },
        
      restartVm (name)  {
        if (! Array.isArray(name)) {
          name  = [name];
        } 
        
        let vmList = [];
        let vmExcludeList = [];
        let vms = this.experiment.vms;
        name.forEach((vmName) => {
          for ( let i = 0;  i < vms.length; i++ ) {
            if ( vms[i].name == vmName ){
              if( vms[i].state.toUpperCase()  != "PAUSED" ) {
                vmList.push(vmName)
              } else  {
                vmExcludeList.push(vmName)
              }
            } 
          }
        })
        if ( vmExcludeList.length > 0) {          
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: 'VMs ' + vmExcludeList.join(", ") +' are currently paused.  Unable to restart paused VMs',
            confirmText: 'Ok'
          })
        } 
        
        if (vmList.length > 0) {
          this.$buefy.dialog.confirm({
              title:  'Restart the VMs',
              message:  'This will restart the VMs ' + vmList.join(", "),
              cancelText: 'Cancel',
              confirmText:  'Restart',
              type: 'is-success',
              hasIcon:  true,
              onConfirm:  () => {
                this.isWaiting = true; 
                this.resetExpModal();
                vmList.forEach((vmName) => { 
                  this.$http.get(
                    'experiments/' + this.$route.params.id + '/vms/' + vmName + '/restart' 
                  ).then(
                    response => {
                      let vms = this.experiment.vms;
                      for ( let i = 0;  i < vms.length; i++ ) {
                        if ( vms[i].name == response.body.name ) {
                          vms[i]  = response.body;
                          break;
                        }
                      } 
                      this.experiment.vms = [ ...vms  ];
                      this.isWaiting  = false;
                    }, response => {
                      this.$buefy.toast.open({
                        message: 'Restarting the ' + vmName + ' VM failed with ' + response.status + ' status.',
                        type: 'is-danger',
                        duration: 4000
                      });
                      this.isWaiting  = false;
                    } 
                  );
                })        
            }
          })
        }
             
      },
        
      shutdownVm  (name) {
        if (! Array.isArray(name)) {
          name  = [name];
        } 
        
        let vmList = [];
        let vmExcludeList = [];
        let vms = this.experiment.vms;
        name.forEach((vmName) => {
          for ( let i = 0;  i < vms.length; i++ ) {
            if ( vms[i].name == vmName ){
              if( vms[i].running  ) {
                vmList.push(vmName)
              } else  {
                vmExcludeList.push(vmName)
              }
            } 
          }
        })
        if ( vmExcludeList.length > 0) {          
          this.$buefy.dialog.alert({
            title: 'No Action',
            message: 'VMs ' + vmExcludeList.join(", ") +' are not running.  Unable to shutdown vms that are not running',
            confirmText: 'Ok'
          })
        } 
        
        if (vmList.length > 0) {
          this.$buefy.dialog.confirm({
              title:  'Shutdown the VMs',
              message:  'This will shutdown the VMs ' + vmList.join(", "),
              cancelText: 'Cancel',
              confirmText:  'Shutdown',
              type: 'is-danger',
              hasIcon:  true,
              onConfirm:  () => {
                this.isWaiting = true;   
                this.resetExpModal();
                vmList.forEach((vmName) => { 
                  this.$http.get(
                    'experiments/' + this.$route.params.id + '/vms/' + vmName + '/shutdown' 
                  ).then(
                    response => {
                      let vms = this.experiment.vms;
                      for ( let i = 0;  i < vms.length; i++ ) {
                        if ( vms[i].name == response.body.name ) {
                          vms[i]  = response.body;                          
                          vms[i].running  = false;
                          break;
                        }
                      } 
                      this.experiment.vms = [ ...vms  ];                      
                      this.isWaiting  = false;
                    }, response => {
                      this.$buefy.toast.open({
                        message: 'Shut down of VM ' + vmName + ' failed with ' + response.status + ' status.',
                        type: 'is-danger',
                        duration: 4000
                      });
                      this.isWaiting  = false;
                    } 
                  );
                })        
            }
          })
        }
      },
      
      redeploy  ( vm ) {
        if (! Array.isArray(vm)) {
          vm  = [vm];
        }
        this.updateDisks();
        let vms = this.experiment.vms;
        vm.forEach((arg,_) => {
          for ( let i = 0; i < vms.length; i++ ) {
            if ( vms[i].name == arg ) {
              this.redeployModal.vm.push({
                name:vms[i].name,
                cpus:vms[i].cpus,
                ram:vms[i].ram,
                disk:vms[i].disk.substring(vms[i].disk.lastIndexOf('/')+1 ), 
                inject:false
              })
           }
          }
        })

        this.redeployModal.active = true;
      },
      
      closeModal(modalName) {      
        this.$refs[modalName].cancel('x')
      },
      
      redeployVm  (vms) {
        let body = "";
        let url = "";
	let name = "";
        vms.forEach((vm,_) => {
          body = { "cpus": parseInt(vm.cpus), "ram": parseInt(vm.ram), "disk": vm.disk }
          url = 'experiments/' + this.$route.params.id + '/vms/' + vm.name + '/redeploy'
		  name = vm.name;
          if  ( vm.inject ) {
            url += '?replicate-injects=true'
          }
          this.redeployModal.actionsQueue.push({name: vm.name,  url: url, body:body});
       })
	   //kick off the first one
       this.$http.post(url, body)
         .then(null,response => {
           this.$buefy.toast.open({
             message: 'Redeploying the ' + name + ' VM failed with ' + response.status + ' status.',
             type: 'is-danger',
             duration: 4000
           });
         })

         this.isWaiting = true;
         this.resetExpModal();
//        this.redeployModal.active = false;
//        this.resetRedeployModal();
      },

      changeVlan  ( index, vlan, from, name ) {        
        if ( vlan === '0' ) {
          this.$buefy.dialog.confirm({
            title: 'Disconnect a VM Network Interface',
            message: 'This will disconnect the ' + index + ' interface for the ' + name + ' VM.',
            cancelText: 'Cancel',
            confirmText: 'Disconnect',
            type: 'is-warning',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting  = true;

              let update  = { "interface": { "index": index, "vlan": "" } };

              this.$http.patch(
                'experiments/' + this.$route.params.id + '/vms/' + name, update
              ).then(
                response => {
                  let vms = this.experiment.vms;

                  for ( let i = 0;  i < vms.length; i++ ) {
                    if ( vms[i].name == response.body.name ) {
                      vms[i]  = response.body;
                      break;
                    }
                  }

                  this.experiment.vms = [ ...vms  ];
                  this.isWaiting  = false;
                }, response => {
                  this.$buefy.toast.open({
                    message: 'Disconnecting the network for the ' + name + ' VM failed with ' + response.status + ' status.',
                    type: 'is-danger',
                    duration: 4000
                  });

                  this.isWaiting  = false;
                }
              );
            }
          })
        } else {
          this.$buefy.dialog.confirm({
            title: 'Change the VLAN',
            message: 'This will change the VLAN from ' 
            + from.toLowerCase() 
            + ' to ' 
            + vlan.alias.toLowerCase() 
            + ' (' + vlan.vlan + ')' 
            + ' for the ' 
            + name 
            + ' VM.',
            cancelText: 'Cancel',
            confirmText: 'Change',
            type: 'is-warning',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting  = true;

              let update  = { "interface": { "index": index, "vlan": vlan.alias } };

              this.$http.patch(
                'experiments/' + this.$route.params.id + '/vms/' + name, update
              ).then(
                response => {
                  let vms = this.experiment.vms;

                  for ( let i = 0;  i < vms.length; i++ ) {
                    if ( vms[i].name == response.body.name ) {
                      vms[i]  = response.body;
                      break;
                    }
                  }

                  this.experiment.vms = [ ...vms  ];
                  this.isWaiting  = false;
                }, response => {
                  this.$buefy.toast.open({
                    message: 'Changing the VLAN for the ' + name + ' VM failed with ' + response.status + ' status.',
                    type: 'is-danger',
                    duration: 4000
                  });

                  this.isWaiting  = false;
                }
              )
            }
          })
        }

        this.vlanModal.active = false;
      },

      
      resetExpModal ()  {        
        this.expModal = {
          active: false,
          vm: [],
          snapshots:  false
        }
        this.showModifyStateBar = false;
      },
    
      resetRedeployModal  () {       
        this.redeployModal = {
          active: false,
          vm: [],
          actionsQueue: []
        }
        
      },
    
      resetDiskImageModal ()  {        
        this.diskImageModal = {
          active: false,
          vm: []
        }
      },
      
      validate  () {
        var regexp = /^[a-zA-Z0-9-_]+$/;
        for ( let i = 0; i < this.diskImageModal.vm.length; i++ ) {
            if ( !regexp.test( this.diskImageModal.vm[i].filename ) ) {
              this.diskImageModal.vm[i].nameErrType = 'is-danger';
              this.diskImageModal.vm[i].nameErrMsg   = 'image names can only contain alphanumeric, dash, and underscore; we will add the file extension';
              return  false;
            }

            this.diskImageModal.vm[i].nameErrType = '';
            this.diskImageModal.vm[i].nameErrMsg = '';
        }
        return true;
      },

      processMultiVmAction  (action) { 
        switch(action) {
              case  this.vmActions.start:
                this.startVm(this.vmSelectedArray);
                break;
              case  this.vmActions.pause:
                this.pauseVm(this.vmSelectedArray);
                break;
              case  this.vmActions.kill:
                this.killVm(this.vmSelectedArray);
                break;
              case  this.vmActions.redeploy:
                this.redeploy(this.vmSelectedArray);
                break;
              case  this.vmActions.createBacking:
                this.diskImage(this.vmSelectedArray);
                break;
              case  this.vmActions.captureSnapshot:
                this.captureSnapshot(this.vmSelectedArray);
                break;
              case  this.vmActions.restart:
                this.restartVm(this.vmSelectedArray);
                break;
              case  this.vmActions.shutdown:
                this.shutdownVm(this.vmSelectedArray);
                break;
              case  this.vmActions.resetState:
                this.resetVmState(this.vmSelectedArray);
                break;
              default:
                this.notImplemented();
        
        }
        
        this.unSelectAllVMs();
      },
  
      selectAllVMs  () {            
        
        var visibleItems = this.$refs["vmTable"].visibleData
        //If there are no visible items, there is nothing to select
        if(visibleItems.length == 0)
        {
          return 
        }    
        
        //If everything is selected, the unselect everything
        else if(this.vmSelectedArray.length == visibleItems.length)
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
        this.vmSelectedArray=[]
        
        for(var i=0; i<visibleItems.length; i++){
            this.vmSelectedArray.push(visibleItems[i].name)
        }  
                
      },
        
      unSelectAllVMs(){
        
        this.checkAll = false;
        this.vmSelectedArray=[]
        this.showModifyStateBar = false;
        
      },
  
      
      updateCaptureLabel(vm) {       
        
        return vm.captures.length == 0 ? "start packet capture" : "stop packet capture"        
        
      },
      
      notImplemented(){
        
       this.$buefy.dialog.alert({
            title: 'Not Implemented',
            message: 'This function has not yet been implemented',
            type:'is-dark',
            confirmText: 'Ok'
          }) 
      }
    },
    
    data () {
      return  {
        search: {
          vms:  [],
          filter: ''
        },
        table: {
          isPaginated:  true,
          isPaginationSimple: true,
          currentPage:  1,
          perPage:  10,
          total:  0,
          sortColumn: 'name',
          sortOrder:  'asc',
          paginationSize: 'is-small'
        },
        expModal: {
          active: false,
          vm: [],
          snapshots:  false
        },
        vlanModal: {
          active: false,
          vmName: null,
          vmFromNet:  null,
          vmNetIndex: null,
          vmNet:  []
        },
        redeployModal: {
          active: false,
          vm:[],
          actionsQueue: [],
          name: null,
          cpus: null,
          ram:  null,
          disk: null,
          inject: false
        },
        diskImageModal: {
          active: false,
          vm:[],
          /*  vm is structured as so:
           name:  null, filename: null, dateTime: null, 
           nameErrType: null, nameErrMsg: null
          */
        },
        experiment: [],
        files: [],
        disks: [],
        vlan: null,
        expName: null,
        isWaiting: true,
        showModifyStateBar:false,
        checkAll:false,
        vmSelectedArray: [],
        vmActions: { 
          start:  0,
          pause:  1,
          kill:2,
          redeploy:3,
          createBacking:4,
          createSnapshot:5,
          restart:6,
          shutdown:7,
          resetState:8,
          createMemorySnapshot:9,
          recordScreenshots:10
        },
        
      }
    }
  }
</script>

<style scoped>
div.autocomplete >>> a.dropdown-item {
  color:  #383838 !important;
}
</style>