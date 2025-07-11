<template>
  <b-modal
    v-model="isUploaderModalActive"
    @close="resetUploader"
    has-modal-card>
    <div class="modal-card" style="width: auto">
      <header class="modal-card-head x-modal-dark">
        <p class="modal-card-title">Upload a Config</p>
      </header>
      <section class="modal-card-body x-modal-dark">
        <b-field>
          <b-upload
            v-model="uploaderFile"
            drag-drop
            @update:modelValue="uploadFile">
            <section class="section">
              <div class="content has-text-centered">
                <p>
                  <b-icon icon="upload" size="is-large"></b-icon>
                </p>
                <p>Drop your config here or click to upload</p>
                <p>(Valid file types are .yaml, .yml, and .json)</p>
              </div>
            </section>
          </b-upload>
        </b-field>
      </section>
    </div>
  </b-modal>
  <b-modal v-model="viewer.isActive" @close="resetViewer" has-modal-card>
    <div class="modal-card" style="width: 50em">
      <header class="modal-card-head x-modal-dark">
        <p class="modal-card-title x-config-text">{{ viewer.title }}</p>
      </header>
      <section class="modal-card-body x-modal-dark">
        <div class="control">
          <textarea
            class="textarea x-config-text has-fixed-size"
            rows="30"
            v-model="viewer.obj"
            readonly />
        </div>
      </section>
      <footer class="modal-card-foot x-modal-dark buttons is-right">
        <button
          v-if="roleAllowed('configs', 'update', viewer.config.metadata.name)"
          class="button is-success"
          @click="$emit('edit', viewer.config)">
          Edit Config
        </button>
        <!-- <button class="button is-info" @click="action( 'dl', { 'kind': viewer.kind, 'metadata': { 'name': viewer.name } } )"> -->
        <button class="button is-info" @click="download([viewer.config])">
          <b-icon icon="download"></b-icon>
        </button>
        <button class="button is-dark" @click="resetViewer">Exit</button>
      </footer>
    </div>
  </b-modal>
  <!--header-->
  <div class="level">
    <div class="level-left" />
    <div class="level-right">
      <div class="level-item">
        <b-field position="is-center">
          <div v-if="selectedConfigs.length > 0">
            <b-tooltip
              v-if="
                selectedConfigs.every((c) =>
                  roleAllowed('configs', 'get', c.metadata.name),
                )
              "
              label="download selected configs"
              type="is-light is-top">
              <button
                class="button is-light action"
                @click="download(selectedConfigs)">
                <b-icon icon="download"></b-icon>
              </button>
            </b-tooltip>

            <b-tooltip
              v-if="
                selectedConfigs.every((c) =>
                  roleAllowed('configs', 'delete', c.metadata.name),
                )
              "
              label="delete selected configs"
              type="is-light is-top">
              <button
                class="button is-light action"
                @click="deleteConfigs(selectedConfigs)">
                <b-icon icon="trash"></b-icon>
              </button>
            </b-tooltip>
          </div>
        </b-field>

        <b-field position="is-right" grouped>
          <b-field>
            <b-select placeholder="Filter on Kind" v-model="filterKind">
              <option v-for="(k, index) in filterOptions" :key="index" :value="k">
                {{ k }}
              </option>
            </b-select>
          </b-field>
          <b-field>
            <b-autocomplete
              v-model="searchQuery"
              placeholder="Find a Config"
              icon="search"
              :data="filteredConfigs"
              @select="(option) => (filtered = option)">
              <template #empty> No results found </template>
            </b-autocomplete>
            <p class="control">
              <b-tooltip
                label="resets search filter and filter on kind"
                type="is-light"
                multilined>
                <button
                  class="button input-button"
                  @click="
                  searchName = '';
                  filterKind = null;
                  ">
                  <b-icon icon="window-close"></b-icon>
                </button>
              </b-tooltip>
            </p>
          </b-field>
          <b-field v-if="roleAllowed('configs', 'create')">
            <b-tooltip label="create a new config" type="is-light is-top">
              <button
                class="button is-light"
                id="main"
                @click="$emit('create')">
                <b-icon icon="plus"></b-icon>
              </button>
            </b-tooltip>
          </b-field>
          <b-field v-if="roleAllowed('configs', 'create')">
            <b-tooltip label="upload a new config" type="is-light is-top">
              <button
                class="button is-light"
                id="main"
                @click="isUploaderModalActive = true">
                <b-icon icon="upload"></b-icon>
              </button>
            </b-tooltip>
          </b-field>
        </b-field>
      </div>
    </div>
  </div>
  <!--table-->
  <div style="margin-top: -1em">
    <b-table
      :data="filteredConfigs"
      :paginated="isPaginated"
      per-page="10"
      pagination-simple="true"
      pagination-size="is-small"
      default-sort="kind"
      checkable
      v-model:checked-rows="selectedConfigs"
      :loading="isWaiting"
      ref="cfgTable">
      <!-- docs currently wrong with checked rows, see: https://github.com/ntohq/buefy-next/issues/278 -->
      <!-- <b-loading :is-full-page="false" v-model="isWaiting"></b-loading> -->

      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            Your search turned up empty!
          </div>
        </section>
      </template>

      <b-table-column
        field="kind"
        label="Kind"
        width="200"
        sortable
        v-slot="props">
        {{ props.row.kind }}
      </b-table-column>

      <b-table-column
        field="name"
        label="Name"
        width="400"
        sortable
        v-slot="props">
        <template v-if="roleAllowed('configs', 'get', props.row.metadata.name)">
          <b-tooltip label="view config" type="is-dark">
            <div class="field">
              <div @click="viewConfig(props.row)">
                {{ props.row.metadata.name }}
              </div>
            </div>
          </b-tooltip>
          &nbsp;
          <b-tag type="is-info" v-if="isBuilderTopology(props.row)"
            >builder</b-tag
          >
        </template>
        <template v-else>
          {{ props.row.metadata.name }}
          &nbsp;
          <b-tag type="is-info" v-if="isBuilderTopology(props.row)"
            >builder</b-tag
          >
        </template>
      </b-table-column>

      <b-table-column field="updated" label="Last Updated" v-slot="props">
        {{ props.row.metadata.updated }}
      </b-table-column>

      <b-table-column label="Actions" centered v-slot="props">
        <button
          v-if="roleAllowed('configs', 'update', props.row.metadata.name)"
          class="button is-light is-small action"
          @click="$emit('edit', props.row)">
          <b-icon icon="edit"></b-icon>
        </button>
        <button
          v-if="roleAllowed('configs', 'get', props.row.metadata.name)"
          class="button is-light is-small action"
          @click="download([props.row])">
          <b-icon icon="download"></b-icon>
        </button>
        <button
          v-if="roleAllowed('configs', 'delete', props.row.metadata.name)"
          class="button is-light is-small action"
          @click="deleteConfigs([props.row])">
          <b-icon icon="trash"></b-icon>
        </button>
      </b-table-column>
    </b-table>
    <br />
    <b-field v-if="paginationNeeded" grouped position="is-right">
      <div class="control is-flex">
        <b-switch
          v-model="isPaginated"
          size="is-small"
          type="is-light"
          @input="changePaginate()"
          >Paginate</b-switch
        >
      </div>
    </b-field>
  </div>
</template>

<script setup>
  import { roleAllowed } from '@/utils/rbac.js';
  import { useErrorNotification } from '@/utils/errorNotif';
</script>
<script>
  import axiosInstance from '@/utils/axios.js';
  import YAML from 'js-yaml';

  import FileSaver from 'file-saver';

  export default {
    emits: ['edit', 'create'],
    data() {
      return {
        configs: [],
        isWaiting: false,

        //filters
        filterKind: null,
        searchQuery: '',
        filterOptions: [
          'Topology',
          'Scenario',
          'Experiment',
          'Image',
          'User',
          'Role',
        ],
        //table
        isPaginated: false,
        perPage: 10,
        currentPage: 1,
        selectedConfigs: [],

        //uploader modal
        isUploaderModalActive: false,
        uploaderFile: null,

        viewer: {
          isActive: false,
          config: { kind: null, metadata: { name: null } },
          title: null,
          obj: null,
        },
      };
    },
    created() {
      this.updateConfigs();
    },
    watch: {
      selectedConfigs(newSelectedConfigs) {
        console.log('selecting from configslisttwo');
      },
    },
    computed: {
      paginationNeeded() {
        return this.filteredConfigs > 10;
      },
      filteredConfigs: function () {
        let configs = this.configs;

        if (this.filterKind) {
          let filteredConfigs = [];

          for (let i = 0; i < configs.length; i++) {
            if (configs[i].kind == this.filterKind) {
              filteredConfigs.push(configs[i]);
            }
          }

          configs = filteredConfigs;
        }

        var name_re = new RegExp(this.searchQuery, 'i');
        var data = [];

        for (let i in configs) {
          let cfg = configs[i];
          if (cfg.metadata.name.match(name_re)) {
            data.push(cfg);
          }
        }
        return data;
      },
    },
    methods: {
      updateConfigs() {
        this.isWaiting = true;
        axiosInstance
          .get('configs')
          .then((response) => {
            const state = response.data;
            this.configs = state.configs === null ? [] : state.configs;
            this.isWaiting = false;
          })
          .catch((err) => {
            this.isWaiting = false;
          });
      },
      isBuilderTopology(cfg) {
        if (cfg.kind == 'Topology') {
          if ('annotations' in cfg.metadata) {
            return 'builder-xml' in cfg.metadata.annotations;
          }
        }

        return false;
      },
      download(configList) {
        const configs = configList.map(
          (conf) => `${conf.kind}/${conf.metadata.name}`,
        );
        axiosInstance
          .post('configs/download', JSON.stringify(configs), {
            headers: {
              'Content-Type': 'application/json',
              Accept: 'application/x-yaml',
            },
            responseType: 'blob',
          })
          .then((response) => {
            if (configs.length == 1) {
              let body = new Blob([response.data], { type: 'text/plain' });
              const fileName = configs[0].replace('/', '-') + '.yml';
              FileSaver.saveAs(body, fileName);
            } else {
              FileSaver.saveAs(response.data, 'configs.zip');
            }
          })
          .catch((err) => {
            console.log(err);
            useErrorNotification(err);
          });
      },
      deleteConfigs(configList) {
        const configs = configList.map(
          (conf) => `${conf.kind}/${conf.metadata.name}`,
        );
        let msg = '';
        if (configs.length > 1) {
          msg =
            'This will delete ' +
            configs.length +
            ' configs. Are you sure you want to do this?';
        } else {
          msg =
            'This will delete the ' +
            configs[0] +
            ' config. Are you sure you want to do this?';
        }
        this.$buefy.dialog.confirm({
          title: 'Delete the Config',
          message: msg,
          cancelText: 'Cancel',
          confirmText: 'Delete',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => {
            for (var i = 0; i < configs.length; i++) {
              this.isWaiting = true;
              axiosInstance
                .delete('configs/' + configs[i])
                .then(() => {
                  //delete from config list
                  let configsSet = new Set(configs);
                  this.configs = this.configs.filter((item) => {
                    const key = `${item.kind}/${item.metadata.name}`;
                    return !configsSet.has(key);
                  });

                  let confirmMsg = '';
                  if (configs.length > 1) {
                    confirmMsg = 'The configs have been deleted.';
                  } else {
                    confirmMsg =
                      'The ' + configs[0] + ' config has been deleted.';
                  }

                  this.isWaiting = false;

                  this.$buefy.toast.open({
                    message: confirmMsg,
                    type: 'is-success',
                    duration: 4000,
                  });
                })
                .catch((err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                });
            }
          },
        });
      },

      uploadFile(file) {
        let ext = /\.yaml|\.yml|\.json$/i;

        if (!ext.exec(file.name)) {
          this.$buefy.toast.open({
            message: 'Valid file types are .yaml, .yml, and .json',
            type: 'is-danger',
            duration: 4000,
          });
          return;
        }

        let formData = new FormData();
        formData.append('fileupload', file);

        axiosInstance
          .post('configs', formData)
          .then(() => {
            this.$buefy.toast.open({
              message: 'The file ' + file.name + ' was uploaded',
              type: 'is-success',
              duration: 4000,
            });
            this.updateConfigs();
          })
          .catch((err) => {
            if (err.body.metadata && err.body.metadata.validation) {
              this.error.title = 'Validation Error';
              this.error.msg = err.body.metadata.validation;
              this.error.modal = true;
            } else {
              useErrorNotification(err);
            }
          });
        this.resetUploader();
        this.isWaiting = false;
      },
      resetUploader() {
        this.isUploaderModalActive = false;
        this.uploaderFile = null;
      },
      resetViewer() {
        this.viewer.isActive = false;
        ((this.viewer.config = { kind: null, metadata: { name: null } }),
          (this.viewer.title = null));
        this.viewer.obj = null;
      },
      viewConfig(cfg) {
        this.viewer.config = cfg;
        this.viewer.title = cfg.kind + '/' + cfg.metadata.name;

        this.isWaiting = true;

        axiosInstance
          .get('configs/' + this.viewer.title, {
            headers: { Accept: 'application/json' },
          })
          .then((response) => {
            let obj = response.data;

            if ('annotations' in obj.metadata) {
              if ('builder-xml' in obj.metadata.annotations) {
                this.config.builderXML =
                  obj.metadata.annotations['builder-xml'];
                obj.metadata.annotations['builder-xml'] = '<SNIPPED>';
              }
            }

            this.viewer.obj = YAML.dump(obj);
            this.viewer.isActive = true;
          })
          .catch((err) => {
            this.errorNotification(err);
          })
          .finally(() => {
            this.isWaiting = false;
          });
      },
    },
  };
</script>
<style scoped>
  .x-modal-dark :deep(textarea) {
    background-color: #686868;
    color: whitesmoke;
  }
  textarea {
    color: whitesmoke;
  }
  .action:not(:last-child) {
    margin-right: 5px;
  }
</style>
