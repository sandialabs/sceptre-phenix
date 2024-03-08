<template>
    <form action="">
    <div class="modal-card">
      <header class="modal-card-head">
        <p class="modal-card-title mr-6">{{this.targetVm}} Mount</p>
        <button type="button" class="delete light" @click="$emit('close')"></button>
      </header>
      <section class="modal-card-body">
        <b-breadcrumb>
              <b-breadcrumb-item v-for="p in pathParts" :key="p.upTo" @click.native="currentPath = p.upTo" :active="p == pathParts.at(-1)">
                {{p.part}}
              </b-breadcrumb-item>
        </b-breadcrumb>
        <b-table :data="files" :loading="filesLoading" class="fixed-table">
          <b-table-column field="name" label="File" v-slot="props">
              <a v-if="props.row.isDir" @click="currentPath = props.row.path">{{ `${props.row.name}/` }}</a>
              <a v-else @click="downloadFile(props.row.path)">{{props.row.name}}</a>
          </b-table-column>
          <b-table-column field="size" label="Size" width="128px" v-slot="props">
            <div v-if="props.row.isDir">&nbsp;</div>
            <div v-else>{{ props.row.size | fileSize }}</div>
          </b-table-column>
          <b-table-column field="date" label="Date" width="212px" v-slot="props">
              {{ props.row.date }}
          </b-table-column>
          <template #empty>
            <div class="has-text-centered">No Files in Directory</div>
          </template>
        </b-table>
      </section>
      <footer class="modal-card-foot">
        <b-field v-show="roleAllowed('vms/mount', 'patch', targetExp + '/' + targetVm)" class="file is-info" style="margin-bottom: 0;">
          <b-upload @input="handleUpload" class="file-label" :class="{disabled: currentUploadProgress !== null}" 
                    :disabled="currentUploadProgress !== null">
              <span class="file-cta">
                  <b-icon class="file-icon" icon="upload"></b-icon>
                  <span class="file-label" :disabled="currentUploadProgress !== null">Upload File</span>
              </span>
          </b-upload>
        </b-field>
        <b-progress class="progress mx-3" v-show="currentUploadProgress !== null" :value="currentUploadProgress" :max="100" show-value>
          {{this.currentUploadFileName}} : {{parseFloat(this.currentUploadProgress).toFixed(2)}}%
        </b-progress>
      </footer>
    </div>
  </form>
  </template>
  
  <script>
  
  export default {
    props: [
      'vm',
    ],
  
    data() {
      return {
        tags: {}
      }
    },
  
    beforeDestroy() {
    },
  
    beforeMount() {
      this.$http.post(`experiments/${this.targetExp}/vms/${this.targetVm}/mount`).then(_ => {
       this.getFiles();
        
      }, err => {
        this.errorDialog('Error mounting vm ' + this.targetVm + ": " + err.body);
        this.$parent.close();
      });
      window.addEventListener('beforeunload', this.unmount);
    },
  
  
    methods: {
      getFiles() {
        this.filesLoading = true;
        this.$http.get(`experiments/${this.targetExp}/vms/${this.targetVm}/files`, { 
            params: { 'path': this.currentPath } 
          }).then(success => {
            this.files = success.body.files === null ? [] : success.body.files
            this.filesLoading = false;
          }, err => {
            this.filesLoading = false;
            this.errorDialog(`Error getting files: ${err.body}`)
          });
      },
    }
  }
  </script>
    
  <style lang="scss">
    .fixed-table {
      height: 75vh;
      overflow: auto;
    }
    .disabled > .file-cta {
      background-color: gray !important;
    }
  
  </style>