<template>
    <div class="modal-card" style="width: 70vw">
      <header class="modal-card-head">
        <p class="modal-card-title mr-6">{{this.vmName}} {{ this.isLabels ? "Labels" : "Tags" }}</p>
      </header>
      <section class="modal-card-body">
      
        <b-table :data="workingTags" class="fixed-table">
          <b-table-column field="key" label="Key" v-slot="props" width="192px">
              <template v-if="canEdit()">
                <b-input v-model="props.row.key"/>
              </template>
              <template v-else>
                {{ props.row.key }}
              </template>
          </b-table-column>
          <b-table-column field="value" label="Value" v-slot="props">
            <template v-if="canEdit()">
                <b-input v-model="props.row.value"/>
              </template>
              <template v-else>
                {{ props.row.value }}
              </template>
          </b-table-column>
          <b-table-column v-slot="props" v-if="canEdit()" width="32px">
            <div @click="deleteTag( props.row )" class="is-clickable">
              <font-awesome-icon icon="trash" />
            </div>
          </b-table-column>
          <template #footer v-if="canEdit()">
            <b-button icon-right="plus" type="is-text" size="is-small" expanded @click="addTag()"/>
          </template>
        </b-table>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <b-button label="Close" @click="$emit('close')" />
        <b-button v-if="canEdit()" label="Save" type="is-primary" @click="saveTags()"/>
      </footer>
    </div>
  </template>
  
  <script>
  
  export default {
    props: {
      vmName: String,
      experiment: String,
      tags: Object,
      isLabels: {
        type: Boolean,
        default: false
      }
    },
  
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
      // start with blank space
      if (this.workingTags.length == 0) {
        this.addTag()
      }
    },
  
  
    methods: {
      canEdit() {
        return this.roleAllowed('vms', 'patch', this.experiment + "/" + this.vmName)
      },
      deleteTag(row) {
        this.workingTags = this.workingTags.filter(e => e !== row)
      },
      addTag() {
        this.workingTags.push({"key": "", "value": ""})
      },
      saveTags() {
        var finalTags = {}
        for (const row of this.workingTags) {
          if (row.key == "" || row.key in finalTags)
            continue
          finalTags[row.key] = row.value
        }

        let update = { "tags": finalTags };

        if (_.isEqual(finalTags, this.tags)) {
          console.log("No change made. Closing")
          this.$emit('close')
          return;
        }

        this.$http.patch('experiments/' + this.$route.params.id + '/vms/' + this.vmName, update)
            .then(response => {
              if (response.ok) {
                this.$emit('close')
              }
            }, err => {
              this.errorNotification(err)
            });
      }
    }
  }
  </script>
    
  <style lang="scss">
    .fixed-table {
      height: 75vh;
      overflow: auto;
    }

    .b-table {
      .table {
        td {
          vertical-align: middle;
        }
      }
    }
  
  </style>