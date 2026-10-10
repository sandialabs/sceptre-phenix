<template>
  <b-modal v-model="error.modal" @close="resetErrorModal" has-modal-card>
    <div class="modal-card" style="width: 50em">
      <header class="modal-card-head x-modal-dark">
        <p class="modal-card-title">{{ error.title }}</p>
      </header>
      <section class="modal-card-body x-modal-dark">
        <div class="control">
          <textarea
            class="textarea x-config-text has-fixed-size"
            style="font-family: monospace"
            rows="30"
            aria-label="Validation errors"
            v-model="error.msg"
            readonly />
        </div>
      </section>
      <footer class="modal-card-foot x-modal-dark buttons is-right">
        <button class="button is-dark" @click="resetErrorModal">Exit</button>
      </footer>
    </div>
  </b-modal>
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
  <b-modal
    v-model="viewer.isActive"
    @close="resetViewer"
    has-modal-card
    aria-role="dialog"
    aria-modal
    :aria-label="viewer.title"
    close-button-aria-label="Close"
    :destroy-on-hide="false">
    <!-- The page keeps the viewer after it closes. Before this, a viewer
         that opened during the removal of the previous one stayed open but
         invisible over the page, and Exit did not close it. -->
    <div class="modal-card" style="width: 50em">
      <header class="modal-card-head x-modal-dark">
        <p class="modal-card-title x-config-text">{{ viewer.title }}</p>
      </header>
      <section class="modal-card-body x-modal-dark">
        <div class="control">
          <textarea
            class="textarea x-config-text has-fixed-size"
            rows="30"
            aria-label="Config, as YAML"
            v-model="viewer.obj"
            readonly />
        </div>
      </section>
      <footer class="modal-card-foot x-modal-dark buttons is-right">
        <!-- A topology opens in the Builder: its diagram when it has one,
             else the Import dialog, which makes one. The button has the
             colors of the list's Builder tag, whose text meets the 4.5:1
             contrast that white on the info color does not. -->
        <button
          v-if="viewer.builder"
          class="button is-info is-light"
          data-testid="viewer-builder"
          @click="openInBuilder(viewer.config)">
          {{
            viewer.builder === 'open'
              ? 'Open in Builder'
              : 'Import into Builder'
          }}
        </button>
        <button
          v-if="roleAllowed('configs', 'update', configFullName(viewer.config))"
          class="button is-success"
          @click="$emit('edit', viewer.config)">
          Edit Config
        </button>
        <!-- <button class="button is-info" @click="action( 'dl', { 'kind': viewer.kind, 'metadata': { 'name': viewer.name } } )"> -->
        <button
          class="button is-info"
          aria-label="Download config"
          @click="download([viewer.config])">
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
        <b-field position="is-right" grouped>
          <b-field
            v-if="
              selectedConfigs.length > 0 &&
              selectedConfigs.every((c) =>
                roleAllowed('configs', 'get', configFullName(c)),
              )
            ">
            <b-tooltip label="download selected configs" type="is-light is-top">
              <button
                class="button is-light action"
                aria-label="Download selected configs"
                @click="download(selectedConfigs)">
                <b-icon icon="download"></b-icon>
              </button>
            </b-tooltip>
          </b-field>
          <b-field
            v-if="
              selectedConfigs.length > 0 &&
              selectedConfigs.every((c) =>
                roleAllowed('configs', 'delete', configFullName(c)),
              )
            ">
            <b-tooltip label="delete selected configs" type="is-light is-top">
              <button
                class="button is-light action"
                aria-label="Delete selected configs"
                @click="deleteConfigs(selectedConfigs)">
                <b-icon icon="trash"></b-icon>
              </button>
            </b-tooltip>
          </b-field>
          <b-field>
            <b-select
              placeholder="Filter on Kind"
              aria-label="Filter configs by kind"
              v-model="filterKind">
              <option
                v-for="(k, index) in filterOptions"
                :key="index"
                :value="k">
                {{ k }}
              </option>
            </b-select>
            <b-autocomplete
              v-model="searchQuery"
              placeholder="Find a Config"
              icon="search"
              :data="filteredConfigs.map((c) => c.metadata.name)"
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
                  aria-label="Reset the search and the kind filter"
                  @click="
                    searchQuery = '';
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
                aria-label="Create a new config"
                @click="$emit('create')">
                <b-icon icon="plus"></b-icon>
              </button>
            </b-tooltip>
          </b-field>
          <b-field v-if="roleAllowed('configs', 'create')">
            <b-tooltip label="upload a new config" type="is-light is-top">
              <button
                class="button is-light"
                aria-label="Upload a new config"
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
      aria-next-label="Next page"
      aria-previous-label="Previous page"
      aria-page-label="Page"
      aria-current-label="Current page"
      per-page="10"
      pagination-simple="true"
      pagination-size="is-small"
      default-sort="kind"
      :loading="isWaiting"
      ref="cfgTable">
      <!-- docs currently wrong with checked rows, see: https://github.com/buefy/buefy/issues/4102 -->
      <!-- <b-loading :is-full-page="false" v-model="isWaiting"></b-loading> -->

      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            Your search turned up empty!
          </div>
        </section>
      </template>

      <b-table-column label="Select" centered>
        <template #header>
          <b-checkbox
            :model-value="
              filteredConfigs.length > 0 &&
              filteredConfigs.every((config) =>
                selectedConfigs.includes(config),
              )
            "
            @update:model-value="toggleAllConfigs">
            <span class="is-sr-only">Select all configs</span>
          </b-checkbox>
        </template>
        <template #default="props">
          <b-checkbox
            :model-value="selectedConfigs.includes(props.row)"
            @update:model-value="toggleConfig(props.row, $event)">
            <span class="is-sr-only">
              Select config {{ props.row.metadata.name }}
            </span>
          </b-checkbox>
        </template>
      </b-table-column>

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
        <template
          v-if="roleAllowed('configs', 'get', configFullName(props.row))">
          <!-- A button, so the keyboard reaches the read-only view too. -->
          <b-tooltip label="view config" type="is-dark">
            <button
              type="button"
              class="config-name"
              :aria-label="`View ${props.row.kind} ${props.row.metadata.name}`"
              :data-config-view="configFullName(props.row)"
              @click="viewConfig(props.row)">
              {{ props.row.metadata.name }}
            </button>
          </b-tooltip>
          &nbsp;
          <!-- The tag is a link into the Builder for a role that may follow
               it. Its name starts with the text it shows, then says what
               following it does. -->
          <b-tooltip
            v-if="tagControl(props.row)"
            :label="
              tagControl(props.row) === 'open'
                ? 'open in Builder'
                : 'import into Builder'
            "
            type="is-dark">
            <router-link
              class="tag is-info is-light config-builder"
              :to="builderLink(props.row)"
              :aria-label="tagControlName(props.row)"
              :data-config-builder="configFullName(props.row)">
              {{ builderTag(props.row) }}
            </router-link>
          </b-tooltip>
          <b-tag type="is-info is-light" v-else-if="builderTag(props.row)">{{
            builderTag(props.row)
          }}</b-tag>
        </template>
        <template v-else>
          {{ props.row.metadata.name }}
          &nbsp;
          <b-tag type="is-info is-light" v-if="builderTag(props.row)">{{
            builderTag(props.row)
          }}</b-tag>
        </template>
      </b-table-column>

      <b-table-column field="updated" label="Last Updated" v-slot="props">
        {{ props.row.metadata.updated }}
      </b-table-column>

      <b-table-column label="Actions" centered v-slot="props">
        <b-tooltip
          class="action"
          :delay="500"
          label="edit config file"
          type="is-light"
          multilined>
          <button
            v-if="roleAllowed('configs', 'update', configFullName(props.row))"
            class="button is-light is-small action"
            :aria-label="`Edit ${props.row.kind} ${props.row.metadata.name}`"
            :data-config-edit="configFullName(props.row)"
            @click="$emit('edit', props.row)">
            <b-icon icon="edit"></b-icon>
          </button>
        </b-tooltip>
        <b-tooltip
          class="action"
          :delay="500"
          label="download config"
          type="is-light"
          multilined>
          <button
            v-if="roleAllowed('configs', 'get', configFullName(props.row))"
            class="button is-light is-small action"
            :aria-label="`Download ${props.row.kind} ${props.row.metadata.name}`"
            @click="download([props.row])">
            <b-icon icon="download"></b-icon>
          </button>
        </b-tooltip>
        <b-tooltip
          class="action"
          :delay="500"
          label="delete config"
          type="is-light"
          multilined>
          <button
            v-if="roleAllowed('configs', 'delete', configFullName(props.row))"
            class="button is-light is-small action"
            :aria-label="`Delete ${props.row.kind} ${props.row.metadata.name}`"
            @click="deleteConfigs([props.row])">
            <b-icon icon="trash"></b-icon>
          </button>
        </b-tooltip>
      </b-table-column>
    </b-table>
    <br />
    <b-field v-if="paginationNeeded" grouped position="is-right">
      <div class="control is-flex">
        <b-switch v-model="isPaginated" size="is-small" type="is-light"
          >Paginate</b-switch
        >
      </div>
    </b-field>
  </div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import YAML from 'js-yaml';

  import FileSaver from 'file-saver';
  import { roleAllowed } from '@/utils/rbac.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import {
    builderAction,
    builderLink,
    builderTagLabel,
  } from '@/builder/configs.js';

  export default {
    emits: ['edit', 'create'],
    props: {
      // The config ("Kind/name") whose edit button takes focus once the
      // list shows it: the one the editor that closed was opened for, so
      // focus does not fall to the page.
      focusConfig: { type: String, default: '' },
    },
    setup() {
      return { roleAllowed };
    },
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

        //validation error modal
        error: {
          modal: false,
          title: null,
          msg: null,
        },

        viewer: {
          isActive: false,
          config: { kind: null, metadata: { name: null } },
          title: null,
          obj: null,
          // What the Builder does with the config shown (see
          // builderControl), or '' for no button.
          builder: '',
        },
      };
    },
    created() {
      this.updateConfigs();
    },
    computed: {
      paginationNeeded() {
        return this.filteredConfigs.length > 10;
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
      toggleConfig(config, selected) {
        if (selected && !this.selectedConfigs.includes(config)) {
          this.selectedConfigs.push(config);
        } else if (!selected) {
          this.selectedConfigs = this.selectedConfigs.filter(
            (selectedConfig) => selectedConfig !== config,
          );
        }
      },
      toggleAllConfigs(selected) {
        if (selected) {
          this.selectedConfigs = [
            ...new Set([...this.selectedConfigs, ...this.filteredConfigs]),
          ];
        } else {
          this.selectedConfigs = this.selectedConfigs.filter(
            (config) => !this.filteredConfigs.includes(config),
          );
        }
      },
      updateConfigs() {
        this.isWaiting = true;
        axiosInstance
          .get('configs')
          .then(async (response) => {
            const state = response.data;
            this.configs = state.configs === null ? [] : state.configs;
            this.isWaiting = false;

            // The list has several root elements, so the search starts at
            // the document.
            if (this.focusConfig) {
              await this.$nextTick();
              document
                .querySelector(
                  `[data-config-edit="${CSS.escape(this.focusConfig)}"]`,
                )
                ?.focus();
            }
          })
          .catch(() => {
            this.isWaiting = false;
          });
      },
      // Matches store.Config.FullName, the name the server authorizes.
      configFullName(cfg) {
        return `${cfg.kind}/${cfg.metadata.name}`;
      },
      // distinguishes Builder documents (builder-doc) from legacy
      // builder diagrams (builder-xml)
      builderTag(cfg) {
        return builderTagLabel(cfg);
      },
      builderLink,
      // What the Builder control of a topology does, for a role that may
      // use it: 'open' its diagram, or 'import' it to make one, which
      // creates a draft. '' for no control. The server decides again.
      builderControl(cfg) {
        const action = builderAction(cfg);

        if (
          !action ||
          !roleAllowed('configs', 'list') ||
          !roleAllowed('configs', 'get', this.configFullName(cfg)) ||
          (action === 'import' && !roleAllowed('configs', 'create'))
        ) {
          return '';
        }

        return action;
      },
      // The control of a topology's tag. A topology without a tag has
      // none in its row: the viewer's button imports it.
      tagControl(cfg) {
        return builderTagLabel(cfg) ? this.builderControl(cfg) : '';
      },
      // The name of the tag's link: the tag's own text, then what the link
      // does, as "builder: open Topology site in the Builder".
      tagControlName(cfg) {
        const name = cfg.metadata.name;
        const does =
          this.tagControl(cfg) === 'open'
            ? `open Topology ${name} in the Builder`
            : `import Topology ${name} into the Builder`;

        return `${builderTagLabel(cfg)}: ${does}`;
      },
      // The viewer closes first, so the page is not left locked behind it.
      openInBuilder(cfg) {
        const link = builderLink(cfg);

        this.viewer.isActive = false;
        this.$router.push(link);
      },
      download(configList) {
        const configs = configList.map(this.configFullName);
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
              let body = new Blob([response.data], {
                type: 'text/plain',
              });
              const fileName = configs[0].replace('/', '-') + '.yml';
              FileSaver.saveAs(body, fileName);
            } else {
              FileSaver.saveAs(response.data, 'configs.zip');
            }
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
      deleteConfigs(configList) {
        const configs = configList.map(this.configFullName);
        let msg;
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
                  this.configs = this.configs.filter(
                    (item) => !configsSet.has(this.configFullName(item)),
                  );

                  let confirmMsg;
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
            if (err.response?.data?.metadata?.validation) {
              this.error.title = 'Validation Error';
              this.error.msg = err.response.data.metadata.validation;
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
      resetErrorModal() {
        this.error.modal = false;
        this.error.title = null;
        this.error.msg = null;
      },
      resetViewer() {
        // Focus goes back to the name the viewer was opened from.
        const opener = this.viewer.title;
        if (opener) {
          this.$nextTick(() =>
            document
              .querySelector(`[data-config-view="${CSS.escape(opener)}"]`)
              ?.focus(),
          );
        }

        this.viewer.isActive = false;
        ((this.viewer.config = {
          kind: null,
          metadata: { name: null },
        }),
          (this.viewer.title = null));
        this.viewer.obj = null;
        this.viewer.builder = '';
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

            // The Builder button follows the config as it is now, not the
            // row, which may be older.
            this.viewer.builder = this.builderControl(obj);

            // The viewer only shows the config, so it keeps no copy of the
            // legacy Builder diagram it leaves out: Edit and Download read
            // the config again.
            if ('annotations' in obj.metadata) {
              if ('builder-xml' in obj.metadata.annotations) {
                obj.metadata.annotations['builder-xml'] = '<SNIPPED>';
              }
            }

            this.viewer.obj = YAML.dump(obj);
            this.viewer.isActive = true;
          })
          .catch((err) => {
            useErrorNotification(err);
          })
          .finally(() => {
            this.isWaiting = false;
          });
      },
    },
  };
</script>
<style scoped>
  /* The name button looks like the text it replaces, and shows its focus. */
  .config-name {
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: start;
    cursor: pointer;
  }

  .config-name:focus-visible {
    outline: 2px solid currentColor;
    outline-offset: 2px;
  }

  /* The Builder tag as a link: it shows its focus, and that it is one. The
     ring is the color of the page's text: the tag's own dark blue would be
     lost on the row behind it. */
  .config-builder:focus-visible {
    outline: 2px solid whitesmoke;
    outline-offset: 2px;
  }

  .config-builder:hover {
    text-decoration: underline;
  }

  .x-modal-dark :deep(textarea) {
    background-color: #686868;
    color: whitesmoke;
  }
  textarea {
    color: whitesmoke;
  }
</style>
