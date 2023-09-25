<template>
  <div class="content">
    <b-modal :active.sync="expModal.active" :on-cancel="resetExpModal" has-modal-card>
      <div class="modal-card" style="width:35em">
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
          <p v-if="expModal.vm.delayed_start">Delay: {{ expModal.vm.delayed_start }}</p>
          <p>Network(s): {{ expModal.vm.networks | stringify | lowercase }}</p>
          <p>Taps: {{ expModal.vm.taps | stringify | lowercase }}</p>
          <p>CC Active: {{ expModal.vm.ccActive }}</p>
          <p v-if="expModal.snapshots">
            Snapshots:       
            <br>
            <p v-for="( snap, index ) in expModal.snapshots" :key="index">
              <b-tooltip label="restore this snapshot" type="is-light is-right">
                <b-icon icon="play-circle" style="color:#686868" @click.native="restoreSnapshot( expModal.vm.name, snap )"></b-icon>
              </b-tooltip>
              {{ snap }}
            </p>
          </p>          
          <p v-if="expModal.forwards.length !== 0">
            Port Forwards:
            <br>
            <p v-for="( forward, index ) in expModal.forwards" :key="index">
              {{ forward.desc }} ({{ forward.owner }})
              <b-tooltip v-if="forward.canDelete" label="delete this port forward" type="is-light is-right">
                <b-icon icon="trash" size="is-small" style="color:#686868" @click.native="deletePortForward(expModal.vm.name, forward)"></b-icon>
              </b-tooltip>
            </p>
          </p>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <div v-if="roleAllowed('vms/start', 'update', expModal.fullName) && !showModifyStateBar">
          <template v-if="!expModal.vm.running">
            <b-tooltip label="start" type="is-light">
              <b-button class="button is-success" icon-left="play" @click="startVm( expModal.vm.name )">
              </b-button>
            </b-tooltip>
          </template>
          <template v-else>
            <b-tooltip label="pause" type="is-light">
              <b-button class="button is-warning" icon-left="pause" @click="pauseVm( expModal.vm.name )">
              </b-button>
            </b-tooltip>
          </template>
        </div>
        <div v-if="features.includes('vm-mount') && roleAllowed('vms/mount', 'post', expModal.fullName) && !showModifyStateBar && expModal.vm.running">
          &nbsp;
          <b-tooltip :label="!expModal.vm.ccActive ? 'mount vm (requires active cc)' : 'mount vm'" type="is-light">
            <b-button class="button is-light" icon-left="hdd" @click="showMountDialog(expModal.vm.name)" :disabled="!expModal.vm.ccActive">
            </b-button>
          </b-tooltip>
        </div>
        <div v-if="roleAllowed('vms/forwards', 'create', expModal.fullName) && !showModifyStateBar && expModal.vm.running">
          &nbsp;
          <b-tooltip label="create port forward" type="is-light">
            <b-button class="button is-light" icon-left="arrow-right" @click="showPortForwardDialog(expModal.vm.name)" :disabled="!expModal.vm.ccActive">
            </b-button>
          </b-tooltip>
        </div>
        <div v-if="roleAllowed('vms/memorySnapshot', 'create', expModal.fullName) && !showModifyStateBar && expModal.vm.running">
          &nbsp;
          <b-tooltip label="create memory snapshot" type="is-light">
            <b-button class="button is-light" icon-left="database" @click="queueMemorySnapshotVMs(expModal.vm.name)">
            </b-button>
          </b-tooltip>
        </div>
        <div v-if="roleAllowed('vms/commit', 'create', expModal.fullName) && !showModifyStateBar && expModal.vm.running">
          &nbsp;
          <b-tooltip label="create backing image" type="is-light">
            <b-button class="button is-light" icon-left="save" @click="diskImage(expModal.vm.name)">
            </b-button>
          </b-tooltip>
        </div>
        <div v-if="roleAllowed('vms/snapshot', 'create', expModal.fullName) && !showModifyStateBar && expModal.vm.running">
          &nbsp;
          <b-tooltip label="create vm snapshot" type="is-light">
            <b-button class="button is-light" icon-left="camera" @click="captureSnapshot(expModal.vm.name)">
            </b-button>
          </b-tooltip>
        </div>
        <div v-if="roleAllowed('vms/cdrom', 'update', expModal.fullName) && roleAllowed('vms/cdrom', 'delete', expModal.fullName)
         && !showModifyStateBar && expModal.vm.running">
          &nbsp;
          <b-tooltip :label="getOpticalDiscLabel()" type="is-light">
            <b-button class="button is-light" icon-left="compact-disc" @click="showChangeDisc(expModal.vm)">
            </b-button>
          </b-tooltip>
        </div>
        <!-- STATE BAR -->
        <div v-if="!showModifyStateBar">
          &nbsp;
          <b-tooltip label="modify state" type="is-light">
            <b-button class="button is-light" icon-left="edit" @click="showModifyStateBar = true">
            </b-button>
          </b-tooltip>
        </div>
        <div v-if="showModifyStateBar">
          &nbsp;
          <b-tooltip v-if="roleAllowed('vms/redeploy', 'update', expModal.fullName)" label="redeploy" type="is-light">
            <b-button class="button is-success" icon-left="history" @click="redeploy(expModal.vm.name)">
            </b-button>
          </b-tooltip>
          &nbsp;
          <b-tooltip v-if="roleAllowed('vms/reset', 'update', expModal.fullName)" label="reset disk state" type="is-light">
            <b-button class="button is-success" icon-left="undo-alt" @click="resetVmState(expModal.vm.name)">
            </b-button>
          </b-tooltip>
      
          &nbsp;
          <b-tooltip v-if="roleAllowed('vms/restart', 'update', expModal.fullName)" label="restart" type="is-light">
            <b-button class="button is-success" icon-left="sync-alt" @click="restartVm(expModal.vm.name)">
            </b-button>
          </b-tooltip>
      
          &nbsp;
          <b-tooltip v-if="roleAllowed('vms/shutdown', 'update', expModal.fullName)" label="shutdown" type="is-light">
            <b-button class="button is-danger" icon-left="power-off" @click="shutdownVm(expModal.vm.name)">
            </b-button>
          </b-tooltip>
      
          &nbsp;
          <b-tooltip v-if="roleAllowed('vms', 'delete', expModal.fullName)" label="kill" type="is-light">
            <b-button class="button is-danger" icon-left="skull-crossbones" @click="killVm(expModal.vm.name)">
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
  <b-modal :active.sync="portForwardModal.active" :on-cancel="resetPortForwardModal" has-modal-card>
    <div class="modal-card" style="width:30em">
      <header class="modal-card-head">
        <p class="modal-card-title">Create New Port Forward</p>
      </header>
      <section class="modal-card-body">
        <b-field label="Source Port">
          <b-input type="text" v-model="portForwardModal.srcPort"></b-input>
        </b-field>
        <b-field label="Destination Host">
          <b-input type="text" v-model="portForwardModal.dstHost"></b-input>
        </b-field>
        <b-field label="Destination Port">
          <b-input type="text" v-model="portForwardModal.dstPort"></b-input>
        </b-field>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button is-success" @click="createPortForward()">Create</button>
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
           <div v-for="(vmI,index)  in redeployModal.vm" :key="index">
             <div align="left">               
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
                <b-tooltip :label="getDiskToolTip(vmI.disk)" type="is-dark">
                  <b-select :value="vmI.disk" @input="( value ) =>  vmI.disk = value">
                    <option
                      v-for="(  d, index ) in disks"
                      :key="index"
                      :value="d">
                        {{ getBaseName(d) }}
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
              <div  v-for="(vmI,index) in diskImageModal.vm" :key="index">
                 <div align="left">             
                  <font color="#202020">
                    <hr v-if="parseInt(index) > 0" style="color:#595959;background-color:#595959">
                        Create disk image of the {{ vmI.name }} VM with filename:                 
                        <br><br>
                        <b-field :type="vmI.nameErrType" :message="vmI.nameErrMsg" autofocus>
                          <b-input  type="text" v-model="vmI.filename" focus></b-input>
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
          <button class="button is-success" :disabled="!validate(diskImageModal)" @click="backingImage(diskImageModal.vm)">
            Create
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="memorySnapshotModal.active" has-modal-card :on-cancel="resetMemorySnapshotModal" ref="memorySnapshot">
      <div  class="modal-card" style="width:auto">
        <header class="modal-card-head">
          <p  class="modal-card-title">Create memory snapshot</p>
        </header>
        <section class="modal-card-body">         
          <div v-if="memorySnapshotModal.vm.length > 0">             
            <div  v-for="(vmI,index) in memorySnapshotModal.vm" :key="index">
              <div align="left">                
                  <font color="#202020">
                    <hr v-if="parseInt(index) > 0" style="color:#595959;background-color:#595959">
                        Create a memory snapshot for the {{ vmI.name }} VM with filename:                 
                        <br><br>
                        <b-field :type="vmI.nameErrType" :message="vmI.nameErrMsg" autofocus>
                          <b-input  type="text" v-model="vmI.filename" focus></b-input>
                        </b-field>
                  </font>                  
                </div>                
            </div>
          </div>          
        </section>        
        <footer class="modal-card-foot buttons is-right">
          <button class="button"  type="button" 
              @click="closeModal('memorySnapshot')">
              Cancel
         </button>  
          <button class="button is-success" :disabled="!validate(memorySnapshotModal)" @click="createMemorySnapshot(memorySnapshotModal.vm)">
            Create
          </button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="appsModal.active" :on-cancel="resetAppsModal" has-modal-card>
      <div class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p class="modal-card-title">phēnix Apps</p>
        </header>
        <section class="modal-card-body">
          <div v-if="appsModal.triggerable.length">
            <b-checkbox v-for="( a, index ) in appsModal.triggerable" :key="index" :native-value="a" v-model="appsModal.apps" type="is-light" style="color:#202020">
              {{ a }}
            </b-checkbox>
          </div>
          <div v-else>
            <span style="color:#202020">This experiment doesn't include any triggerable apps.</span>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <div v-if="roleAllowed('experiments/trigger', 'create', experiment.name)">
            <b-tooltip label="start selected apps" type="is-light is-left">
              <b-button v-if="appsModal.apps.length > 0" class="button is-success" @click="startApps(appsModal.apps)">Trigger Apps</b-button>
              <b-button v-else disabled class="button is-success">Trigger Apps</b-button>
            </b-tooltip>
          </div>
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
    <b-modal :active.sync="opticalDiscModal.active" has-modal-card :on-cancel="resetOpticalDiscModal" ref="opticalDisc">
      <div align="left" class="modal-card" style="width:auto">
        <header class="modal-card-head">
          <p  class="modal-card-title">Change Optical Disc for {{opticalDiscModal.vmName}} </p>
        </header>
        <section class="modal-card-body pt-3">   
              <font color="#202020">             
              <b-field label="Optical Disc:">
              <b-tooltip :label="getDiskToolTip(opticalDiscModal.disc, 'select ISO')" type="is-dark">
              <b-select :value="opticalDiscModal.disc" @input="( value ) =>  opticalDiscModal.disc = value">
                <option
                  v-for="(  d, index ) in disks"
                    :key="index"
                    :value="d">
                    {{ getBaseName(d) }}
                </option>
              </b-select>
              </b-tooltip>
              </b-field>
              </font>            
        </section>        
        <footer class="modal-card-foot buttons is-right">
          <button class="button"  type="button" 
              @click="closeModal('opticalDisc')">
              Cancel
          </button>  
          <button class="button is-success"  @click="changeOpticalDisc(opticalDiscModal.vmName,opticalDiscModal.disc)">
            {{ getOpticalDiscLabel() }}
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
      <div class="level-item" v-if="experiment.apps" @click="getApps()">
        <span style="font-weight: bold;">Apps:</span>&nbsp;
        <b-taglist>
          <b-tag v-for="( a, index ) in experiment.apps" :key="index" type="is-light">
            {{ a }}  
          </b-tag>
        </b-taglist>
      </div>
    </div>    
    <div class="level">    
      <div class="level-left"></div>
      <div class="level-right">
        <!-- Multi-VM options shown next to search -->
        <div class="level-item" style="margin-bottom: -.3em;">
        <b-field v-if="isMultiVmSelected" position="is-center">
          <div v-if="vmSelectedArray.every(vm => roleAllowed('vms/start', 'update', experiment.name + '/' + vm)) && !showModifyStateBar">
            <b-tooltip label="start" type="is-light">
              <b-button class="button is-success" icon-left="play" @click="processMultiVmAction(vmActions.start)">
              </b-button>
            </b-tooltip>
            &nbsp;
            <b-tooltip label="pause" type="is-light">
              <b-button class="button is-warning" icon-left="pause" @click="processMultiVmAction(vmActions.pause)">
              </b-button>
            </b-tooltip>
          </div>
          <div v-if="vmSelectedArray.every(vm => roleAllowed('vms/memorySnapshot', 'create', experiment.name + '/' + vm)) && !showModifyStateBar">
            &nbsp;
            <b-tooltip label="create memory snapshot" type="is-light">
              <b-button class="button is-light" icon-left="database"
                @click="processMultiVmAction(vmActions.createMemorySnapshot)">
              </b-button>
            </b-tooltip>
          </div>
          <div v-if="vmSelectedArray.every(vm => roleAllowed('vms/commit', 'create', experiment.name + '/' + vm)) && !showModifyStateBar">
            &nbsp;
            <b-tooltip label="create backing image" type="is-light">
              <b-button class="button is-light" icon-left="save" @click="processMultiVmAction(vmActions.createBacking)">
              </b-button>
            </b-tooltip>
          </div>
          <div v-if="vmSelectedArray.every(vm => roleAllowed('vms/snapshots', 'update', experiment.name + '/' + vm)) && !showModifyStateBar">
            &nbsp;
            <b-tooltip label="create vm snapshot" type="is-light">
              <b-button class="button is-light" icon-left="camera" @click="processMultiVmAction(vmActions.captureSnapshot)">
              </b-button>
            </b-tooltip>
          </div>          
          <div v-if="!showModifyStateBar">
            &nbsp;
            <b-tooltip label="modify state" type="is-light">
              <b-button class="button is-light" icon-left="edit" @click="showModifyStateBar = true">
              </b-button>
            </b-tooltip>
          </div>
          <div v-if="showModifyStateBar">
            &nbsp;
            <b-tooltip v-if="vmSelectedArray.every(vm => roleAllowed('vms/redeploy', 'update', experiment.name + '/' + vm))" label="redeploy" type="is-light">
              <b-button class="button is-success" icon-left="history" @click="processMultiVmAction(vmActions.redeploy)">
              </b-button>
            </b-tooltip>
            &nbsp;
            <b-tooltip v-if="vmSelectedArray.every(vm => roleAllowed('vms/reset', 'update', experiment.name + '/' + vm))" label="reset disk state" type="is-light">
              <b-button class="button is-success" icon-left="undo-alt" @click="processMultiVmAction(vmActions.resetState)">
              </b-button>
            </b-tooltip>
        
            &nbsp;
            <b-tooltip v-if="vmSelectedArray.every(vm => roleAllowed('vms/restart', 'update', experiment.name + '/' + vm))" label="restart" type="is-light">
              <b-button class="button is-success" icon-left="sync-alt" @click="processMultiVmAction(vmActions.restart)">
              </b-button>
            </b-tooltip>
        
            &nbsp;
            <b-tooltip v-if="vmSelectedArray.every(vm => roleAllowed('vms/shutdown', 'update', experiment.name + '/' + vm))"  label="shutdown" type="is-light">
              <b-button class="button is-danger" icon-left="power-off" @click="processMultiVmAction(vmActions.shutdown)">
              </b-button>
            </b-tooltip>
        
            &nbsp;
            <b-tooltip v-if="vmSelectedArray.every(vm => roleAllowed('vms', 'delete', experiment.name + '/' + vm))" label="kill" type="is-light">
              <b-button class="button is-danger" icon-left="skull-crossbones" @click="processMultiVmAction(vmActions.kill)">
              </b-button>
            </b-tooltip>
        
            &nbsp;
            <b-tooltip label="close toolbar" type="is-light">
              <b-button class="button is-light" icon-left="window-close" @click="showModifyStateBar = false">
              </b-button>
            </b-tooltip>
          </div>
        
        </b-field>
       </div>
        &nbsp;&nbsp;
       <div class="level-item"  style="margin-bottom: -1em;">
        <b-field v-if="roleAllowed('experiments/files', 'list', experiment.name)" position="is-right">
          <template v-if="this.activeTab == 1">
            <b-tooltip label="search on a specific category" type="is-light">
              <b-select :value="filesTable.category" @input="( value ) => assignCategory( value )" placeholder="All Categories">
                <option v-for="( category, index ) in filesTable.categories" :key="index" :value=category>{{ category }}</option>
              </b-select>
            </b-tooltip>
            &nbsp;
          </template>
          <b-autocomplete
            v-model="search.filter"
            :placeholder="searchPlaceholder"
            icon="search"
            :data="searchHistory"
            @typing="searchVMs"
            @select="option => searchVMs(option)">
            <template slot="empty">No results found</template>
          </b-autocomplete>
          <p  class='control'>
            <button class='button' style="color:#686868" @click="searchVMs(''); filesTable.category = null">
              <b-icon icon="window-close"></b-icon>
            </button>
          </p>
          &nbsp; &nbsp;
          <p  class="control">
            <b-button v-if="roleAllowed('experiments/stop', 'update', experiment.name)" class="button is-danger" slot="trigger" icon-right="stop" @click="stop"></b-button>
            &nbsp;
            <router-link v-if="roleAllowed('experiments', 'get', experiment.name)" class="button is-light" :to="{ name: 'soh', params: { id: this.$route.params.id }}">
              <b-icon icon="heartbeat"></b-icon>
            </router-link>
            &nbsp;
            <router-link v-if="roleAllowed('experiments', 'get', experiment.name)" class="button is-light" :to="{ name: 'scorch', params: { id: this.$route.params.id }}">
              <b-icon icon="fire"></b-icon>
            </router-link>
          </p>
        </b-field>
       </div>
      </div>
    </div>
    <div style="margin-top: -4em;">
      <b-tabs @input="tabsSwitched()" v-model="activeTab">
        <b-tab-item label="VMs" icon="desktop">
          <b-table
            :data="experiment.vms"
            :paginated="table.isPaginated"
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
                  Your search turned up empty!
                </div>
              </section>
            </template>
            <b-table-column field="multiselect" label="">              
              <template v-slot:header="{ column }">
                <b-tooltip label="Select/Unselect All" type="is-dark">
                <b-checkbox @input="selectAllVMs" v-model="checkAll" type="is-primary"/>
                </b-tooltip>
              </template>
              <template v-slot:default="props">
                <template v-if="!props.row.busy">
                  <div>
                    <b-checkbox :disabled="props.row.external" v-model="vmSelectedArray" :native-value=props.row.name type="is-primary"/>
                  </div>
                </template>
                <template v-else>
                  BUSY 
                </template>
              </template>
            </b-table-column>
            <b-table-column field="name" label="Node" width="150" sortable centered v-slot="props">
              <template v-if="!props.row.external && roleAllowed('vms', 'get', experiment.name + '/' + props.row.name)">
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
              <template v-else-if="props.row.external">
                <span class="tag is-medium">
                  <div class="field">
                    {{ props.row.name }}
                  </div>
                </span>
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
                <b-progress size="is-small" type="is-warning" show-value :value=props.row.percent format="percent"></b-progress>
              </section>
            </b-table-column>
            <b-table-column field="screenshot"  label="Screenshot" centered v-slot="props">
              <template v-if="props.row.external">
                <img src="@/assets/external.png" width="200" height="150">
              </template>
              <template v-else-if="props.row.running && !props.row.busy && !props.row.screenshot">
                <a  :href="vncLoc(props.row)" target="_blank">
                  <img src="@/assets/not-available.png" width="200" height="150">
                </a>
              </template>
              <template v-else-if="props.row.delayed_start && props.row.state == 'BUILDING'">
                <a  :href="vncLoc(props.row)" target="_blank">
                  <img src="@/assets/delayed.png" width="200" height="150">
                </a>
              </template>
              <template v-else-if="props.row.running && !props.row.busy && props.row.screenshot">
                <a  :href="vncLoc(props.row)" target="_blank">
                  <img :src="props.row.screenshot">
                </a>
              </template>
              <template v-else-if="props.row.busy">
                <b-tooltip  label="Screenshot not available while busy with action" type="is-dark">
                <img  src="@/assets/not-available.png" width="200" height="150">
                </b-tooltip>
              </template>
              <template v-else>
                <img  src="@/assets/not-running.png" width="200" height="150">
              </template>
            </b-table-column>
            <b-table-column v-if="isDelayed()" field="delayed"  label="Delay" centered v-slot="props">
              <b-tag type="is-info" v-if="props.row.delayed_start && props.row.state == 'BUILDING'">{{ props.row.delayed_start }}</b-tag>
            </b-table-column> 
            <b-table-column field="host" label="Host" width="150" sortable v-slot="props">
              <template v-if="props.row.external">
                EXTERNAL
              </template>
              <template v-else>
                {{ props.row.host }}
              </template>
            </b-table-column>   
            <b-table-column field="ipv4"  label="IPv4" width="150">
              <template v-slot:header= "{ column }"> 
                <div class="level">  
                  <div class="level-item"> 
                    {{ column.label }}             
                    &nbsp;
                    <b-tooltip label="Start Subnet Packet Capture" type="is-dark" :active="isSubnetPresent()">
                        <b-button :disabled="!isSubnetPresent() || displayedVMsCapturing()" class="button is-light is-small" @click="captureSubnet()" style="width: .1em;">
                        <b-icon icon="play-circle" ></b-icon>
                        </b-button>
                    </b-tooltip>
                    &nbsp;&nbsp;
                    <b-tooltip label="Stop Subnet Packet Capture" type="is-dark" :active="((isSubnetPresent() || capturesSearched()) && displayedVMsCapturing())">
                      <b-button  :disabled="!((isSubnetPresent() || capturesSearched()) && displayedVMsCapturing())" class="button is-light is-small" @click="stopCaptureSubnet()" style="width: .1em;">
                        <b-icon icon="stop-circle" ></b-icon>
                      </b-button>
                    </b-tooltip>
                  </div>
                </div>
              </template>
              <template v-slot:default="props">
                <template  v-if="roleAllowed('vms/captures', 'create', experiment.name + '/' + props.row.name) && props.row.running && !props.row.busy"> 
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
              </template>
            </b-table-column>
            <b-table-column field="network" label="Network" v-slot="props">
              <template v-if="roleAllowed('vms', 'patch', experiment.name + '/' + props.row.name) && props.row.running && !props.row.busy">                  
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
            <b-table-column field="taps"  label="Taps" v-slot="props">
              <template v-if="roleAllowed('vms/captures', 'create', experiment.name + '/' + props.row.name) && props.row.running && !props.row.busy">
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
            <b-table-column field="uptime"  label="Uptime" width="165" v-slot="props">
              <template v-if="props.row.external">
                unknown 
              </template>
              <template v-else>
                {{ props.row.uptime | uptime }}
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
            :paginated="filesTable.isPaginated"
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
    <b-loading :is-full-page="true" :active.sync="isWaiting" :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import { mapState }        from 'vuex';
  import VmMountBrowserModal from './VMMountBrowserModal.vue';

  import _ from 'lodash';

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

      ...mapState({
        features: 'features'
      })
    },

    methods: {
      changePaginate () {
        var user = localStorage.getItem( 'user' );
        localStorage.setItem( user + '.lastPaginate', this.table.isPaginated );
      },

      changeFilesPaginate () {
        var user = localStorage.getItem( 'user' );
        localStorage.setItem( user + '.lastPaginate', this.filesTable.isPaginated );
      },

      vncLoc (vm) {
        return this.$router.resolve({name: 'vnc', params: {id: this.$route.params.id, name: vm.name, token: this.$store.getters.token}}).href;
      },

      searchVMs: _.debounce(function ( term ) {
        if ( term == null ) {
          term = '';
        }
        this.search.filter = term;        
        if (this.activeTab == 0){
          this.updateExperiment();
          return
        }
        
        this.updateFiles()
      },250 ),

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
          this.handle(  msg );
        });
      },
    
      handle  ( msg ) {
        switch ( msg.resource.type ) {
          case 'experiment/' + this.$route.params.id: {
            switch ( msg.resource.action ) {
              case 'triggered': {
                this.$buefy.toast.open({
                  message: 'phēnix Apps ' + this.apps + ' have been triggered.',
                  type: 'is-success',
                  duration: 4000
                });

                this.apps = null;

                break;
              }

              case 'triggerError': {
                this.$buefy.toast.open({
                  message: 'Triggering phēnix Apps ' + this.apps + ' failed.',
                  type: 'is-danger',
                  duration: 4000
                });

                this.apps = null;

                break;
              }
            }
          }

          case  'experiment/vms': {
            if ( msg.resource.action != 'list' ) {
              return;
            }
            
            this.experiment.vms = [ ...msg.result.vms ];

            if ( this.search.filter ) {
              this.table.total  = msg.result.total;
              // Only add successful searches to the search history
              if (this.table.total > 0) {
                if (this.searchHistory > this.searchHistoryLength) {
                  this.searchHistory.pop()
                }
                this.searchHistory.push(this.search.filter.trim())
                this.searchHistory = this.getUniqueItems(this.searchHistory)
              }
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

              case  'shutdown': {
                break;
              }

              case 'cdrom-inserted': {
                this.$buefy.toast.open({
                  message: 'The optical disc for ' + vm[ 1 ] + ' was successfully inserted.',
                  type: 'is-success',
                  duration: 4000
                });

                // Refresh the VM
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[i].name == vm[ 1 ] ) {
                    this.getInfo(vms[i]);
                    break;
                  }
                }
                
                break;
              }

              case 'cdrom-ejected': {
                this.$buefy.toast.open({
                  message: 'The optical disc for ' + vm[ 1 ] + ' was successfully ejected.',
                  type: 'is-success',
                  duration: 4000
                });

                // Refresh the VM
                for ( let i = 0; i < vms.length; i++ ) {
                  if ( vms[i].name == vm[ 1 ] ) {
                    this.getInfo(vms[i]);
                    break;
                  }
                }
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
                   .then(null, err  => {
                     this.errorNotification(err);
                  })
                } else { 
                  this.redeployModal.active = false;  
                  this.resetRedeployModal();
                  this.isWaiting  = false;
                }
 
                break;
              }

              case 'error': {
                // Only show this error if the user is currently viewing the
                // running experiment in which the error occurred.
                if (this.$route.params.id == vm[0]) {
                  this.$buefy.toast.open({
                    message:  msg.result.error,
                    type: 'is-danger',
                    duration: 4000
                  });
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

          case  'experiment/vm/memorySnapshot': {
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
                        message: 'A memory snapshot was created with name ' + disk + ' for the ' + vm[ 1 ] + ' VM was successfully created.',
                        type: 'is-success',
                        duration: 4000
                      });
                      this.experiment.vms = [ ...vms ];
                      break;
                    }
                  }
                  break;
                }
              case  'committing': {

                for ( let i = 0; i < vms.length; i++ ) {
                  if  ( vms[i].name == vm[ 1 ] ) {
                    vms[i].busy = true;
                    vms[i].percent = 0;
                    let disk = msg.result.disk;
                
                    this.$buefy.toast.open({
                      message:  'A memory snapshot with name ' + disk + ' for the ' + vm[ 1 ] + ' VM is being created.',
                      type: 'is-warning',
                      duration: 4000
                    });
                
                    this.experiment.vms = [ ...vms ];
                    break;
                  }
                }
                break;
              }
              
              case  'progress': {                
                let percent = Math.round(msg.result.percent * 100 );
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

      isDelayed () {
        if (this.experiment.vms === undefined) {
          return false;
        }
        for ( let i = 0; i < this.experiment.vms.length; i++ ) {
          if ( this.experiment.vms[i].delayed_start && this.experiment.vms[i].state == 'BUILDING' ) {
            return true;
          }
        }

        return false;
      },

      async updateExperiment () {
        try {
          let resp  = await this.$http.get('experiments/' + this.$route.params.id);
          let state = await resp.json();

          this.experiment  = state;
          this.search.vms  = state.vms.map( vm => { return vm.name } );
          this.table.total = state.vm_count;          

          this.updateTable(); 
        } catch (err) {
          console.log(`ERROR getting experiments: ${err}`);
          this.errorNotification(err);
        } finally {
          this.isWaiting  = false;
        }
      },
    
      updateDisks (diskType="")  {
        this.disks = [];
      
        this.$http.get( `disks?diskType=${diskType}` ).then(
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
          },  err => {
            console.log('Getting the disks failed with ' + err.status);
            this.isWaiting = false;
            this.errorNotification(err);
          }
        );
      },

      tabsSwitched() {

        // Clear search history and 
        // search filter when switching tabs
        this.searchHistory = []
        this.search.filter = ""

        
        if (this.activeTab == 0){
          this.searchPlaceholder = "Find a VM"         
          this.updateExperiment()          
        }
        else {
          this.searchPlaceholder = "Find a File" 
          this.updateFiles()         
        }

      },


      updateFiles ()  {

        let params = '?filter=' + this.search.filter
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
                this.files = state.files === null ? [] : state.files
                this.filesTable.total = state.total

                for ( let i = 0; i < this.files.length; i++ ) {
                  this.filesTable.categories.push( ...this.files[i].categories );
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
                  this.searchHistory.push(this.search.filter.trim())
                  this.searchHistory = this.getUniqueItems(this.searchHistory)
              }

                this.isWaiting = false;
              }
            );
          },  err => { 
            this.errorNotification(err);
            this.isWaiting = false;
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

      assignCategory ( value ) {
        this.filesTable.category = value;
        this.updateFiles();
      },

      async fetchVMDetails(vm) {
        let url  = `experiments/${this.$route.params.id}/vms/${vm.name}`;
        let resp = await this.$http.get(url);

        return await resp.json();
      },

      async fetchVMSnapshots(vm) {
        let url  = `experiments/${this.$route.params.id}/vms/${vm.name}/snapshots`;
        let resp = await this.$http.get(url);

        return await resp.json();
      },

      async fetchVMForwards(vm) {
        let url  = `experiments/${this.$route.params.id}/vms/${vm.name}/forwards`;
        let resp = await this.$http.get(url);

        return await resp.json();
      },

      async getInfo (vm) {
        if(vm.busy){
          this.$buefy.dialog.alert({
            title: 'VM Busy',
            message: ['VM',vm.name,'is currently busy and not available for another action'].join(' '),
            confirmText: 'Ok'
          })       

          return
        }

        try {
          const [details, snapshots, forwards] = await Promise.all([
            this.fetchVMDetails(vm),
            this.fetchVMSnapshots(vm),
            this.fetchVMForwards(vm),
          ])

          this.expModal.vm = details;

          if (snapshots.snapshots && snapshots.snapshots.length > 0) {
            this.expModal.snapshots = snapshots.snapshots;
          }

          this.expModal.forwards = [];

          if (forwards.listeners) {
            for (let i = 0; i < forwards.listeners.length; i++) {
              let l = forwards.listeners[i];

              l.desc = `${l.srcPort} --> ${l.dstHost}:${l.dstPort}`;
              l.canDelete = this.$store.getters.username === l.owner;

              this.expModal.forwards.push(l);
            }
          }

          this.expModal.fullName = this.experiment.name + '/' + vm.name;
          this.expModal.active   = true;
        } catch (err) {
          this.errorNotification(err);
        }
      },

      snapshots ( vm ) {
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
          },  err => {
            this.errorNotification(err);
            this.isWaiting = false;
          }
        );
      },

      captureSnapshot ( name ) {
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
                }, err => {
                  this.errorNotification(err);
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
              },  err => {
                this.errorNotification(err);
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
          title: 'Create Disk Images',
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
                   console.log('backing image for vm ' + name + ' returned status ' + response.status);
                }, err => {
                  this.errorNotification(err);
                }
              );
            })
          }
        })
        
        this.diskImageModal.active = true;
      },

      queueMemorySnapshotVMs (name)  {
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
               
           var filename = date + "_" + time;
       
              filename = vms[i].name +"_"+ filename.substring(filename.lastIndexOf( '/')+1 ); 
       
              this.memorySnapshotModal.vm.push({
                dateTime:date+time+"" ,
                name:vms[i].name ,
                filename:filename ,
                nameErrType:"" ,
                nameErrMsg:""
              });
      }
      }
        })
        this.expModal.active = false;
        this.memorySnapshotModal.active = true;
      },

      createMemorySnapshot (vm) {
        
        this.memorySnapshotModal.active = false;        
        let url = "";
        let name = "";
        let body = "";
        vm.forEach((arg,) => {
          url = 'experiments/' + this.$route.params.id + '/vms/' + arg.name + '/memorySnapshot';
          body = { "filename": arg.filename  + '.elf' };
          name = arg.name;
          
          this.$http.post(url,body,{ timeout: 0 }).then(
            err => {
              this.errorNotification(err);
          });
          
          this.resetMemorySnapshotModal();
        })
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
                  },  err => {
                    this.errorNotification(err);
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
              },  err => {
                this.errorNotification(err);
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
                        }, err => {
                          this.errorNotification(err);
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
                        }, err => {
                          this.errorNotification(err);
                          this.isWaiting = false;
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
                  },  err => {
                    this.errorNotification(err);
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
                  },  err => {
                    this.errorNotification(err);
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
                    }, err => {
                      this.errorNotification(err);                      
                      this.isWaiting = false;
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
                    }, err => {
                      this.errorNotification(err);                      
                      this.isWaiting = false;
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
                    }, err => {
                      this.errorNotification(err);
                      this.isWaiting = false;
                    } 
                  );
                })        
            }
          })
        }
      },
      
      redeploy ( vm ) {
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
      
      closeModal (modalName) {      
        this.$refs[modalName].cancel('x');
      },
      
      redeployVm (vms) {
        let body = "";
        let postUrl  = "";
        let name = "";

        vms.forEach((vm, _) => {
          body = { "cpus": parseInt(vm.cpus), "ram": parseInt(vm.ram), "disk": vm.disk };
          postUrl  = 'experiments/' + this.$route.params.id + '/vms/' + vm.name + '/redeploy';

          if ( vm.inject ) {
            body["injects"] = true;
          }

          this.redeployModal.actionsQueue.push({name: vm.name, url: postUrl, body: body});
        })

        this.$http.post(postUrl, body).then(
          null, err => {
            this.errorNotification(err);
            this.isWaiting = false;
            this.resetExpModal();
          }
        )
      },

      changeVlan ( index, vlan, from, name ) {
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
                }, err => {
                  this.errorNotification(err);       
                  this.isWaiting = false;
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
                }, err => {
                  this.errorNotification(err);
                  this.isWaiting = false;
                }
              )
            }
          })
        }

        this.vlanModal.active = false;
      },

      captureSubnet () {
        let subnets = this.extractAllSubnets(this.search.filter)
        if (subnets == null){
          return
        }
        if (subnets.length > 1) {
          this.$buefy.dialog.alert({
            title: 'Multiple Subnets Detected',
            message: 'Subnet packet captures can be started for only one subnet',
            type:'is-dark',
            confirmText: 'Ok'
            }) 
          return
        }

        let vms = this.vmSelectedArray
        //Determine the list of VMs to apply the capture request to
        if (vms.length == 0){
          vms = []
          let visibleItems = this.$refs["vmTable"].visibleData
          
          for(let i = 0; i<visibleItems.length;i++){
            vms.push(visibleItems[i].name)
          }
        }

        if (vms.length == 0){
          return
        }

        this.$buefy.dialog.confirm({
          title:"Start Subnet Packet Captures",
          message:"This will start all packet captures for " + vms.join(", "),
          cancelText:"Cancel",
          confirmText:"Ok",
          type:"is-success",
          onConfirm: () => {
            let url = 'experiments/' + this.$route.params.id + '/captureSubnet';
            let body = { "subnet": subnets[0], "vms":vms };
            
            this.$http.post(url,body).then(
              response  => {
                let vmMap = {}
                for(let i = 0;i<response.body.captures.length;i++)
                {
                    if(vmMap[response.body.captures[i].vm] === undefined){
                      vmMap[response.body.captures[i].vm] = []
                      vmMap[response.body.captures[i].vm].push(response.body.captures[i])
                    }
                    else { 
                        vmMap[response.body.captures[i].vm].push(response.body.captures[i])
                    }
                }
              
              let vms = this.experiment.vms;
              for ( let i = 0; i < vms.length; i++ ) {
                if  ( vmMap[vms[i].name] !== undefined ) {
                  vms[i].captures = vmMap[vms[i].name]
                  
                }
              } 
              this.experiment.vms = [ ...vms ];
              this.isWaiting = false;
                  
            
              },  err => {
                this.errorNotification(err);
                this.isWaiting = false;
              } 
            );
          }
        })
      },

      stopCaptureSubnet () {
        let subnets = this.extractAllSubnets(this.search.filter)
        if (subnets == null){
          if (!this.capturesSearched()){
            return
          }
          subnets = [];
          
        }
        if (subnets.length > 1) {
          this.$buefy.dialog.alert({
            title: 'Multiple Subnets Detected',
            message: 'Subnet packet captures can only be stopped for only one subnet',
            type:'is-dark',
            confirmText: 'Ok'
            }) 
          return
        }

        let vms = this.vmSelectedArray
        //Determine the list of VMs to apply the capture request to
        if (vms.length == 0){
          vms = []
          let visibleItems = this.$refs["vmTable"].visibleData
          
          for(let i = 0; i<visibleItems.length;i++){
            vms.push(visibleItems[i].name)
          }
        }

        if (vms.length == 0){
          return
        }

        this.$buefy.dialog.confirm({
          title:"Stop Subnet Packet Captures",
          message:"This will stop all packet captures for " + vms.join(", "),
          cancelText:"Cancel",
          confirmText:"Ok",
          type:"is-danger",
          onConfirm: () => {
        
            let url = 'experiments/' + this.$route.params.id + '/stopCaptureSubnet';
            let body = {"subnet":subnets.length > 0 ? subnets[0] : "", "vms":vms };                  
            this.$http.post(url,body).then(
              response  => {
                
                let vmMap = {}
                for(let i = 0;i<response.body.vms.length;i++)
                {
                    if(vmMap[response.body.vms[i]] === undefined){
                      vmMap[response.body.vms[i]] = true
                      
                    } 
                }
              
              let vms = this.experiment.vms;
              for ( let i = 0; i < vms.length; i++ ) {
                if  ( vmMap[vms[i].name] !== undefined ) {
                  vms[i].captures = []
                  
                }
              } 
              this.experiment.vms = [ ...vms ];
              this.isWaiting = false;
                  
            
              },  err => {
                this.errorNotification(err);
                this.isWaiting = false;
              } 
            );
          }
        })
      },

      changeOpticalDisc (vmName,isoPath) {
        this.opticalDiscModal.active = false;  

        let url = `experiments/${this.$route.params.id}/vms/${vmName}/cdrom`

        if (this.getOpticalDiscLabel().indexOf('eject') != -1) {
          this.$http.delete(url).then(
            null, err => {
              this.errorNotification(err);
            }
          );
        } else {
          url += `?isoPath=${isoPath}`

          this.$http.post(url).then(
            null, err => {
              this.errorNotification(err);
            }
          );
        }

        this.resetOpticalDiscModal;        
      },
      
      resetExpModal ()  {        
        this.expModal = {
          active: false,
          fullName: '',
          vm: [],
          snapshots: false,
          forwards: []
        }
        this.showModifyStateBar = false;
      },
    
      resetRedeployModal () {       
        this.redeployModal = {
          active: false,
          vm: [],
          actionsQueue: []
        }
        
      },
    
      resetDiskImageModal () {        
        this.diskImageModal = {
          active: false,
          vm: []
        }
      },

      resetMemorySnapshotModal () {        
        this.memorySnapshotModal = {
          active: false,
          vm: []
        }
      },

      getApps () {
        let defaultApps = ['ntp', 'serial', 'startup', 'vrouter', 'scorch'];

        this.appsModal.triggerable = this.experiment.apps.filter(a => !defaultApps.includes(a));
        this.appsModal.active = true;
      },

      startApps ( apps ) {
        apps = apps.join();
        this.apps = apps;

        this.$http.post( 'experiments/' + this.$route.params.id + '/trigger' + '?apps=' + apps )
        .then( response => {
          console.log('triggering ' + apps + ': ' + response);
        }, err => {
          this.errorNotification(err);
          this.isWaiting = false;
        });

        this.resetAppsModal();
      },

      resetAppsModal () {
        this.appsModal.apps = [];
        this.appsModal.active = false;
      },

      resetFileViewerModal () {
        this.fileViewerModal.active = false;
        this.fileViewerModal.title = null;
        this.fileViewerModal.contents = null;
      },

      resetOpticalDiscModal () {
        this.opticalDiscModal.active = false;
        this.opticalDiscModal.disc = ""; 
        this.opticalDiscModal.vmName = null;       
      },

      showChangeDisc(vm) {
        this.updateDisks("ISO")
        this.opticalDiscModal.vmName = vm.name;
        this.opticalDiscModal.active = true;        
        this.opticalDiscModal.disc = vm.cdRom;                
      },

      validate (modalVMQueue) {  
        var regexp = /^[a-zA-Z0-9-_]+$/;
        for ( let i = 0; i < modalVMQueue.vm.length; i++ ) {
            if ( !regexp.test( modalVMQueue.vm[i].filename ) ) {
              modalVMQueue.vm[i].nameErrType = 'is-danger';
              modalVMQueue.vm[i].nameErrMsg  = 'image names can only contain alphanumeric, dash, and underscore; we will add the file extension';             
              return  false;
            }

            modalVMQueue.vm[i].nameErrType = '';
            modalVMQueue.vm[i].nameErrMsg  = '';
        }

        return true;
      },

      processMultiVmAction (action) { 
        switch(action) {
          case this.vmActions.start:
            this.startVm(this.vmSelectedArray);
            break;
          case this.vmActions.pause:
            this.pauseVm(this.vmSelectedArray);
            break;
          case this.vmActions.kill:
            this.killVm(this.vmSelectedArray);
            break;
          case this.vmActions.redeploy:
            this.redeploy(this.vmSelectedArray);
            break;
          case this.vmActions.createBacking:
            this.diskImage(this.vmSelectedArray);
            break;
          case this.vmActions.createMemorySnapshot:
            this.queueMemorySnapshotVMs(this.vmSelectedArray);
            break;
          case this.vmActions.captureSnapshot:
            this.captureSnapshot(this.vmSelectedArray);
            break;
          case this.vmActions.restart:
            this.restartVm(this.vmSelectedArray);
            break;
          case this.vmActions.shutdown:
            this.shutdownVm(this.vmSelectedArray);
            break;
          case this.vmActions.resetState:
            this.resetVmState(this.vmSelectedArray);
            break;
          default:
            this.notImplemented();
        }
        
        this.unSelectAllVMs();
      },
  
      selectAllVMs () {            
        var visibleItems = this.$refs["vmTable"].visibleData

        if (visibleItems.length == 0) {
          // if there are no visible items, there is nothing to select
          return;
        } else if (this.vmSelectedArray.length == visibleItems.length) {
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
        this.vmSelectedArray = [];
        
        for (var i = 0; i < visibleItems.length; i++){
          let item = visibleItems[i];

          if (!item.external) {
            this.vmSelectedArray.push(item.name)
          }
        }
      },
        
      unSelectAllVMs () {
        this.checkAll           = false;
        this.vmSelectedArray    = [];
        this.showModifyStateBar = false;
      },
      
      updateCaptureLabel (vm) {       
        return vm.captures.length == 0 ? "start packet capture" : "stop packet capture"        
      },
      
      notImplemented () {
       this.$buefy.dialog.alert({
          title: 'Not Implemented',
          message: 'This function has not yet been implemented',
          type:'is-dark',
          confirmText: 'Ok'
        }) 
      },

      getUniqueItems (inputArray) {
        let arrayHash = {};
        
        for( let i = 0; i < inputArray.length; i++ ) {
          // Skip really short items
          if (inputArray[i].length < 4) {
            continue
          }

          if( arrayHash[inputArray[i]] === undefined ) {
            arrayHash[inputArray[i]] = true;
          }
        }
        
        return Object.keys(arrayHash).sort();
      },

      getBaseName(diskName) { 
        return diskName.substring(diskName.lastIndexOf("/")+1);       
      },

      getOpticalDiscLabel () {    

        if (this.expModal.vm.cdRom === undefined) {
          return "insert optical disc"
        }

        // If there is an existing disc in the VM, see 
        // if the disc was changed
        if (this.expModal.vm.cdRom.length > 0 && this.opticalDiscModal.disc.length > 0) {

          return String(this.expModal.vm.cdRom) != String(this.opticalDiscModal.disc) ? "change optical disc" : "eject optical disc"            
        }
        else if (this.expModal.vm.cdRom.length > 0) {
          return "eject optical disc"
        }
        else {
          return "insert optical disc"
        }   
              
      },

      getDiskToolTip(fullPath,defaultMessage="menu for assigning vm(s) disk") {       
        return this.disks.indexOf(fullPath) == -1 ? defaultMessage : fullPath
      },

      isSubnetPresent () { 
        return /(?:\d{1,3}[.]){3}\d{1,3}[/]\d{1,2}/.test(this.search.filter)
      },

      extractAllSubnets (searchTerm) {
        return searchTerm.match(/(?:\d{1,3}[.]){3}\d{1,3}[/]\d{1,2}/g)
      },

      capturesSearched () {
        let tmp = this.search.filter.toLowerCase()      
        return tmp.indexOf("capturing") != -1 && tmp.indexOf("not capturing") == -1
      },

      displayedVMsCapturing () {
        // Determine if any displayed VMs are currently capturing
        let visibleItems = this.$refs["vmTable"].visibleData
            
        for( let i = 0; i<visibleItems.length;i++ ) {
          if ( visibleItems[i].captures.length > 0 ) {           
            return true;
          }
        }
        
        return false  
      },

      fileDownloadURL(name, path) {
        return this.$router.resolve({name: 'file', params: {id: this.$route.params.id, name: name, path: path, token: this.$store.getters.token}}).href;
      },

      showMountDialog(vm) {
        this.resetExpModal()

        this.$buefy.modal.open({
          parent:       this,
          component:    VmMountBrowserModal,
          trapFocus:    true,
          hasModalCard: true,
          canCancel:    [],
          props:        {"targetVm": vm, "targetExp": this.$route.params.id}
        })
      },

      showPortForwardDialog(vm) {
        this.portForwardModal.vmName = vm;
        this.portForwardModal.active = true;
      },

      resetPortForwardModal() {
        this.portForwardModal = {
          active:  false,
          vmName:  null,
          srcPort: null,
          dstHost: '127.0.0.1',
          dstPort: null
        }
      },

      async createPortForward() {
        let url    = `experiments/${this.$route.params.id}/vms/${this.portForwardModal.vmName}/forwards`;
        let params = `?src=${this.portForwardModal.srcPort}&host=${this.portForwardModal.dstHost}&dst=${this.portForwardModal.dstPort}`

        try {
          await this.$http.post(url + params);

          let resp = await this.$http.get(url);
          let json = await resp.json();

          this.expModal.forwards = [];

          if (json.listeners) {
            for (let i = 0; i < json.listeners.length; i++) {
              let l = json.listeners[i];

              l.desc = `${l.srcPort} --> ${l.dstHost}:${l.dstPort}`;
              l.canDelete = this.$store.getters.username === l.owner;

              this.expModal.forwards.push(l);
            }
          }
        } catch (err) {
          this.errorNotification(err);
        } finally {
          this.resetPortForwardModal();
        }
      },

      async deletePortForward(vm, forward) {
        let url    = `experiments/${this.$route.params.id}/vms/${vm}/forwards`;
        let params = `?host=${forward.dstHost}&dst=${forward.dstPort}`

        try {
          await this.$http.delete(url + params);

          let resp = await this.$http.get(url);
          let json = await resp.json();

          this.expModal.forwards = [];

          if (json.listeners) {
            for (let i = 0; i < json.listeners.length; i++) {
              let l = json.listeners[i];

              l.desc = `${l.srcPort} --> ${l.dstHost}:${l.dstPort}`;
              l.canDelete = this.$store.getters.username === l.owner;

              this.expModal.forwards.push(l);
            }
          }
        } catch (err) {
          this.errorNotification(err);
        }
      }
    },
    
    data () {
      return  {
        search: {
          vms:  [],
          filter: ''
        },
        table: {
          isPaginated:  false,
          isPaginationSimple: true,
          currentPage:  1,
          perPage:  10,
          total:  0,
          sortColumn: 'name',
          sortOrder:  'asc',
          paginationSize: 'is-small'
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
          vm: [],
          fullName: '',
          snapshots: false,
          forwards: []
        },
        portForwardModal: {
          active:  false,
          vmName:  null,
          srcPort: null,
          dstHost: '127.0.0.1',
          dstPort: null
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
        memorySnapshotModal: {
          active: false,
          vm:[],
          /*  vm is structured as so:
           name:  null, filename: null, dateTime: null, 
           nameErrType: null, nameErrMsg: null
          */
        },
        appsModal: {
          active: false,
          triggerable: [],
          apps: []
        },
        fileViewerModal: {
          active: false,
          title: null,
          contents: null
        },
        opticalDiscModal: {
          active: false,
          disc: "",
          vmName: null          
        },
        apps: null,
        experiment: [],
        files: [],
        disks: [],
        vlan: null,
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
          recordScreenshots:10,
        },
        searchHistory: [],
        searchHistoryLength:10,
        searchPlaceholder:"Find a VM",
        activeTab:0        
      }
    }
  }
</script>

<style scoped>
div.autocomplete >>> a.dropdown-item {
  color:  #383838 !important;
}
</style>
