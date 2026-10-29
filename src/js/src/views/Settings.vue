<template>
  <section>
    <div class="form-section">
      <p v-if="!loaded" class="has-text-grey">
        {{ loadingText('settings') }}
      </p>
      <form v-else class="content" @submit.prevent="sendSettingsToServer">
        <h3>Password Settings</h3>
        <b-field>
          <b-switch v-model="settings_obj.password_settings.lowercase_req">
            Require a lowercase letter
          </b-switch>
        </b-field>
        <b-field>
          <b-switch v-model="settings_obj.password_settings.uppercase_req">
            Require an uppercase letter
          </b-switch>
        </b-field>
        <b-field>
          <b-switch v-model="settings_obj.password_settings.number_req">
            Require a number
          </b-switch>
        </b-field>
        <b-field>
          <b-switch v-model="settings_obj.password_settings.symbol_req">
            Require a symbol
          </b-switch>
        </b-field>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-password-min-length"
            v-model="settings_obj.password_settings.min_length"
            min="4"
            max="32"
            :controls="false">
          </b-numberinput>
          <span id="settings-password-min-length"
            >Minimum length of password</span
          >
        </b-field>
        <h3>Timeout Settings</h3>
        <b-field>
          <b-switch v-model="settings_obj.timeout_settings.enabled">
            Log out users after period of inactivity
          </b-switch>
        </b-field>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-idle-timeout"
            v-model="settings_obj.timeout_settings.timeout_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5">
          </b-numberinput>
          <span id="settings-idle-timeout"
            >Time (minutes) to log out users after idle for</span
          >
        </b-field>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-idle-warning"
            v-model="settings_obj.timeout_settings.warning_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5">
          </b-numberinput>
          <span id="settings-idle-warning"
            >Display idle user logout with (minutes) left</span
          >
        </b-field>

        <h3>File Logging Settings</h3>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-log-max-size"
            v-model="settings_obj.logging_settings.max_file_size"
            :controls="false"
            step="1">
          </b-numberinput>
          <span id="settings-log-max-size">Max log file size (MiB)</span>
        </b-field>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-log-max-rotations"
            v-model="settings_obj.logging_settings.max_file_rotations"
            :controls="false"
            step="1"
            min="0">
          </b-numberinput>
          <span id="settings-log-max-rotations"
            >Max number of file rotations (0 for infinite)</span
          >
        </b-field>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-log-max-age"
            v-model="settings_obj.logging_settings.max_file_age"
            :controls="false"
            step="1"
            min="0">
          </b-numberinput>
          <span id="settings-log-max-age"
            >Max rotated log file age (0 for infinite)</span
          >
        </b-field>

        <hr />
        <b-button :disabled="!changed" @click="resetForm">Reset Form</b-button>
        <b-button native-type="submit" :disabled="!changed" :loading="saving">
          Save Changes
        </b-button>
      </form>
    </div>
  </section>
</template>
<script>
  import * as settingsApi from '@/api/settings.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { cachePage } from '@/utils/pageCache.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';

  const copy = (obj) => JSON.parse(JSON.stringify(obj));

  export default {
    created() {
      this.loader = createPageLoader({
        key: 'settings',
        fetch: pageFetchers.settings,
        apply: (data) => {
          // a reload must not wipe out edits that have not been saved yet
          const keepEdits = this.loaded && this.changed;
          this.saved = copy(data);
          if (!keepEdits) this.settings_obj = copy(data);
          this.loaded = true;
        },
      });
      this.loader.start();
    },

    beforeUnmount() {
      this.loader.stop();
    },

    computed: {
      changed() {
        return JSON.stringify(this.settings_obj) !== JSON.stringify(this.saved);
      },
    },

    methods: {
      loadingText,

      // back to the settings last loaded from or saved to the server
      resetForm() {
        this.settings_obj = copy(this.saved);
      },

      sendSettingsToServer() {
        // Enter submits the form too
        if (this.saving || !this.changed) return;
        const sent = copy(this.settings_obj);
        this.saving = true;
        settingsApi
          .saveSettings(sent)
          .then((_) => {
            this.saved = sent;
            cachePage('settings', copy(sent));
            this.$buefy.toast.open({
              message: 'Settings updated',
              type: 'is-success',
            });
          })
          .catch((err) => {
            useErrorNotification(err);
          })
          .finally(() => {
            this.saving = false;
          });
      },
    },
    data() {
      return {
        loaded: false,
        saving: false,
        saved: null,
        settings_obj: null,
      };
    },
  };
</script>
<style scoped>
  /* a number box sized to its value before its label, like the switches */
  .setting-number {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .setting-number :deep(.b-numberinput) {
    flex: none;
    width: 5.5em;
    margin-bottom: 0;
  }

  /* dark like the page; text is 11:1 and the border 4.2:1 on the form */
  .setting-number :deep(.input) {
    background-color: #333333;
    border-color: #b1b1b1;
    color: #f5f5f5;
  }

  .setting-number :deep(.input[disabled]) {
    border-color: #686868;
    color: #b1b1b1;
    box-shadow: none;
  }
</style>
