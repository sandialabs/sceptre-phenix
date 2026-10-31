<template>
  <b-modal
    v-model="error.modal"
    @close="resetErrorModal"
    has-modal-card
    aria-role="dialog"
    aria-modal
    :aria-label="error.title"
    close-button-aria-label="Close">
    <div class="modal-card" style="width: 50em">
      <header class="modal-card-head x-modal-dark">
        <h2 class="modal-card-title">{{ error.title }}</h2>
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
  <section class="hero is-light is-bold is-small">
    <div class="columns p-4">
      <div class="column is-1" />
      <div class="column is-2">
        <!-- Focus starts here when the editor opens, not on the page. -->
        <h1
          ref="heading"
          class="title is-5 config-editor__heading"
          tabindex="-1">
          {{ heading }}
        </h1>
        <b-field
          class="editor"
          label="Config Name"
          label-for="config-editor-name"
          :type="nameErrType"
          :message="nameErrMsg"
          autofocus>
          <b-tooltip
            v-if="mode == 'edit'"
            label="change the name in the editor"
            type="is-light">
            <b-input
              id="config-editor-name"
              :compat-fallthrough="false"
              v-model="configName"
              disabled />
          </b-tooltip>
          <b-input
            v-if="mode == 'create'"
            id="config-editor-name"
            :compat-fallthrough="false"
            v-model="configName" />
        </b-field>

        <b-field
          class="editor"
          label="Config Kind"
          label-for="config-editor-kind">
          <b-tooltip
            v-if="mode == 'edit'"
            label="change the kind in the editor"
            type="is-light">
            <b-select
              id="config-editor-kind"
              :compat-fallthrough="false"
              placeholder="Select a Kind"
              v-model="configKind"
              disabled>
              <option v-for="(k, index) in kinds" :key="index" :value="k">
                {{ k }}
              </option>
            </b-select>
          </b-tooltip>
          <b-select
            v-if="mode == 'create'"
            id="config-editor-kind"
            :compat-fallthrough="false"
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
            :disabled="!validConfig"
            native-value="yaml"
            @input="convertLang('yaml')">
            <abbr title="YAML Ain't Markup Language">YAML</abbr>
          </b-radio>
        </b-field>
        <b-field>
          <b-radio
            type="is-white"
            v-model="editor.lang"
            :disabled="!validConfig"
            native-value="json"
            @input="convertLang('json')">
            JSON
          </b-radio>
        </b-field>

        <b-field class="editor" label="Vim Mode">
          <b-switch
            v-model="editor.vim"
            type="is-light"
            @change="changeKeybinding">
            {{ keybinding }}
          </b-switch>
        </b-field>

        <b-tooltip
          v-if="expRunning"
          label="you cannot edit a running experiment"
          type="is-light is-bottom"
          multilined>
          <button
            v-if="expRunning"
            class="button is-warning"
            id="editor"
            disabled>
            Running
          </button>
        </b-tooltip>

        <button
          v-if="mode == 'edit' && !expStart"
          class="button is-success"
          id="editor"
          @click="saveConfig(editor.lang)"
          :disabled="!validConfig">
          Save
        </button>
        <button
          v-if="mode == 'create'"
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
        <div>
          <AceEditor
            v-model:value="config.str"
            :lang="editor.lang"
            :vim="editor.vim"
            @save="configSentSave"
            @reset="configSentReset" />
          <p class="help">
            Press Enter on the editor to type in it, and Escape to leave it
            (twice in Vim mode), so that Tab moves on.
          </p>
          <b-loading
            :is-full-page="false"
            v-model="editor.isLoading"></b-loading>
        </div>
      </div>
      <div class="column is-1" />
    </div>
  </section>
</template>
<script>
  import { useErrorNotification } from '@/utils/errorNotif';
  import YAML from 'js-yaml';
  import AceEditor from './AceEditor.vue';
  import { sample } from 'openapi-sampler';
  import { debounce } from 'lodash-es';

  import axiosInstance from '@/utils/axios.js';

  export default {
    expose: ['confirmResetEditor'],
    components: {
      AceEditor,
    },
    emits: ['is-done'],
    props: {
      mode: String,
      editorConfig: Object,
    },
    mounted() {
      this.$refs.heading?.focus();

      //set vim mode
      let user = localStorage.getItem('user');
      if (localStorage.getItem(user + '.vimMode')) {
        this.editor.vim = localStorage.getItem(user + '.vimMode') == 'true';
      }

      if (this.mode == 'create') {
        //default config
        this.config.obj = {
          apiVersion: 'phenix.sandia.gov/v1',
          kind: 'TODO',
          metadata: { name: 'TODO' },
          spec: { TODO: 'FIXME' },
        };
        this.config.str = this.getConfigStr('yaml');
        this.editor.lang = 'yaml';
      } else if (this.mode == 'edit') {
        const name = `${this.editorConfig.kind}/${this.editorConfig.metadata.name}`;
        this.editor.isLoading = true;

        axiosInstance
          .get('configs/' + name, { headers: { Accept: 'application/json' } })
          .then((response) => {
            this.config.obj = response.data;
            this.config.str = this.getConfigStr('yaml');

            if (this.config.obj.kind == 'Experiment') {
              if (
                this.config.obj.status &&
                this.config.obj.status.startTime !== ''
              ) {
                this.expStart = true;
              }
            }
            this.editor.lang = 'yaml';
            this.editor.isLoading = false;
          })
          .catch((err) => {
            useErrorNotification(err);
            this.editor.isLoading = false;
          });
      }
      window.addEventListener('beforeunload', this.handlePageReload);
    },
    unmounted() {
      window.removeEventListener('beforeunload', this.handlePageReload);
    },
    created() {
      this.debouncedUpdateConfigTemplate = debounce(
        this.updateConfigTemplate,
        500,
      );
    },
    data() {
      return {
        kinds: ['Topology', 'Scenario', 'Experiment', 'Image', 'User', 'Role'],
        editor: {
          lang: 'yaml',
          vim: false,
          isLoading: false,
        },
        config: {
          obj: {},
          str: '',
          builderXML: null,
        },
        error: {
          modal: false,
          title: null,
          msg: null,
        },

        expRunning: false,
        expStart: false, //TODO: remove

        pastConfigKind: null,
      };
    },
    methods: {
      handlePageReload(event) {
        event.preventDefault();
      },
      configSentSave() {
        if (this.mode == 'edit') {
          this.saveConfig();
        } else if (this.mode == 'create') {
          this.createConfig();
        }
      },
      configSentReset() {
        if (confirm('Any edits will be lost... are you sure?')) {
          this.resetEditor('');
        }
      },
      resetErrorModal() {
        this.error.modal = false;
        this.error.title = null;
        this.error.msg = null;
      },
      changeKeybinding() {
        console.log(this.editor.vim);
        let user = localStorage.getItem('user');
        localStorage.setItem(user + '.vimMode', this.editor.vim);
      },
      updateConfigTemplate(config) {
        this.editor.isLoading = true;
        let version = config.apiVersion.split('/')[1];

        // The kind's schema refers to sub-schemas in the full schema of the
        // same version, so get that version's full schema to resolve them.
        axiosInstance
          .get('schemas/' + version, {
            headers: { Accept: 'application/json' },
          })
          .then((spec) => {
            axiosInstance
              .get('schemas/' + config.kind + '/' + version, {
                headers: { Accept: 'application/json' },
              })
              .then((schema) => {
                config.spec = sample(
                  schema.data,
                  { skipReadOnly: true },
                  spec.data,
                );
                this.config.str = this.getConfigStr(null, config);
                this.editor.isLoading = false;

                //update past kind so any edits to file doesn't reset
                this.pastConfigKind = config.kind;
              })
              .catch((err) => {
                useErrorNotification(err);
                this.editor.isLoading = false;
              });
          })
          .catch((err) => {
            useErrorNotification(err);
            this.editor.isLoading = false;
          });
      },
      async confirmResetEditor() {
        return this.$buefy.dialog.confirm({
          title: 'Edits in Progress',
          message:
            'You will lose your current edits... do you want to continue?',
          confirmText: 'Continue',
          cancelText: 'Cancel',
          type: 'is-warning',
          hasIcon: true,
          onConfirm: () => {
            this.resetEditor('');
          },
        });
      },
      resetEditor(msg, savedName = '') {
        this.editor.vim = false;

        this.config.str = '';
        this.config.builderXML = null;
        this.editor.nameErrType = null;
        this.editor.nameErrMsg = null;

        this.$emit('is-done', msg, savedName);
      },
      getConfigObj(lang = null) {
        if (!lang) {
          lang = this.editor.lang;
        }
        let str = this.config.str;
        let obj = null;

        if (lang == 'json') {
          try {
            obj = JSON.parse(str);
          } catch {
            return null;
          }
        } else if (lang == 'yaml') {
          try {
            obj = YAML.load(str);
          } catch {
            return null;
          }
        }

        if (this.config.builderXML) {
          obj.metadata.annotations['builder-xml'] = this.config.builderXML;
        }
        return obj;
      },
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
      convertLang(lang) {
        switch (lang) {
          case 'yaml': {
            this.editor.lang = 'yaml';
            this.config.obj = this.getConfigObj('json');
            this.config.str = this.getConfigStr('yaml', this.config.obj);
            return;
          }

          case 'json': {
            this.editor.lang = 'json';
            this.config.obj = this.getConfigObj('yaml');
            this.config.str = this.getConfigStr('json', this.config.obj);
          }
        }
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
            axiosInstance
              .put('configs/' + name, JSON.stringify(config), {
                headers: { 'Content-Type': 'application/json' },
              })
              .then(() => {
                // The save can rename the config, so report its new name.
                this.resetEditor(
                  `config ${name} has been edited`,
                  `${config.kind}/${config.metadata.name}`,
                );
              })
              .catch((err) => {
                console.log(err);
                const resp = err.response.data;
                // useErrorNotification(err);
                if (resp.metadata && resp.metadata.validation) {
                  this.error.title = 'Validation Error';
                  this.error.msg = resp.metadata.validation;
                  this.error.modal = true;
                } else {
                  this.error.title = 'Validation Error';
                  this.error.msg = resp.message;
                  this.error.modal = true;
                }
              });
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

        axiosInstance
          .post('configs', JSON.stringify(config), {
            headers: { 'Content-Type': 'application/json' },
          })
          .then(() => {
            this.resetEditor(
              `config ${config.kind}/${config.metadata.name} has been created`,
            );
          })
          .catch((err) => {
            console.log(err);

            if (
              err.response.data.metadata &&
              err.response.data.metadata.validation
            ) {
              this.error.title = 'Validation Error';
              this.error.msg = err.response.data.metadata.validation;
              this.error.modal = true;
            } else {
              useErrorNotification(err); // this may need to be updated
            }
          });

        this.isWaiting = false;
      },
    },
    computed: {
      // What the editor is for: "Edit Topology/web", or "New config".
      heading() {
        return this.mode == 'edit' && this.editorConfig
          ? `Edit ${this.editorConfig.kind}/${this.editorConfig.metadata.name}`
          : 'New config';
      },
      validName() {
        return /^[a-zA-Z0-9_@.-]*$/.test(this.configName);
      },
      nameErrType() {
        if (!this.validName) {
          return 'is-danger';
        }
        return null;
      },
      nameErrMsg() {
        if (!this.validName) {
          return 'invalid name';
        }
        return null;
      },
      validConfig() {
        const config = this.getConfigObj();
        if (config == null) {
          return false;
        }

        if (config.metadata) {
          if (config.metadata.name) {
            let valid = /^[a-zA-Z0-9_@.-]*$/.test(config.metadata.name);
            if (valid) {
              return true;
            }
          }
        }
        return false;
      },
      keybinding() {
        if (this.editor.vim) {
          return 'enabled';
        } else {
          return 'disabled';
        }
      },
      configName: {
        get() {
          const config = this.getConfigObj();
          if (config == null) {
            try {
              return this.config.obj.metadata.name;
            } catch {
              return null;
            }
          }
          return config.metadata.name;
        },
        set(name) {
          const config = this.getConfigObj();
          if (config != null) {
            this.config.obj = config;
          }

          this.config.obj.metadata.name = name;
          this.config.str = this.getConfigStr(); //regenerate string from obj
        },
      },
      configKind: {
        get: function () {
          let config = this.getConfigObj();
          if (config == null) {
            return null;
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

          if (
            this.mode == 'create' &&
            this.kinds.includes(config.kind) &&
            config.kind !== this.pastConfigKind
          ) {
            this.debouncedUpdateConfigTemplate(config);
            // this.updateConfigTemplate(config)
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
            //can't set config kind if object is messed up
            return;
          }

          this.config.obj.kind = value;
          this.config.str = this.getConfigStr();
        },
      },
    },
  };
</script>
<style scoped>
  /*Quickfix for css. incorporate into main css packages*/
  /* Darker stops give white text 7:1 or more on all of the gradient. */
  .hero.is-light.is-bold {
    background-image: linear-gradient(
      141deg,
      #564749 0%,
      #575757 71%,
      #595251 100%
    );
  }
  .hero.is-light {
    background-color: #575757;
    color: #fff;
  }

  .x-modal-dark {
    background-color: #4a4a4a;
  }

  .x-modal-dark :deep(p) {
    color: whitesmoke;
  }

  .x-modal-dark :deep(textarea) {
    background-color: #575757;
    color: white;
  }

  .x-config-text {
    font-family: monospace;
  }

  .editor :deep(.label) {
    color: white;
  }

  .config-editor__heading {
    color: white;
    overflow-wrap: anywhere;
  }

  button#editor {
    margin-top: 125px;
    margin-right: 10px;
  }

  button.action {
    margin-right: 5px;
  }
</style>
