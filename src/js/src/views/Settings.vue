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
        <p id="settings-log-help" class="setting-section-help">
          These fields change runtime settings of phenix ui. A change applies at
          once, and phenix writes it to the config file.
        </p>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-log-max-size"
            aria-describedby="settings-log-max-size-help settings-log-help"
            v-model="settings_obj.logging_settings.max_file_size"
            :controls="false"
            step="1"
            min="0">
          </b-numberinput>
          <span id="settings-log-max-size">Max log file size (MiB)</span>
        </b-field>
        <p id="settings-log-max-size-help" class="help setting-help">
          Changes <code>log.system.max-size</code>. 0 is 100 MiB.
        </p>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-log-max-rotations"
            aria-describedby="settings-log-max-rotations-help settings-log-help"
            v-model="settings_obj.logging_settings.max_file_rotations"
            :controls="false"
            step="1"
            min="0">
          </b-numberinput>
          <span id="settings-log-max-rotations"
            >Max number of file rotations (0 for infinite)</span
          >
        </b-field>
        <p id="settings-log-max-rotations-help" class="help setting-help">
          Changes <code>log.system.max-backups</code>.
        </p>
        <b-field class="setting-number" :addons="false">
          <b-numberinput
            aria-labelledby="settings-log-max-age"
            aria-describedby="settings-log-max-age-help settings-log-help"
            v-model="settings_obj.logging_settings.max_file_age"
            :controls="false"
            step="1"
            min="0">
          </b-numberinput>
          <span id="settings-log-max-age"
            >Max rotated log file age (0 for infinite)</span
          >
        </b-field>
        <p id="settings-log-max-age-help" class="help setting-help">
          Changes <code>log.system.max-age</code>, in days.
        </p>

        <template v-if="settings_obj.minimega_settings">
          <h2 class="is-size-5">minimega</h2>
          <p id="settings-minimega-help" class="setting-section-help">
            Read-only connections caps how many read commands phēnix sends to
            minimega at the same time. phēnix keeps 2 of them for reads of the
            head node. A change applies at once, and phenix writes it to the
            config file.
          </p>
          <b-field
            class="setting-number"
            :addons="false"
            :type="readConnectionsError ? 'is-danger' : null">
            <b-numberinput
              aria-labelledby="settings-read-connections"
              :aria-invalid="readConnectionsError ? 'true' : null"
              :aria-describedby="
                readConnectionsError
                  ? 'settings-read-connections-error settings-read-connections-help'
                  : 'settings-read-connections-help settings-minimega-help'
              "
              v-model="settings_obj.minimega_settings.read_connections"
              :controls="false"
              :disabled="readConnectionsSetBy !== null"
              step="1"
              :min="READ_CONNECTIONS_MIN"
              :max="READ_CONNECTIONS_MAX">
            </b-numberinput>
            <span id="settings-read-connections">Read-only connections</span>
          </b-field>
          <p id="settings-read-connections-help" class="help setting-help">
            Changes <code>minimega.read-connections</code>, from
            {{ READ_CONNECTIONS_MIN }} to {{ READ_CONNECTIONS_MAX }}. The
            default is {{ READ_CONNECTIONS_DEFAULT }}.
            <span
              v-if="readConnectionsSetBy"
              id="settings-read-connections-source">
              {{ readConnectionsSetBy }} of phenix ui sets this value, so you
              cannot change it here.
            </span>
          </p>
          <p
            v-if="readConnectionsError"
            id="settings-read-connections-error"
            class="help is-danger setting-error">
            {{ readConnectionsError }}
          </p>
        </template>

        <hr />
        <p
          v-if="saveError"
          id="settings-save-error"
          class="help is-danger setting-save-error"
          role="alert">
          {{ saveError }}
        </p>
        <div class="buttons">
          <b-button :disabled="!changed" @click="resetForm"
            >Reset Form</b-button
          >
          <b-button
            native-type="submit"
            :disabled="!changed || !timeoutValid || !!readConnectionsError"
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
  import { errorBody } from '@/utils/operations.js';

  const copy = (obj) => JSON.parse(JSON.stringify(obj));

  // the statuses of a refusal whose message tells the user what to change
  const REFUSED = [400, 409];

  /**
   * The server's message for a save it refused (400 or 409), or null for
   * another failure.
   */
  export function refusalMessage(err) {
    const response = err?.response;
    if (!REFUSED.includes(response?.status)) return null;
    const message = errorBody(response.data)?.message;
    return typeof message === 'string' && message !== '' ? message : null;
  }

  // the range and default of minimega.read-connections, as phenix sets them
  export const READ_CONNECTIONS_MIN = 3;
  export const READ_CONNECTIONS_MAX = 64;
  export const READ_CONNECTIONS_DEFAULT = 16;

  /**
   * Why the read-only connections value is not valid, or null when it is: a
   * whole number from READ_CONNECTIONS_MIN to READ_CONNECTIONS_MAX.
   */
  export function readConnectionsError(value) {
    const n = Number(value);
    if (
      value === '' ||
      value === null ||
      !Number.isInteger(n) ||
      n < READ_CONNECTIONS_MIN ||
      n > READ_CONNECTIONS_MAX
    ) {
      return `Enter a whole number from ${READ_CONNECTIONS_MIN} to ${READ_CONNECTIONS_MAX}.`;
    }
    return null;
  }

  /**
   * What sets the read-only connections value outside the config file, from
   * its source: "A flag", "An environment variable", or null when the config
   * file or the default sets it, so the page can change it.
   */
  export function overriddenBy(source) {
    switch (source) {
      case 'flag':
        return 'A flag';
      case 'env':
        return 'An environment variable';
      default:
        return null;
    }
  }

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
      readConnectionsSetBy() {
        return overriddenBy(this.settings_obj?.minimega_settings?.source);
      },
      readConnectionsError() {
        const minimega = this.settings_obj?.minimega_settings;
        if (!minimega || this.readConnectionsSetBy) return null;
        return readConnectionsError(minimega.read_connections);
      },
    },

    methods: {
      loadingText,

      // back to the settings last loaded from or saved to the server
      resetForm() {
        this.settings_obj = copy(this.saved);
        this.saveError = null;
      },

      sendSettingsToServer() {
        // Enter submits the form too
        if (
          this.saving ||
          !this.changed ||
          !this.timeoutValid ||
          this.readConnectionsError
        )
          return;
        const sent = copy(this.settings_obj);
        this.saving = true;
        this.saveError = null;
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
            const refusal = refusalMessage(err);
            if (refusal) {
              this.saveError = `The server did not save the settings: ${refusal}`;
            } else {
              useErrorNotification(err);
            }
          })
          .finally(() => {
            this.saving = false;
          });
      },
    },
    data() {
      return {
        READ_CONNECTIONS_MIN,
        READ_CONNECTIONS_MAX,
        READ_CONNECTIONS_DEFAULT,
        loaded: false,
        saving: false,
        saveError: null,
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

  .setting-error,
  .setting-help {
    margin-top: -0.5rem;
    margin-bottom: 0.75rem;
  }

  .setting-section-help {
    margin-bottom: 0.75rem;
  }

  .setting-save-error {
    margin-bottom: 0.75rem;
  }
</style>
