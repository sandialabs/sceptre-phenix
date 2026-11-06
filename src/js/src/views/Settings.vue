<template>
  <section>
    <div class="form-section">
      <h1 class="title is-4">Settings</h1>
      <p v-if="!loaded">
        {{ loadingText('settings') }}
      </p>
      <form v-else class="content" @submit.prevent="sendSettingsToServer">
        <h2 class="is-size-5">Password Settings</h2>
        <b-field>
          <b-switch
            v-model="settings_obj.password_settings.lowercase_req"
            type="is-light">
            Require a lowercase letter
          </b-switch>
        </b-field>
        <b-field>
          <b-switch
            v-model="settings_obj.password_settings.uppercase_req"
            type="is-light">
            Require an uppercase letter
          </b-switch>
        </b-field>
        <b-field>
          <b-switch
            v-model="settings_obj.password_settings.number_req"
            type="is-light">
            Require a number
          </b-switch>
        </b-field>
        <b-field>
          <b-switch
            v-model="settings_obj.password_settings.symbol_req"
            type="is-light">
            Require a symbol
          </b-switch>
        </b-field>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-password-min-length"
            v-model="settings_obj.password_settings.min_length"
            min="8"
            max="32"
            :controls="false">
          </b-numberinput>
          <span id="settings-password-min-length"
            >Minimum length of password</span
          >
        </b-field>
        <h2 class="is-size-5">Timeout Settings</h2>
        <b-field>
          <b-switch
            v-model="settings_obj.timeout_settings.enabled"
            type="is-light">
            Log out users after period of inactivity
          </b-switch>
        </b-field>
        <b-field
          class="setting-number"
          :addons="false"
          :type="timeoutError ? 'is-danger' : null">
          <b-numberinput
            aria-labelledby="settings-idle-timeout"
            :aria-invalid="timeoutError ? 'true' : null"
            :aria-describedby="
              timeoutError ? 'settings-idle-timeout-error' : null
            "
            v-model="settings_obj.timeout_settings.timeout_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5"
            min="0.5">
          </b-numberinput>
          <span id="settings-idle-timeout"
            >Time (minutes) to log out users after idle for</span
          >
        </b-field>
        <p
          v-if="timeoutError"
          id="settings-idle-timeout-error"
          class="help is-danger setting-error">
          {{ timeoutError }}
        </p>
        <b-field
          class="setting-number"
          :addons="false"
          :type="warningError ? 'is-danger' : null">
          <b-numberinput
            aria-labelledby="settings-idle-warning"
            :aria-invalid="warningError ? 'true' : null"
            :aria-describedby="
              warningError ? 'settings-idle-warning-error' : null
            "
            v-model="settings_obj.timeout_settings.warning_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5"
            min="0">
          </b-numberinput>
          <span id="settings-idle-warning"
            >Display idle user logout with (minutes) left</span
          >
        </b-field>
        <p
          v-if="warningError"
          id="settings-idle-warning-error"
          class="help is-danger setting-error">
          {{ warningError }}
        </p>

        <h2 class="is-size-5">File Logging Settings</h2>
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
        <div class="buttons">
          <b-button :disabled="!changed" @click="resetForm"
            >Reset Form</b-button
          >
          <b-button
            native-type="submit"
            :disabled="!changed || !timeoutValid"
            :loading="saving">
            Save Changes
          </b-button>
        </div>
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

  /**
   * Why the idle timeout settings are not valid, or null for each field that
   * is valid. The timeout is at least 0.5 minutes and the warning is shorter.
   */
  export function timeoutErrors({ enabled, timeout_min, warning_min }) {
    const errors = { timeout: null, warning: null };
    if (!enabled) {
      return errors;
    }

    const timeout = Number(timeout_min);
    const warning = Number(warning_min);

    if (!(timeout >= 0.5)) {
      errors.timeout = 'Enter an idle time of 0.5 minutes or more.';
    }
    if (!(warning >= 0)) {
      errors.warning = 'Enter a warning time of 0 minutes or more.';
    } else if (timeout >= 0.5 && warning >= timeout) {
      errors.warning = `Enter a warning time of less than the idle time (${timeout} minutes).`;
    }

    return errors;
  }

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
      timeoutErrors() {
        return timeoutErrors(this.settings_obj?.timeout_settings ?? {});
      },
      timeoutError() {
        return this.timeoutErrors.timeout;
      },
      warningError() {
        return this.timeoutErrors.warning;
      },
      timeoutValid() {
        return !this.timeoutError && !this.warningError;
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
        if (this.saving || !this.changed || !this.timeoutValid) return;
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

  /* the red border is 4.2:1 on the form */
  .setting-number :deep(.input.is-danger) {
    border-color: #ff8fa3;
  }

  .setting-error {
    margin-top: -0.5rem;
    margin-bottom: 0.75rem;
  }
</style>
