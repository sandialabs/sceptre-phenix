<template>
  <div ref="editor" class="editor"></div>
  <b-loading :is-full-page="false" v-model="isLoading"></b-loading>
</template>

<script>
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
        disposed: false,
      };
    },
    async mounted() {
      this.isLoading = true;

      const aceModule = await import('ace-builds/src-noconflict/ace');
      this.ace = aceModule.default;
      this.ace.config.set('basePath', 'ace-builds/src-noconflict/');

      const themeDraculaUrl = await import(
        'ace-builds/src-noconflict/theme-dracula?url'
      );
      this.ace.config.setModuleUrl(
        'ace/theme/dracula',
        themeDraculaUrl.default,
      );

      const keybindingVimUrl = await import(
        'ace-builds/src-noconflict/keybinding-vim?url'
      );
      this.ace.config.setModuleUrl(
        'ace/keybinding/vim',
        keybindingVimUrl.default,
      );
      await import('ace-builds/src-noconflict/keybinding-vim');

      await import('ace-builds/src-noconflict/ext-language_tools');
      this.ace.require('ace/ext/language_tools');

      const modeJsonUrl = await import(
        'ace-builds/src-noconflict/mode-json?url'
      );
      this.ace.config.setModuleUrl('ace/mode/json', modeJsonUrl.default);

      const modeYamlUrl = await import(
        'ace-builds/src-noconflict/mode-yaml?url'
      );
      this.ace.config.setModuleUrl('ace/mode/yaml', modeYamlUrl.default);

      if (this.disposed || !this.$refs.editor) {
        return;
      }

      // Tab reaches the editor as one stop: Enter starts typing and Escape
      // stops, so Tab moves on and does not indent (WCAG 2.1.2).
      this.editor = this.ace.edit(this.$refs.editor, {
        theme: 'ace/theme/dracula',
        mode: 'ace/mode/' + this.lang,
        useWorker: false,
        tabSize: 2,
        enableKeyboardAccessibility: true,
      });
      // Chromium puts scrollable divs in the tab order, which adds stops.
      for (const bar of this.$refs.editor.querySelectorAll('.ace_scrollbar')) {
        bar.setAttribute('tabindex', '-1');
      }
      if (this.vim) {
        this.editor.setKeyboardHandler('ace/keyboard/vim');
      }

      this.editor.setValue(this.value, 1); // Initialize with `value` prop

      // Emit input event to update v-model binding
      this.editor.getSession().on('change', () => {
        this.$emit('update:value', this.editor.getValue());
      });

      this.loadVimCommands();
      this.isLoading = false;
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
          return;
        }
        if (vimActive) {
          this.editor.setKeyboardHandler('ace/keyboard/vim');
        } else {
          this.editor.setKeyboardHandler('');
        }
      },
    },
    beforeUnmount() {
      this.disposed = true;
      this.editor?.destroy();
      this.editor = null;
    },
    methods: {
      loadVimCommands() {
        this.ace.config.loadModule('ace/keybinding/vim', (_) => {
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

  /* The stock Dracula theme, with lighter text of the same hues and a darker
     current line, so text is 7:1 or more on #282a36 and #383a4a (WCAG 1.4.6). */
  .editor.ace-dracula :deep(.ace_gutter) {
    color: #cdcdcf;
  }

  .editor.ace-dracula :deep(.ace_marker-layer .ace_active-line),
  .editor.ace-dracula :deep(.ace_gutter-active-line) {
    background-color: #383a4a;
  }

  .editor.ace-dracula :deep(.ace_comment) {
    color: #c8cddf;
  }

  .editor.ace-dracula :deep(.ace_keyword),
  .editor.ace-dracula :deep(.ace_storage),
  .editor.ace-dracula :deep(.ace_entity.ace_name.ace_tag),
  .editor.ace-dracula :deep(.ace_constant.ace_character.ace_escape) {
    color: #ffb9e1;
  }

  .editor.ace-dracula :deep(.ace_constant.ace_language),
  .editor.ace-dracula :deep(.ace_constant.ace_numeric),
  .editor.ace-dracula :deep(.ace_constant.ace_character),
  .editor.ace-dracula :deep(.ace_constant.ace_other) {
    color: #dbc3fc;
  }

  .editor.ace-dracula :deep(.ace_support.ace_class),
  .editor.ace-dracula :deep(.ace_support.ace_type) {
    color: #72dcf0;
  }

  .editor.ace-dracula :deep(.ace_variable.ace_parameter) {
    color: #ffc17e;
  }

  /* Light text on darker fills of the same hues. */
  .editor.ace-dracula :deep(.ace_invalid) {
    background-color: #a4005e;
  }

  .editor.ace-dracula :deep(.ace_invalid.ace_deprecated) {
    background-color: #670de9;
  }
</style>
