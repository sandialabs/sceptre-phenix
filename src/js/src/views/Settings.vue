<template>
  <section>
    <div class="form-section">
      <form class="content">
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
            v-model="settings_obj.logging_settings.max_file_size"
            :controls="false"
            step="1"
            class="custom-small">
          </b-numberinput>
        </b-field>
        <b-field>
          Max number of file rotations (0 for infinite)
          <b-numberinput
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
            v-model="settings_obj.logging_settings.max_file_age"
            :controls="false"
            step="1"
            class="custom-small"
            min="0">
          </b-numberinput>
        </b-field>

        <hr />
        <!-- <b-button @click="getSettings">Reset Form</b-button> -->
        <b-button @click="sendSettingsToServer">Save Changes</b-button>
      </form>
    </div>
  </section>
</template>
<script>
  import axiosInstance from '@/utils/axios.js';
  export default {
    async created() {
      this.getSettings();
    },

    methods: {
      getSettings() {
        console.log('getting settings');
        axiosInstance.get('settings').then((response) => {
          const state = response.data;
          console.log(state);
          this.settings_obj = state;
        });
      },
      printSettings() {
        console.log(this.settings_obj);
      },
      sendSettingsToServer() {
        axiosInstance
          .post('settings', this.settings_obj, { timeout: 0 })
          .then((resp) => {
            console.log(resp);
            this.$buefy.toast.open({
              message: 'Settings updated',
              type: 'is-success',
              duration: 3000,
            });
          })
          .catch((err) => {
            this.errorNotification(err);
          });
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
