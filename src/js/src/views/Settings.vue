<template>
  <section>
    <div class="form-section">
      <form class="content" @submit.prevent="sendSettingsToServer">
        <h3>Appearance Settings</h3>
        <b-field
          label="Default theme"
          label-for="default-theme"
          message="Used only when this browser has no locally saved theme preference.">
          <div class="select is-fullwidth">
            <select
              id="default-theme"
              v-model="theme_settings.default_theme"
              :disabled="theme_settings.locked">
              <option value="system">System</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
          </div>
        </b-field>
        <b-message
          v-if="theme_settings.locked"
          type="is-warning"
          has-icon
          aria-live="polite">
          Default theme is controlled by the
          <code>phenix ui --default-theme</code> command-line flag.
        </b-message>

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
          Minimum length of password
          <b-numberinput
            aria-label="Minimum password length"
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
          Time (minutes) to log out users after idle for
          <b-numberinput
            aria-label="Idle timeout in minutes"
            v-model="settings_obj.timeout_settings.timeout_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5"
            class="custom-small">
          </b-numberinput>
        </b-field>
        <b-field>
          Display idle user logout with (minutes) left
          <b-numberinput
            aria-label="Idle logout warning in minutes"
            v-model="settings_obj.timeout_settings.warning_min"
            :disabled="!settings_obj.timeout_settings.enabled"
            :controls="false"
            step=".5"
            class="custom-small">
          </b-numberinput>
        </b-field>

        <h3>File Logging Settings</h3>
        <b-field>
          Max log file size (MiB)
          <b-numberinput
            aria-label="Maximum log file size in MiB"
            v-model="settings_obj.logging_settings.max_file_size"
            :controls="false"
            step="1"
            class="custom-small">
          </b-numberinput>
        </b-field>
        <b-field>
          Max number of file rotations (0 for infinite)
          <b-numberinput
            aria-label="Maximum number of log file rotations"
            v-model="settings_obj.logging_settings.max_file_rotations"
            :controls="false"
            step="1"
            class="custom-small"
            min="0">
          </b-numberinput>
        </b-field>
        <b-field>
          Max rotated log file age (0 for infinite)
          <b-numberinput
            aria-label="Maximum rotated log file age"
            v-model="settings_obj.logging_settings.max_file_age"
            :controls="false"
            step="1"
            class="custom-small"
            min="0">
          </b-numberinput>
        </b-field>

        <hr />
        <!-- <b-button @click="getSettings">Reset Form</b-button> -->
        <b-button native-type="submit">Save Changes</b-button>
      </form>
    </div>
  </section>
</template>
<script>
  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { useTheme } from '@/utils/theme.js';
  export default {
    async created() {
      this.getSettings();
    },

    methods: {
      async getSettings() {
        try {
          const [settingsResponse, themeResponse] = await Promise.all([
            axiosInstance.get('settings'),
            axiosInstance.get('settings/theme'),
          ]);
          this.settings_obj = settingsResponse.data;
          this.theme_settings = themeResponse.data;
        } catch (error) {
          useErrorNotification(error);
        }
      },
      async sendSettingsToServer() {
        try {
          const updates = [
            axiosInstance.post('settings', this.settings_obj, { timeout: 0 }),
          ];

          if (!this.theme_settings.locked) {
            updates.push(
              axiosInstance.put('settings/theme', {
                default_theme: this.theme_settings.default_theme,
              }),
            );
          }

          const responses = await Promise.all(updates);
          const themeResponse = responses[1];
          if (themeResponse) {
            this.theme_settings = themeResponse.data;
            useTheme().setDefaultTheme(themeResponse.data.default_theme);
          }

          this.$buefy.toast.open({
            message: 'Settings updated',
            type: 'is-success',
            duration: 3000,
          });
        } catch (error) {
          useErrorNotification(error);
        }
      },
    },
    data() {
      return {
        settings_obj: {
          password_settings: {
            number_req: false,
            symbol_req: false,
            lowercase_req: false,
            uppercase_req: false,
            min_length: 8,
          },
          timeout_settings: {
            enabled: false,
            timeout_min: 30,
            warning_min: 3,
          },
          logging_settings: {
            max_file_age: 365,
            max_file_rotations: 3,
            max_file_size: 100,
          },
        },
        theme_settings: {
          default_theme: 'system',
          locked: false,
        },
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
