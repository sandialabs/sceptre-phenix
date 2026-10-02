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
        <b-field>
          <span id="settings-password-min-length"
            >Minimum length of password</span
          >
          <b-numberinput
            aria-labelledby="settings-password-min-length"
            v-model="settings_obj.password_settings.min_length"
            class="custom-small"
            min="4"
            max="32"
            :controls="false">
          </b-numberinput>
        </b-field>
        <h3>Timeout Settings</h3>
        <b-field>
          <b-switch v-model="settings_obj.timeout_settings.enabled">
            Log out users after period of inactivity
          </b-switch>
        </b-field>
        <b-field>
          <span id="settings-idle-timeout"
            >Time (minutes) to log out users after idle for</span
          >
          <b-numberinput
            aria-labelledby="settings-idle-timeout"
            v-model="settings_obj.timeout_settings.timeout_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5"
            class="custom-small">
          </b-numberinput>
        </b-field>
        <b-field>
          <span id="settings-idle-warning"
            >Display idle user logout with (minutes) left</span
          >
          <b-numberinput
            aria-labelledby="settings-idle-warning"
            v-model="settings_obj.timeout_settings.warning_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5"
            class="custom-small">
          </b-numberinput>
        </b-field>

        <h3>File Logging Settings</h3>
        <b-field>
          <span id="settings-log-max-size">Max log file size (MiB)</span>
          <b-numberinput
            aria-labelledby="settings-log-max-size"
            v-model="settings_obj.logging_settings.max_file_size"
            :controls="false"
            step="1"
            class="custom-small">
          </b-numberinput>
        </b-field>
        <b-field>
          <span id="settings-log-max-rotations"
            >Max number of file rotations (0 for infinite)</span
          >
          <b-numberinput
            aria-labelledby="settings-log-max-rotations"
            v-model="settings_obj.logging_settings.max_file_rotations"
            :controls="false"
            step="1"
            class="custom-small"
            min="0">
          </b-numberinput>
        </b-field>
        <b-field>
          <span id="settings-log-max-age"
            >Max rotated log file age (0 for infinite)</span
          >
          <b-numberinput
            aria-labelledby="settings-log-max-age"
            v-model="settings_obj.logging_settings.max_file_age"
            :controls="false"
            step="1"
            class="custom-small"
            min="0">
          </b-numberinput>
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
  import axiosInstance from '@/utils/axios.js';
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
        axiosInstance
          .post('settings', sent, { timeout: 0 })
          .then((_) => {
            this.saved = sent;
            cachePage('settings', copy(sent));
            this.$buefy.toast.open({
              message: 'Settings updated',
              type: 'is-success',
              duration: 3000,
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
  .custom-small {
    width: 25%;
    min-width: 150px;
  }
</style>
