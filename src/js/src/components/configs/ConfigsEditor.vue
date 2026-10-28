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
    v-model="conflict.modal"
    has-modal-card
    aria-modal
    aria-labelledby="config-conflict-title">
    <div class="modal-card" style="width: 40em">
      <header class="modal-card-head x-modal-dark">
        <p id="config-conflict-title" class="modal-card-title">
          Config Changed
        </p>
      </header>
      <section class="modal-card-body x-modal-dark">
        <p>
          {{ conflict.name }} changed since you opened it (revision
          {{ conflict.from }} → {{ conflict.to ?? 'unknown' }}).
        </p>
      </section>
      <footer class="modal-card-foot x-modal-dark buttons is-right">
        <button class="button is-light" @click="reloadConflicted">
          Reload it (your edits are lost)
        </button>
        <button class="button is-warning" @click="overwriteConflicted">
          Overwrite anyway
        </button>
        <button class="button is-dark" @click="conflict.modal = false">
          Cancel
        </button>
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
          :type="nameErrType"
          :message="nameErrMsg"
          autofocus>
          <b-tooltip
            v-if="mode == 'edit'"
            label="change the name in the editor"
            type="is-light">
            <b-input v-model="configName" disabled />
          </b-tooltip>
          <b-input v-if="mode == 'create'" v-model="configName" />
        </b-field>

        <b-field class="editor" label="Config Kind">
          <b-tooltip
            v-if="mode == 'edit'"
            label="change the kind in the editor"
            type="is-light">
            <b-select placeholder="Select a Kind" v-model="configKind" disabled>
              <option v-for="(k, index) in kinds" :key="index" :value="k">
                {{ k }}
              </option>
            </b-select>
          </b-tooltip>
          <b-select
            v-if="mode == 'create'"
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
            YAML
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
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import YAML from 'js-yaml';
  import AceEditor from './AceEditor.vue';
  import { debounce } from 'lodash-es';

  import * as configsApi from '@/api/configs.js';
  import { configKey, forgetConfig, fullConfig } from '@/utils/configCache.js';

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
        this.original = this.config.str;
      } else if (this.mode == 'edit') {
        this.editor.isLoading = true;

        this.loadConfig();
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

        original: '', // the config as loaded, to tell whether it was edited
        // the store revision of the config as loaded, which a save names in
        // If-Match so it does not overwrite a change made meanwhile
        loadedRevision: null,
        // a save refused because the config changed: its name, the revision
        // loaded and the current one, and the save to resend
        conflict: { modal: false, name: '', from: null, to: null, save: null },

        expStart: false, //TODO: remove

        pastConfigKind: null,
      };
    },
    methods: {
      // opens the edited config: usually already loaded by the viewer or a
      // hover over the config
      loadConfig() {
        this.editor.isLoading = true;

        fullConfig(this.editorConfig)
          .then((config) => {
            this.config.obj = config;
            this.config.str = this.getConfigStr('yaml');
            this.original = this.config.str;
            this.loadedRevision = config.metadata?.revision ?? null;

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
      },
      // asks before a reload throws away edits
      handlePageReload(event) {
        if (this.changed) event.preventDefault();
      },
      configSentSave() {
        if (this.mode == 'edit') {
          this.saveConfig();
        } else if (this.mode == 'create') {
          this.createConfig();
        }
      },
      configSentReset() {
        this.confirmResetEditor();
      },
      resetErrorModal() {
        this.error.modal = false;
        this.error.title = null;
        this.error.msg = null;
      },
      changeKeybinding() {
        let user = localStorage.getItem('user');
        localStorage.setItem(user + '.vimMode', this.editor.vim);
      },
      async updateConfigTemplate(config) {
        this.editor.isLoading = true;
        const version = config.apiVersion.split('/')[1];

        try {
          // the sampler is needed only here, so it loads with the schemas
          const [{ sample }, spec, schema] = await Promise.all([
            import('openapi-sampler'),
            // the kind's schema refers to sub-schemas in the full schema of
            // the same version, so get that version's full schema
            configsApi.getSchemaSpec(version),
            configsApi.getKindSchema(config.kind, version),
          ]);
          config.spec = sample(schema.data, { skipReadOnly: true }, spec.data);
          this.config.str = this.getConfigStr(null, config);

          // update past kind so any edits to file doesn't reset
          this.pastConfigKind = config.kind;
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.editor.isLoading = false;
        }
      },
      // Closes the editor, first asking whether to drop any edits. Resolves
      // to whether it closed.
      confirmResetEditor() {
        if (!this.changed) {
          this.resetEditor('');
          return Promise.resolve(true);
        }

        return new Promise((resolve) => {
          this.$buefy.dialog.confirm({
            title: 'Edits in Progress',
            message:
              'You will lose your current edits... do you want to continue?',
            confirmText: 'Continue',
            cancelText: 'Cancel',
            type: 'is-warning',
            hasIcon: true,
            onConfirm: () => {
              this.resetEditor('');
              resolve(true);
            },
            onCancel: () => resolve(false),
          });
        });
      },
      resetEditor(msg, savedName = '') {
        this.editor.vim = false;

        this.config.str = '';
        this.config.builderXML = null;

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
          showError('there is no config to create');
          return;
        }

        // Using component config to get name just in case name changed as part
        // of edit.
        let name = configKey(this.config.obj);

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
          onConfirm: () => this.putConfig(name, config, this.loadedRevision),
        });
      },

      // Saves config over the config `name`. With a revision, the server
      // refuses the save (412) if the config has changed since that revision
      // was loaded, and a dialog offers to reload it or overwrite it anyway.
      putConfig(name, config, revision) {
        return configsApi
          .updateConfig(name, config, revision)
          .then(() => {
            // the cached copies are of the revision this save replaced
            forgetConfig(this.editorConfig ?? this.config.obj);
            forgetConfig(config);
            // the save can rename the config, so report its new name
            this.resetEditor(
              `config ${name} has been edited`,
              configKey(config),
            );
          })
          .catch((err) => {
            if (err.response?.status === 412) {
              const current = Number(err.response.data?.metadata?.revision);
              Object.assign(this.conflict, {
                modal: true,
                name,
                from: revision,
                to: Number.isFinite(current) && current > 0 ? current : null,
                save: () => this.putConfig(name, config, null),
              });
              return;
            }
            this.showSaveError(err);
          });
      },

      // sends the refused save again, without If-Match, replacing the newer
      // config
      overwriteConflicted() {
        const save = this.conflict.save;
        this.conflict.modal = false;
        return save?.();
      },

      // opens the config as it is now, dropping the edits
      reloadConflicted() {
        this.conflict.modal = false;
        forgetConfig(this.editorConfig);
        this.loadConfig();
      },

      createConfig(lang) {
        let config = this.getConfigObj(lang);

        if (!config) {
          showError('there is no config to create');
          return;
        }

        configsApi
          .createConfig(config)
          .then(() => {
            this.resetEditor(`config ${configKey(config)} has been created`);
          })
          .catch((err) => this.showSaveError(err));
      },
      // Validation failures open the editor's error modal, which keeps the
      // config open for fixing; other failures (permissions, network) use the
      // usual error notification.
      showSaveError(err) {
        const validation = err.response?.data?.metadata?.validation;
        if (validation) {
          this.error.title = 'Validation Error';
          this.error.msg = validation;
          this.error.modal = true;
        } else {
          useErrorNotification(err);
        }
      },
    },
    computed: {
      // What the editor is for: "Edit Topology/web", or "New config".
      heading() {
        return this.mode == 'edit' && this.editorConfig
          ? `Edit ${this.editorConfig.kind}/${this.editorConfig.metadata.name}`
          : 'New config';
      },
      changed() {
        return this.config.str !== this.original;
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
  .hero.is-light.is-bold {
    background-image: linear-gradient(
      141deg,
      #564749 0%,
      #686868 71%,
      #7b716f 100%
    );
  }
  .hero.is-light {
    background-color: #686868;
    color: #fff;
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

  .config-editor__heading {
    color: whitesmoke;
    overflow-wrap: anywhere;
  }

  button#editor {
    margin-top: 125px;
    margin-right: 10px;
  }
</style>
