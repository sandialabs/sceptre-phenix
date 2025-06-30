<template>
  <div class="content">
    <div v-if="editor.use">
      <hr />
      <section class="hero is-light is-bold is-small">
        <div class="columns">
          <div class="column is-1" />
          <div class="column is-2">
            &nbsp; &nbsp;
            <b-field
              class="editor"
              label="Config Name"
              :type="editor.nameErrType"
              :message="editor.nameErrMsg"
              autofocus>
              <b-tooltip
                v-if="editor.action"
                label="change the name in the editor"
                type="is-light">
                <b-input v-model="configName" disabled />
              </b-tooltip>
              <b-input v-if="!editor.action" v-model="configName" />
            </b-field>
            <b-field class="editor" label="Config Kind">
              <b-tooltip
                v-if="editor.action"
                label="change the kind in the editor"
                type="is-light">
                <b-select
                  placeholder="Select a Kind"
                  v-model="configKind"
                  disabled>
                  <option v-for="(k, index) in kinds" :key="index" :value="k">
                    {{ k }}
                  </option>
                </b-select>
              </b-tooltip>
              <b-select
                v-if="!editor.action"
                placeholder="Select a Kind"
                v-model="configKind">
                <option v-for="(k, index) in kinds" :key="index" :value="k">
                  {{ k }}
                </option>
              </b-select>
            </b-field>
            <b-field class="editor" label="File Format">
              <b-radio
                type="is-white"
                v-model="editor.lang"
                native-value="yaml"
                @input="convertLang('yaml')">
                YAML
              </b-radio>
            </b-field>
            <b-field>
              <b-radio
                type="is-white"
                v-model="editor.lang"
                native-value="json"
                @input="convertLang('json')">
                JSON
              </b-radio>
            </b-field>
            <b-field class="editor" label="Vim Mode">
              <b-switch
                v-model="editor.vim"
                type="is-light"
                @input="changeKeybinding()">
                {{ keybinding }}
              </b-switch>
            </b-field>
            <b-tooltip
              v-if="expStart"
              label="you cannot edit a running experiment"
              type="is-light is-bottom"
              multilined>
              <button
                v-if="expStart"
                class="button is-warning"
                id="editor"
                disabled>
                Running
              </button>
            </b-tooltip>
            <button
              v-if="editor.action && !expStart"
              class="button is-success"
              id="editor"
              @click="saveConfig(editor.lang)"
              :disabled="!validConfig">
              Save
            </button>
            <button
              v-if="!editor.action"
              class="button is-success"
              id="editor"
              @click="createConfig(editor.lang)"
              :disabled="!validConfig">
              Create
            </button>
            <button
              class="button is-danger"
              id="editor"
              @click="confirmResetEditor">
              Exit
            </button>
          </div>
          <div class="column">
            <div class="hero-body">
              <editor
                ref="configEditor"
                v-model="config.str"
                @init="editorInit"
                :lang="editor.lang"
                theme="dracula"
                width="100%"
                height="500"></editor>
            </div>
          </div>
          <div class="column is-1" />
        </div>
      </section>
    </div>
    <div v-else>
      <div class="level">
        <div class="level-left" />
        <div class="level-right">
          <div class="level-item">
            <b-field position="is-center">
              <div v-if="configSelected.length > 0">
                <b-tooltip
                  v-if="
                    configSelected.every((c) =>
                      roleAllowed('configs', 'get', c.metadata.name),
                    )
                  "
                  label="download selected configs"
                  type="is-light is-top">
                  <button
                    class="button is-light"
                    id="main"
                    @click="action('dl', configSelected)">
                    <b-icon icon="download"></b-icon>
                  </button>
                </b-tooltip>
                <b-tooltip
                  v-if="
                    configSelected.every((c) =>
                      roleAllowed('configs', 'delete', c.metadata.name),
                    )
                  "
                  label="delete selected configs"
                  type="is-light is-top">
                  <button
                    class="button is-light"
                    id="main"
                    @click="action('del', configSelected)">
                    <b-icon icon="trash"></b-icon>
                  </button>
                </b-tooltip>
              </div>
            </b-field>
            &nbsp; &nbsp;
            <b-field position="is-right">
              <b-select placeholder="Filter on Kind" v-model="filterKind">
                <option v-for="(k, index) in kinds" :key="index" :value="k">
                  {{ k }}
                </option>
              </b-select>
              <b-autocomplete
                v-model="searchName"
                placeholder="Find a Config"
                icon="search"
                :data="filteredData"
                @select="(option) => (filtered = option)">
                <template #empty> No results found </template>
              </b-autocomplete>
              <p class="control">
                <b-tooltip
                  label="resets search filter and filter on kind"
                  type="is-light"
                  multilined>
                  <button
                    class="button"
                    style="color: #686868"
                    @click="
                      searchName = '';
                      filterKind = null;
                    ">
                    <b-icon icon="window-close"></b-icon>
                  </button>
                </b-tooltip>
              </p>
              &nbsp; &nbsp;
              <p v-if="roleAllowed('configs', 'create')" class="control">
                <b-tooltip label="create a new config" type="is-light is-top">
                  <button
                    class="button is-light"
                    id="main"
                    @click="createEditor">
                    <b-icon icon="plus"></b-icon>
                  </button>
                </b-tooltip>
                <b-tooltip label="upload a new config" type="is-light is-top">
                  <button
                    class="button is-light"
                    id="main"
                    @click="uploader.modal = true">
                    <b-icon icon="upload"></b-icon>
                  </button>
                </b-tooltip>
              </p>
            </b-field>
          </div>
        </div>
      </div>
      <div style="margin-top: -1em">
        <b-table
          :data="filteredConfigs"
          :paginated="table.isPaginated"
          :per-page="table.perPage"
          :current-page.sync="table.currentPage"
          :pagination-simple="table.isPaginationSimple"
          :pagination-size="table.paginationSize"
          :default-sort-direction="table.defaultSortDirection"
          default-sort="kind"
          checkable
          :checked-rows.sync="configSelected"
          ref="cfgTable">
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
            <template
              v-if="roleAllowed('configs', 'get', props.row.metadata.name)">
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
              @click="action('edit', props.row)">
              <b-icon icon="edit"></b-icon>
            </button>
            <button
              v-if="roleAllowed('configs', 'get', props.row.metadata.name)"
              class="button is-light is-small action"
              @click="action('dl', props.row)">
              <b-icon icon="download"></b-icon>
            </button>
            <button
              v-if="roleAllowed('configs', 'delete', props.row.metadata.name)"
              class="button is-light is-small action"
              @click="action('del', props.row)">
              <b-icon icon="trash"></b-icon>
            </button>
          </b-table-column>
        </b-table>
        <br />
        <b-field v-if="paginationNeeded" grouped position="is-right">
          <div class="control is-flex">
            <b-switch
              v-model="table.isPaginated"
              size="is-small"
              type="is-light"
              @input="changePaginate()"
              >Paginate</b-switch
            >
          </div>
        </b-field>
      </div>
    </div>
    <b-loading
      :is-full-page="true"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script setup>
  import { roleAllowed } from '@/utils/rbac.js';
</script>
<script>
  import YAML from 'js-yaml';

  import ace from 'brace';
  import FileSaver from 'file-saver';
  import EventBus from '@/utils/eventBus.js';

  import axiosInstance from '@/utils/axios.js';
  import { usePhenixStore } from '@/stores/phenix.js';

  // import { rbacMixin } from '@/mixins/rbacMixin.js';

  import { debounce } from 'lodash-es';

  export default {
    // mixins: [rbacMixin],
    created() {
      this.updateConfigs();
      this.debouncedUpdateConfigTemplate = debounce(
        this.updateConfigTemplate,
        500,
      );
    },
    computed: {
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

        var name_re = new RegExp(this.searchName, 'i');
        var data = [];

        for (let i in configs) {
          let cfg = configs[i];
          if (cfg.metadata.name.match(name_re)) {
            data.push(cfg);
          }
        }
        return data;
      },

      filteredData() {
        let names = this.configs.map((config) => {
          return config.metadata.name;
        });
        return names.filter((option) => {
          return (
            option
              .toString()
              .toLowerCase()
              .indexOf(this.searchName.toLowerCase()) >= 0
          );
        });
      },

      paginationNeeded() {
        const store = usePhenixStore();
        var user = store.username;

        if (localStorage.getItem(user + '.lastPaginate')) {
          this.table.isPaginated =
            localStorage.getItem(user + '.lastPaginate') == 'true';
        }

        if (this.configs.length <= 10) {
          this.table.isPaginated = false;
          return false;
        } else {
          return true;
        }
      },

      keybinding() {
        if (this.editor.vim) {
          return 'enabled';
        } else {
          return 'disabled';
        }
      },

      configName: {
        get: function () {
          try {
            let config = this.getConfigObj();
            return config.metadata.name;
          } catch {
            return this.config.obj.metadata.name;
          }
        },

        set: function (name) {
          try {
            this.config.obj = this.getConfigObj();
          } catch {
            return;
          }

          this.config.obj.metadata.name = name;
          this.config.str = this.getConfigStr();
        },
      },

      configKind: {
        get: function () {
          let config = null;

          try {
            config = this.getConfigObj();
          } catch {
            return this.config.obj.kind;
          }

          // This if-else block is the logic for updating the API version to v2
          // if Scenario is selected or manually typed as the config kind. Right
          // now, scenarios are the only config kinds that have a v2. All other
          // kinds are v1. In the future, a better approach to this would be to
          // query the API for the latest version for each kind rather than
          // hard-coding it.
          if (config.kind == 'Scenario') {
            if (config.apiVersion != 'phenix.sandia.gov/v2') {
              config.apiVersion = 'phenix.sandia.gov/v2';
            }
          } else {
            if (this.kinds.includes(config.kind)) {
              if (config.apiVersion != 'phenix.sandia.gov/v1') {
                config.apiVersion = 'phenix.sandia.gov/v1';
              }
            }
          }

          if (!this.editor.action && this.kinds.includes(config.kind)) {
            this.debouncedUpdateConfigTemplate(config);
          }

          return config.kind;
        },

        set: function (value) {
          // Will be undefined if dropdown isn't set.
          if (!value) {
            return;
          }

          try {
            this.config.obj = this.getConfigObj();
          } catch {
            return;
          }

          this.config.obj.kind = value;
          this.config.str = this.getConfigStr();
        },
      },

      validConfig() {
        let config = null;

        try {
          config = this.getConfigObj();
        } catch {
          return false;
        }

        if (config.metadata) {
          if (config.metadata.name) {
            let valid = /^[a-zA-Z0-9_@.-]*$/.test(config.metadata.name);

            if (valid) {
              this.editor.nameErrType = null;
              this.editor.nameErrMsg = null;

              return true;
            }
          }
        }

        this.editor.nameErrType = 'is-danger';
        this.editor.nameErrMsg = 'invalid name';

        return false;
      },
    },

    methods: {
      updateConfigTemplate(config) {
        let version = config.apiVersion.split('/')[1];

        // Always get v2 (latest version) of full schema.
        axiosInstance
          .get('schemas/v2', { headers: { Accept: 'application/json' } })
          .then((spec) => {
            axiosInstance
              .get('schemas/' + config.kind + '/' + version, {
                headers: { Accept: 'application/json' },
              })
              .then((schema) => {
                config.spec = OpenAPISampler.sample(
                  schema.body,
                  { skipReadOnly: true },
                  spec.body,
                );
                this.config.str = this.getConfigStr(null, config);
              })
              .catch((err) => {
                this.errorNotification(err);
              });
          })
          .catch((err) => {
            this.errorNotification(err);
            this.isWaiting = false;
          });
      },
      handler(event) {
        event.data.split(/\r?\n/).forEach((m) => {
          let msg = JSON.parse(m);
          this.handle(msg);
        });
      },

      handle(msg) {
        if (msg.resource.type != 'config') {
          return;
        }

        let cfg = this.configs;

        switch (msg.resource.action) {
          case 'create': {
            cfg.push(msg.result);

            this.configs = [...cfg];

            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' config has been created.',
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'update': {
            let config = msg.resource.name.split('/');
            let name = msg.result.kind + '/' + msg.result.metadata.name;

            for (let i = 0; i < cfg.length; i++) {
              if (cfg[i].kind == config[0]) {
                if (cfg[i].metadata.name == config[1]) {
                  cfg[i] = msg.result;

                  break;
                }
              }
            }

            this.configs = [...cfg];

            this.$buefy.toast.open({
              message:
                'The ' +
                msg.resource.name +
                ' config has been updated to ' +
                name +
                '.',
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'delete': {
            let config = msg.resource.name.split('/');

            for (let i = 0; i < cfg.length; i++) {
              if (cfg[i].kind == config[0]) {
                if (cfg[i].metadata.name == config[1]) {
                  cfg.splice(i, 1);

                  break;
                }
              }
            }

            this.configs = [...cfg];

            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' config has been deleted.',
              type: 'is-success',
              duration: 4000,
            });

            break;
          }
        }
      },
      updateConfigs() {
        axiosInstance
          .get('configs')
          .then((response) => {
            const state = response.data;
            this.configs = state.configs === null ? [] : state.configs;
            this.isWaiting = false;
          })
          .catch((error) => {
            this.isWaiting = false;
            this.errorNotification(err);
          });
      },

      changePaginate() {
        const store = usePhenixStore();
        var user = store.username;

        localStorage.setItem(user + '.lastPaginate', this.table.isPaginated);
      },

      action(a, c) {
        if (!Array.isArray(c)) {
          let temp = [];
          temp.push(c);
          c = temp;
        }

        switch (a) {
          case 'edit': {
            this.viewer.modal = false;
            let name = c[0].kind + '/' + c[0].metadata.name;

            this.isWaiting = true;

            this.$http
              .get('configs/' + name, {
                headers: { Accept: 'application/json' },
              })
              .then(
                (response) => {
                  if (this.isBuilderTopology(response.body)) {
                    this.$buefy.dialog.alert({
                      title: 'Built by Builder',
                      message:
                        'This configuration can only be edited in Builder',
                      confirmText: 'OK',
                      type: 'is-warning',
                      hasIcon: true,
                    });
                  } else {
                    this.config.obj = response.body;
                    this.config.str = this.getConfigStr('yaml');

                    if (this.config.obj.kind == 'Experiment') {
                      if (
                        this.config.obj.status &&
                        this.config.obj.status.startTime !== ''
                      ) {
                        this.expStart = true;
                      }
                    }

                    this.editor.action = true;
                    this.editor.lang = 'yaml';
                    this.editor.use = true;
                  }
                },
                (err) => {
                  this.errorNotification(err);
                },
              );

            this.isWaiting = false;

            break;
          }

          case 'dl': {
            let configs = [];

            for (var i = 0; i < c.length; i++) {
              let name = c[i].kind + '/' + c[i].metadata.name;
              configs.push(name);
            }

            this.$http
              .post('configs/download', JSON.stringify(configs), {
                headers: {
                  'Content-Type': 'application/json',
                  Accept: 'application/x-yaml',
                },
                responseType: 'blob',
              })
              .then(
                (response) => {
                  if (configs.length == 1) {
                    let body = new Blob([response.body], {
                      type: 'text/plain',
                    });
                    FileSaver.saveAs(
                      body,
                      c[0].kind + '-' + c[0].metadata.name + '.yml',
                    );
                  } else {
                    FileSaver.saveAs(response.body, 'configs.zip');
                  }

                  this.configSelected = [];
                },
                (err) => {
                  this.errorNotification(err);
                },
              );

            break;
          }

          case 'del':
            {
              let configs = [];
              let msg = '';

              for (var i = 0; i < c.length; i++) {
                let name = c[i].kind + '/' + c[i].metadata.name;
                configs.push(name);
              }

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

                    this.$http.delete('configs/' + configs[i]).then(
                      () => {
                        this.isWaiting = false;
                      },
                      (err) => {
                        this.errorNotification(err);
                        this.isWaiting = false;
                      },
                    );
                  }
                },
              });
            }

            break;
        }
      },

      isBuilderTopology(cfg) {
        if (cfg.kind == 'Topology') {
          if ('annotations' in cfg.metadata) {
            return 'builder-xml' in cfg.metadata.annotations;
          }
        }

        return false;
      },

      viewConfig(cfg) {
        this.viewer.kind = cfg.kind;
        this.viewer.name = cfg.metadata.name;
        this.viewer.title = cfg.kind + '/' + cfg.metadata.name;

        this.isWaiting = true;

        this.$http
          .get('configs/' + this.viewer.title, {
            headers: { Accept: 'application/json' },
          })
          .then(
            (response) => {
              this.config.obj = response.body;
              this.config.str = this.getConfigStr('yaml');
              this.viewer.modal = true;
            },
            (err) => {
              this.errorNotification(err);
            },
          );

        this.isWaiting = false;
      },

      saveConfig(lang) {
        let config = this.getConfigObj(lang);

        if (!config) {
          this.$buefy.toast.open({
            message: 'missing config to create',
            type: 'is-danger',
            duration: 4000,
          });

          return;
        }

        // Using component config to get name just in case name changed as part
        // of edit.
        let name = this.config.obj.kind + '/' + this.config.obj.metadata.name;

        this.$buefy.dialog.confirm({
          title: 'Modify the Config',
          message:
            'This will <strong>overwrite</strong> the ' +
            name +
            ' config. Are you sure you want to do this?',
          cancelText: 'Cancel',
          confirmText: 'Save',
          type: 'is-warning',
          hasIcon: true,
          onConfirm: () => {
            this.$http
              .put('configs/' + name, JSON.stringify(config), {
                headers: { 'Content-Type': 'application/json' },
              })
              .then(
                () => {
                  this.resetEditor();
                },
                (response) => {
                  let name = config.kind + '/' + config.metadata.name;

                  if (
                    response.body.metadata &&
                    response.body.metadata.validation
                  ) {
                    this.error.title = 'Validation Error';
                    this.error.msg = response.body.metadata.validation;
                    this.error.modal = true;
                  } else {
                    this.errorNotification(response); // this may need to be updated
                  }
                },
              );
          },
        });

        this.isWaiting = false;
      },

      createConfig(lang) {
        let config = this.getConfigObj(lang);

        if (!config) {
          this.$buefy.toast.open({
            message: 'missing config to create',
            type: 'is-danger',
            duration: 4000,
          });

          return;
        }

        this.$http
          .post('configs', JSON.stringify(config), {
            headers: { 'Content-Type': 'application/json' },
          })
          .then(
            () => {
              this.resetEditor();
            },
            (response) => {
              let name = config.kind + '/' + config.metadata.name;

              if (response.body.metadata && response.body.metadata.validation) {
                this.error.title = 'Validation Error';
                this.error.msg = response.body.metadata.validation;
                this.error.modal = true;
              } else {
                this.errorNotification(response); // this may need to be updated
              }
            },
          );

        this.isWaiting = false;
      },

      async createEditor() {
        this.config.obj = {
          apiVersion: 'phenix.sandia.gov/v1',
          kind: 'TODO',
          metadata: {
            name: 'TODO',
          },
          spec: {
            TODO: 'FIXME',
          },
        };

        this.config.str = this.getConfigStr('yaml');

        this.editor.action = false;
        this.editor.lang = 'yaml';
        this.editor.use = true;
      },

      convertLang(lang) {
        switch (lang) {
          case 'yaml': {
            this.editor.lang = 'yaml';

            this.config.obj = this.getConfigObj('json');
            this.config.str = this.getConfigStr('yaml');

            return;
          }

          case 'json': {
            this.editor.lang = 'json';

            this.config.obj = this.getConfigObj('yaml');
            this.config.str = this.getConfigStr('json');

            return;
          }
        }
      },

      // Returns new config object based on the current editor language.
      getConfigObj(lang = null, str = null) {
        if (!lang) {
          lang = this.editor.lang;
        }

        if (!str) {
          str = this.config.str;
        }

        switch (lang) {
          case 'json': {
            if (this.config.builderXML) {
              let obj = JSON.parse(str);
              obj.metadata.annotations['builder-xml'] = this.config.builderXML;

              return obj;
            } else {
              return JSON.parse(str);
            }
          }

          case 'yaml': {
            if (this.config.builderXML) {
              let obj = YAML.load(str);
              obj.metadata.annotations['builder-xml'] = this.config.builderXML;

              return obj;
            } else {
              return YAML.load(str);
            }
          }
        }
      },

      // Returns stringified version of config object based on the current
      // editor language.
      getConfigStr(lang = null, obj = null) {
        if (!lang) {
          lang = this.editor.lang;
        }

        if (!obj) {
          obj = this.config.obj;
        }

        if ('annotations' in obj.metadata) {
          if ('builder-xml' in obj.metadata.annotations) {
            this.config.builderXML = obj.metadata.annotations['builder-xml'];
            obj.metadata.annotations['builder-xml'] = '<SNIPPED>';
          }
        }

        switch (lang) {
          case 'json': {
            return JSON.stringify(obj, '', 2);
          }

          case 'yaml': {
            return YAML.dump(obj);
          }
        }
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

        this.$http.post('configs', formData).then(
          () => {
            this.$buefy.toast.open({
              message: 'The file ' + file.name + ' was uploaded',
              type: 'is-success',
              duration: 4000,
            });

            this.updateConfigs();
          },
          (response) => {
            if (response.body.metadata && response.body.metadata.validation) {
              this.error.title = 'Validation Error';
              this.error.msg = response.body.metadata.validation;
              this.error.modal = true;
            } else {
              this.errorNotification(response); // this may need to be updated
            }
          },
        );

        this.resetUploader();
        this.isWaiting = false;
      },

      resetErrorModal() {
        this.error.modal = false;
        this.error.title = null;
        this.error.msg = null;
      },

      resetEditor() {
        this.editor.use = false;
        this.editor.vim = false;
        this.config.obj = null;
        this.config.str = null;
        this.config.builderXML = null;

        this.editor.nameErrType = null;
        this.editor.nameErrMsg = null;
      },

      confirmResetEditor() {
        this.$buefy.dialog.confirm({
          title: 'Edits in Progress',
          message:
            'You will lose your current edits... do you want to continue?',
          confirmText: 'Continue',
          cancelText: 'Cancel',
          type: 'is-warning',
          hasIcon: true,
          onConfirm: () => {
            this.resetEditor();
          },
        });
      },

      resetUploader() {
        this.uploader.modal = false;
        this.uploader.file = null;
      },

      resetViewer() {
        this.config.obj = null;
        this.config.str = null;
        this.config.builderXML = null;
        this.viewer.modal = false;
        this.viewer.kind = null;
        this.viewer.name = null;
      },

      changeKeybinding() {
        let user = this.$store.getters.username;
        localStorage.setItem(user + '.vimMode', this.editor.vim);

        if (this.editor.vim) {
          this.$refs.configEditor.editor.setKeyboardHandler('ace/keyboard/vim');
        } else {
          this.$refs.configEditor.editor.setKeyboardHandler(null);
        }
      },
    },
    directives: {
      focus: {
        inserted(el) {
          if (el.tagName == 'INPUT') {
            el.focus();
          } else {
            el.querySelector('input').focus();
          }
        },
      },
    },

    data() {
      return {
        error: {
          modal: false,
          title: null,
          msg: null,
        },
        viewer: {
          modal: false,
          kind: null,
          name: null,
        },
        editor: {
          use: false,
          action: null,
          lang: null,
          nameErrType: null,
          nameErrMsg: null,
          vim: false,
        },
        uploader: {
          modal: false,
          file: null,
        },
        expStart: false,
        table: {
          isPaginated: false,
          perPage: 10,
          currentPage: 1,
          isPaginationSimple: true,
          paginationSize: 'is-small',
          defaultSortDirection: 'asc',
        },
        kinds: ['Topology', 'Scenario', 'Experiment', 'Image', 'User', 'Role'],
        ext: ['yaml', 'yml', 'json'],
        kind: null,
        filterKind: null,
        configs: [],
        config: {
          obj: null,
          str: null,
          builderXML: null,
        },
        checkAll: false,
        showSelectBar: false,
        configSelected: [],
        searchName: '',
        filtered: null,
        isWaiting: true,
      };
    },
  };
</script>

<style scoped>
  div.autocomplete :deep(a.dropdown-item) {
    color: #383838 !important;
  }

  .x-modal-dark {
    background-color: #5b5b5b;
  }

  .x-modal-dark :deep(p) {
    color: whitesmoke;
  }

  .x-modal-dark :deep(textarea) {
    background-color: #686868;
    color: whitesmoke;
  }

  .x-config-text {
    font-family: monospace;
  }

  .editor :deep(.label) {
    color: whitesmoke;
  }

  button#editor {
    margin-top: 125px;
    margin-right: 10px;
  }

  button.action {
    margin-right: 5px;
  }
</style>
