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
    <!-- Kept after it closes: a viewer opened while the last one was being
         removed stayed invisible over the page, and Exit did not close it. -->
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
            :value="viewer.obj ?? 'Loading…'"
            readonly />
        </div>
      </section>
      <footer class="modal-card-foot x-modal-dark buttons is-right">
        <button
          v-if="viewer.config && configAllowed('update', viewer.config)"
          class="button is-success"
          @click="$emit('edit', viewer.config)">
          Edit Config
        </button>
        <button
          class="button is-info"
          aria-label="Download config"
          :class="{ 'is-loading': isDownloading(viewer.config) }"
          @click="download([viewer.config])">
          <b-icon icon="download"></b-icon>
        </button>
        <button class="button is-dark" @click="resetViewer">Exit</button>
      </footer>
    </div>
  </b-modal>
  <!--header-->
  <div class="level configs-toolbar">
    <div class="level-left">
      <stat-strip label="Config totals" :stats="totalsShown" />
    </div>
    <div class="level-right">
      <div class="level-item">
        <b-field position="is-right" grouped>
          <div
            v-if="paginationNeeded"
            class="control is-flex is-align-items-center">
            <b-switch
              v-model="table.isPaginated"
              size="is-small"
              type="is-light"
              >Paginate</b-switch
            >
          </div>
          <b-field
            v-if="
              selectedConfigs.length > 0 &&
              selectedConfigs.every((c) => configAllowed('get', c))
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
              selectedConfigs.every((c) => configAllowed('delete', c))
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
              <option :value="null">All kinds</option>
              <option
                v-for="(k, index) in filterOptions"
                :key="index"
                :value="k">
                {{ k }}
              </option>
            </b-select>
          </b-field>
          <b-field :addons="false" class="has-addons">
            <b-autocomplete
              v-model="searchQuery"
              placeholder="Find a Config"
              icon="search"
              :data="filteredConfigs.map((c) => c.metadata.name)">
              <template #empty> No results found </template>
            </b-autocomplete>
            <p v-if="searchQuery || filterKind" class="control">
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
                @mouseenter="loadEditor"
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
      :paginated="table.isPaginated && paginationNeeded"
      aria-next-label="Next page"
      aria-previous-label="Previous page"
      aria-page-label="Page"
      aria-current-label="Current page"
      :per-page="table.perPage"
      :pagination-simple="table.isPaginationSimple"
      :pagination-size="table.paginationSize"
      default-sort="kind"
      :loading="isWaiting">
      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            {{ emptyText }}
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
        header-class="sort-inline"
        sortable
        v-slot="props">
        {{ props.row.kind }}
      </b-table-column>

      <b-table-column
        field="metadata.name"
        label="Name"
        width="400"
        header-class="sort-inline"
        sortable
        v-slot="props">
        <template v-if="configAllowed('get', props.row)">
          <!-- A button, so the keyboard reaches the read-only view too. -->
          <b-tooltip label="view config" type="is-dark">
            <button
              type="button"
              class="config-name"
              :aria-label="`View ${props.row.kind} ${props.row.metadata.name}`"
              :data-config-view="configKey(props.row)"
              @mouseenter="prepareOpen(props.row)"
              @click="viewConfig(props.row)">
              {{ props.row.metadata.name }}
            </button>
          </b-tooltip>
          &nbsp;
        </template>
        <template v-else>
          {{ props.row.metadata.name }}
          &nbsp;
        </template>
      </b-table-column>

      <b-table-column
        field="metadata.updated"
        label="Last Updated"
        header-class="sort-inline"
        sortable
        :custom-sort="sortByUpdated"
        v-slot="props">
        <time-with-age :time="props.row.metadata.updated" />
      </b-table-column>

      <b-table-column label="Actions" centered v-slot="props">
        <b-tooltip
          class="action"
          :delay="500"
          label="edit config file"
          type="is-light"
          multilined>
          <button
            v-if="configAllowed('update', props.row)"
            class="button is-light is-small action"
            :aria-label="`Edit ${props.row.kind} ${props.row.metadata.name}`"
            :data-config-edit="configKey(props.row)"
            @mouseenter="prepareOpen(props.row)"
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
            v-if="configAllowed('get', props.row)"
            class="button is-light is-small action"
            :aria-label="`Download ${props.row.kind} ${props.row.metadata.name}`"
            :class="{ 'is-loading': isDownloading(props.row) }"
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
            v-if="configAllowed('delete', props.row)"
            class="button is-light is-small action"
            :aria-label="`Delete ${props.row.kind} ${props.row.metadata.name}`"
            @click="deleteConfigs([props.row])">
            <b-icon icon="trash"></b-icon>
          </button>
        </b-tooltip>
      </b-table-column>
    </b-table>
  </div>
</template>

<script>
  import * as configsApi from '@/api/configs.js';
  import YAML from 'js-yaml';

  import FileSaver from 'file-saver';
  import { roleAllowed } from '@/utils/rbac.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import {
    configKey,
    findConfig,
    forgetConfig,
    fullConfig,
    prefetchConfig,
  } from '@/utils/configCache.js';
  import { loadAce } from '@/utils/loadAce.js';
  import StatStrip from '@/components/StatStrip.vue';
  import { CONFIG_KINDS, configTotals } from '@/utils/pageTotals.js';
  import {
    createPageLoader,
    loadingText,
    stillLoading,
  } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { useTable } from '@/utils/useTable.js';
  import TimeWithAge from '@/components/TimeWithAge.vue';

  export default {
    components: { StatStrip, TimeWithAge },
    emits: ['edit', 'create'],
    props: {
      // The config ("Kind/name") whose edit button takes focus on the first
      // load, so focus returns there when the editor closes.
      focusConfig: { type: String, default: '' },
    },
    setup() {
      // the server authorizes configs by kind/name (store.Config.FullName)
      const configAllowed = (verb, cfg) =>
        roleAllowed('configs', verb, configKey(cfg));
      return {
        ...useTable({ name: 'configs' }),
        roleAllowed,
        configAllowed,
        configKey,
      };
    },

    data() {
      return {
        configs: [],
        loaded: false, // false until the first list arrives
        listSettled: false, // whether this visit's own first load has ended
        isWaiting: false, // set while a change is being saved

        //filters
        filterKind: null,
        searchQuery: '',
        filterOptions: CONFIG_KINDS,
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
          config: null,
          title: null,
          obj: null,
        },

        downloading: new Set(), // configs being downloaded, by key
      };
    },
    created() {
      this.loader = createPageLoader({
        key: 'configs',
        fetch: pageFetchers.configs,
        apply: (configs) => {
          const first = !this.loaded;
          this.configs = configs;
          this.loaded = true;
          // a cached list arrives before the rows are drawn; mounted() waits
          if (first && this.$el) this.focusReturnedConfig();
          this.viewFromRoute();
        },
      });
      // a link to a config missing from a cached list waits for this load
      Promise.resolve(this.loader.start()).then(() => {
        if (this.unmounted) return;
        this.listSettled = true;
        this.viewFromRoute();
      });
    },
    watch: {
      '$route.query.view'() {
        this.viewFromRoute();
      },
    },
    mounted() {
      if (this.loaded) this.focusReturnedConfig();
    },
    beforeUnmount() {
      this.unmounted = true;
      this.loader.stop();
    },
    computed: {
      // every config, then each kind's, whatever the search and kind filter
      totalsShown() {
        if (stillLoading(this.loaded, this.configs)) {
          return [{ key: 'configs', label: 'Configs', value: '…' }];
        }
        const totals = configTotals(this.configs);
        return [
          { key: 'configs', label: 'Configs', value: totals.configs },
          ...totals.kinds.map(({ kind, count }) => ({
            key: `kind-${String(kind).toLowerCase()}`,
            label: kind,
            value: count,
          })),
        ];
      },
      emptyText() {
        if (!this.loaded) return loadingText('configs');
        if (this.configs.length === 0) return 'No configs found';
        return 'No configs match your search';
      },
      paginationNeeded() {
        return this.filteredConfigs.length > this.table.perPage;
      },
      filteredConfigs() {
        // a plain substring match: the search box is not a regular expression
        const search = this.searchQuery.toLowerCase();
        return this.configs.filter(
          (cfg) =>
            (!this.filterKind || cfg.kind == this.filterKind) &&
            cfg.metadata.name.toLowerCase().includes(search),
        );
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

      // Focuses the edit button of the config the editor closed on, on the
      // first load only, so a later refresh (an upload) leaves focus alone.
      async focusReturnedConfig() {
        if (!this.focusConfig) return;
        await this.$nextTick();
        // The list has several root elements, so search the document.
        document
          .querySelector(`[data-config-edit="${CSS.escape(this.focusConfig)}"]`)
          ?.focus();
      },

      // Starts loading what opening a config needs once the pointer is on its
      // way: the config itself and, for the editor, Ace. Ace is not prefetched
      // with the pages since it would add ~150 kB to every first visit.
      prepareOpen(cfg) {
        prefetchConfig(cfg);
        this.loadEditor();
      },
      loadEditor() {
        // the editor reports a failure when opened
        loadAce().catch(() => {});
      },

      sortByUpdated(a, b, isAsc) {
        const diff =
          Date.parse(a.metadata.updated ?? 0) -
          Date.parse(b.metadata.updated ?? 0);
        return isAsc ? diff : -diff;
      },

      isDownloading(cfg) {
        return cfg != null && this.downloading.has(configKey(cfg));
      },

      updateConfigs() {
        this.loader.load({ force: true });
      },
      download(configList) {
        const configs = configList.map(configKey);
        // the button spins until the file is ready
        configs.forEach((key) => this.downloading.add(key));
        configsApi
          .downloadConfigs(configs)
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
          })
          .finally(() => {
            configs.forEach((key) => this.downloading.delete(key));
          });
      },
      deleteConfigs(configList) {
        const configs = configList.map(configKey);
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
          title:
            configs.length > 1 ? 'Delete the Configs' : 'Delete the Config',
          message: msg,
          cancelText: 'Cancel',
          confirmText: 'Delete',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => {
            for (var i = 0; i < configs.length; i++) {
              this.isWaiting = true;
              configsApi
                .deleteConfig(configs[i])
                .then(() => {
                  //delete from config list
                  let configsSet = new Set(configs);
                  configList.forEach(forgetConfig);
                  this.configs = this.configs.filter(
                    (item) => !configsSet.has(configKey(item)),
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
        let ext = /\.(yaml|yml|json)$/i;

        if (!ext.exec(file.name)) {
          showError('Valid file types are .yaml, .yml, and .json');
          return;
        }

        let formData = new FormData();
        formData.append('fileupload', file);

        configsApi
          .uploadConfig(formData)
          .then(() => {
            this.$buefy.toast.open({
              message: 'The file ' + file.name + ' was uploaded',
              type: 'is-success',
            });
            this.updateConfigs();
          })
          .catch((err) => {
            // the explained lines keep their layout in the dialog's textarea
            const validation = err.response?.data?.metadata?.validation;
            if (validation) {
              this.error = {
                modal: true,
                title: 'Validation Error',
                msg: validation,
              };
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
        this.viewer.config = null;
        this.viewer.title = null;
        this.viewer.obj = null;
      },
      // Opens the config ?view=Kind/name names once it is listed, or reports it
      // missing once this visit's load ends; either way drops the parameter.
      viewFromRoute() {
        const key = this.$route.query.view;
        if (key === undefined || !this.loaded) return;
        const cfg = findConfig(this.configs, key);
        if (!cfg && !this.listSettled) return;
        const query = { ...this.$route.query };
        delete query.view;
        this.$router.replace({ query });

        if (cfg && this.configAllowed('get', cfg)) {
          this.viewConfig(cfg);
        } else {
          showError(
            'Config not found',
            `The config list has no config ${String(key)} that your role can view.`,
          );
        }
      },
      // Opens the viewer at once and fills it when the config arrives; the
      // editor reuses the loaded config.
      async viewConfig(cfg) {
        this.viewer.config = cfg;
        this.viewer.title = configKey(cfg);
        this.viewer.obj = null;
        this.viewer.isActive = true;
        this.loadEditor();

        try {
          const obj = await fullConfig(cfg);
          // the viewer moved on to another config or closed meanwhile
          if (this.viewer.config !== cfg) return;

          // the builder's diagram is long and unreadable here
          if (obj.metadata.annotations?.['builder-xml']) {
            obj.metadata.annotations['builder-xml'] = '<SNIPPED>';
          }

          this.viewer.obj = YAML.dump(obj);
        } catch (err) {
          useErrorNotification(err);
          if (this.viewer.config === cfg) this.resetViewer();
        }
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

  /* the controls drop below the counts when both do not fit on one row */
  .configs-toolbar {
    flex-wrap: wrap;
    gap: 0.75rem;
  }

  .configs-toolbar .level-right {
    margin-left: auto;
  }

  .x-modal-dark :deep(textarea) {
    background-color: #686868;
    color: whitesmoke;
  }
  textarea {
    color: whitesmoke;
  }
</style>
