<template>
    <form action="">
    <div class="modal-card">
      <header class="modal-card-head">
        <p class="modal-card-title mr-6">{{this.vmName}} Tags</p>
      </header>
      <section class="modal-card-body">
      
        <b-table :data="workingTags" class="fixed-table">
          <b-table-column field="key" label="Key" v-slot="props">
              {{ props.row.key }}
          </b-table-column>
          <b-table-column field="value" label="Value" v-slot="props">
            {{ props.row.value }}
          </b-table-column>
        </b-table>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <b-button label="Close" @click="$emit('close')" />
        <b-button v-if="canEdit()" label="Save" type="is-primary" />
      </footer>
    </div>
  </form>
  </template>
  
  <script>
  
  export default {
    props: [
      'vmName',
      'experiment',
      'tags',
    ],
  
    data() {
      return {
        workingTags: []
      }
    },
  
    beforeDestroy() {
    },
  
    beforeMount() {
      // copy tags object into an array of key,values for ui use
      for (const [key, value] of Object.entries(this.tags)) {
        this.workingTags.push({"key": key, "value": value})
      }
      console.log(this.workingTags)
    },
  
  
    methods: {
      canEdit() {
        return this.roleAllowed('vms', 'patch', this.experiment + "/" + this.vmName)
      }
    }
  }
  </script>
    
  <style lang="scss">
    .fixed-table {
      height: 75vh;
      overflow: auto;
    }
  
  </style>