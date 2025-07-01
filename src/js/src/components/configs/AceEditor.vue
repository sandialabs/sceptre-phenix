<template>
  <div ref="editor" class="editor"></div>
  <b-loading
    :is-full-page="false"
    v-model="isLoading"></b-loading>
</template>

<script>
  // import ace from 'ace-builds/src-noconflict/ace';
  //
  // import themeDraculaUrl from 'ace-builds/src-noconflict/theme-dracula?url';
  // ace.config.setModuleUrl('ace/theme/dracula', themeDraculaUrl);
  //
  // import keybindingVim from 'ace-builds/src-noconflict/keybinding-vim?url';
  // ace.config.setModuleUrl('ace/keybinding/vim', keybindingVim);
  // import 'ace-builds/src-noconflict/keybinding-vim';
  //
  // import 'ace-builds/src-noconflict/ext-language_tools';
  // ace.require('ace/ext/language_tools');
  //
  // import modeJsonUrl from 'ace-builds/src-noconflict/mode-json?url';
  // ace.config.setModuleUrl('ace/mode/json', modeJsonUrl);
  //
  // import modeYamlUrl from 'ace-builds/src-noconflict/mode-yaml?url';
  // ace.config.setModuleUrl('ace/mode/yaml', modeYamlUrl);

  export default {
    props: {
      value: {
        type: String,
        default: '',
        required: true,
      },
      lang: {
        type: String,
        default: 'json',
      },
      vim: {
        type: Boolean,
        default: false,
      },
    },
    emits: ['update:value', 'save', 'reset'],
    data() {
      return {
        editor: null,
        ace: null,
        isLoading: false,
      };
    },
    async mounted() {

      this.isLoading = true;

      const aceModule = await import('ace-builds/src-noconflict/ace');
      this.ace = aceModule.default;
      this.ace.config.set('basePath', 'ace-builds/src-noconflict/');

      const themeDraculaUrl = await import('ace-builds/src-noconflict/theme-dracula?url');
      this.ace.config.setModuleUrl('ace/theme/dracula', themeDraculaUrl.default);

      const keybindingVimUrl = await import('ace-builds/src-noconflict/keybinding-vim?url');
      this.ace.config.setModuleUrl('ace/keybinding/vim', keybindingVimUrl.default);
      await import('ace-builds/src-noconflict/keybinding-vim');

      await import('ace-builds/src-noconflict/ext-language_tools');
      this.ace.require('ace/ext/language_tools');

      const modeJsonUrl = await import('ace-builds/src-noconflict/mode-json?url');
      this.ace.config.setModuleUrl('ace/mode/json', modeJsonUrl.default);

      const modeYamlUrl = await import('ace-builds/src-noconflict/mode-yaml?url');
      this.ace.config.setModuleUrl('ace/mode/yaml', modeYamlUrl.default);


      this.editor = this.ace.edit(this.$refs.editor, {
        theme: 'ace/theme/dracula',
        mode: 'ace/mode/' + this.lang,
        useWorker: false,
        tabSize: 2,
      });
      if (this.vim) {
        this.editor.setKeyboardHandler('ace/keyboard/vim');
      }

      this.editor.setValue(this.value, 1); // Initialize with `value` prop

      // Emit input event to update v-model binding
      this.editor.getSession().on('change', () => {
        this.$emit('update:value', this.editor.getValue());
      });

      this.loadVimCommands();
      this.isLoading = false
    },
    watch: {
      value(newValue) {
        if (this.editor && this.editor.getValue() !== newValue) {
          this.editor.setValue(newValue, 1); // 1 is to move cursor to the start
        }
      },
      lang(newLang) {
        if (this.editor) {
          this.editor.session.setMode(`ace/mode/${newLang}`);
        }
      },
      vim(vimActive) {
        if (!this.editor) {
          return
        }
        if (vimActive) {
          this.editor.setKeyboardHandler('ace/keyboard/vim');
        } else {
          this.editor.setKeyboardHandler('');
        }
      },
    },
    // setup() {
    //   this.ace.config.set('basePath', 'ace-builds/src-noconflict/');
    // },
    beforeDestroy() {
      this.editor.destroy();
    },
    methods: {
      loadVimCommands() {
        this.ace.config.loadModule('ace/keybinding/vim', (module) => {
          const VimApi = this.ace.require('ace/keyboard/vim').CodeMirror.Vim;

          VimApi.defineEx('wq', null, () => {
            this.$emit('save');
          });

          VimApi.defineEx('q', null, () => {
            this.$emit('reset');
          });
        });
      },
    },
  };
</script>

<style scoped>
  .editor {
    height: 500px;
    width: 100%;
  }
</style>
