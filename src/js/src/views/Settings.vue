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

        <h3>Runtime Settings</h3>
        <div
          v-for="group in runtimeSettingGroups"
          :key="group.name"
          class="runtime-section">
          <h4>{{ group.name }}</h4>
          <b-field
            v-for="setting in group.settings"
            :key="setting.key"
            :label="setting.key"
            :message="runtimeSettingMessage(setting)">
            <div class="runtime-setting">
              <b-switch
                v-if="setting.type === 'bool'"
                v-model="runtime_values[setting.key]">
              </b-switch>
              <b-numberinput
                v-else-if="isNumberRuntimeSetting(setting)"
                v-model="runtime_values[setting.key]"
                :controls="false"
                :step="setting.type === 'float64' ? '.5' : '1'"
                class="custom-small">
              </b-numberinput>
              <b-input
                v-else
                v-model="runtime_values[setting.key]"
                :type="setting.sensitive ? 'password' : 'text'"
                :password-reveal="setting.sensitive"
                expanded>
              </b-input>
              <b-button
                size="is-small"
                type="is-primary"
                native-type="button"
                @click.prevent="saveRuntimeSetting(setting)">
                Save
              </b-button>
              <b-button
                size="is-small"
                native-type="button"
                @click.prevent="unsetRuntimeSetting(setting)">
                Reset
              </b-button>
            </div>
          </b-field>
        </div>

        <h3>Environment Variables</h3>
        <b-table
          :data="settings_obj.runtime_settings.environment"
          :paginated="settings_obj.runtime_settings.environment.length > 10"
          :per-page="10"
          striped
          hoverable>
          <b-table-column field="name" label="Variable" v-slot="props">
            {{ props.row.name }}
          </b-table-column>
          <b-table-column field="value" label="Value" v-slot="props">
            <span :class="{ 'has-text-grey': !props.row.set }">
              {{ props.row.set ? props.row.value : '(not set)' }}
            </span>
          </b-table-column>
        </b-table>

        <hr />
        <!-- <b-button @click="getSettings">Reset Form</b-button> -->
        <b-button native-type="button" @click="sendSettingsToServer">
          Save Changes
        </b-button>
      </form>
    </div>
  </section>
</template>
<script>
  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  export default {
    async created() {
      this.getSettings();
    },

    methods: {
      getSettings() {
        console.log('getting settings');
        axiosInstance
          .get('settings')
          .then((response) => {
            const state = response.data;
            console.log(state);
            this.settings_obj = state;
            this.setRuntimeValues();
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
      printSettings() {
        console.log(this.settings_obj);
      },
      setRuntimeValues() {
        this.runtime_values = {};
        this.settings_obj.runtime_settings.settings.forEach((setting) => {
          if (Array.isArray(setting.value)) {
            this.runtime_values[setting.key] = setting.value.join(',');
          } else {
            this.runtime_values[setting.key] = setting.value;
          }
        });
      },
      runtimeSettingGroupName(key) {
        if (key.startsWith('ui.')) {
          return 'UI';
        }
        if (key.startsWith('log.')) {
          return 'Logging';
        }
        if (key.startsWith('base-dir.')) {
          return 'Base Directories';
        }
        if (key.startsWith('store.')) {
          return 'Store';
        }
        return 'General';
      },
      runtimeSettingMessage(setting) {
        const parts = [
          setting.description,
          `Env: ${setting.env_var}`,
          `Source: ${setting.source}`,
        ];
        if (setting.restart_required) {
          parts.push('Restart required');
        }
        if (setting.type === '[]string') {
          parts.push('Use comma-separated values');
        }
        return parts.filter(Boolean).join(' | ');
      },
      isNumberRuntimeSetting(setting) {
        return ['int', 'float64'].includes(setting.type);
      },
      normalizeRuntimeValue(setting) {
        const value = this.runtime_values[setting.key];
        if (setting.type === '[]string') {
          if (typeof value !== 'string') {
            return value;
          }
          return value
            .split(',')
            .map((entry) => entry.trim())
            .filter((entry) => entry.length > 0);
        }
        return value;
      },
      saveRuntimeSetting(setting) {
        axiosInstance
          .post(
            'settings/runtime',
            {
              key: setting.key,
              value: this.normalizeRuntimeValue(setting),
            },
            { timeout: 0 },
          )
          .then(() => {
            this.$buefy.toast.open({
              message: `${setting.key} updated`,
              type: 'is-success',
              duration: 3000,
            });
            this.getSettings();
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
      unsetRuntimeSetting(setting) {
        axiosInstance
          .delete('settings/runtime', {
            params: { key: setting.key },
            timeout: 0,
          })
          .then(() => {
            this.$buefy.toast.open({
              message: `${setting.key} reset`,
              type: 'is-success',
              duration: 3000,
            });
            this.getSettings();
          })
          .catch((err) => {
            useErrorNotification(err);
          });
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
            useErrorNotification(err);
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
          runtime_settings: {
            settings: [],
            environment: [],
          },
        },
        runtime_values: {},
      };
    },
    computed: {
      runtimeSettingGroups() {
        const groups = {};
        this.settings_obj.runtime_settings.settings.forEach((setting) => {
          const name = this.runtimeSettingGroupName(setting.key);
          if (!groups[name]) {
            groups[name] = [];
          }
          groups[name].push(setting);
        });
        return Object.keys(groups)
          .sort()
          .map((name) => ({
            name,
            settings: groups[name],
          }));
      },
    },
  };
</script>
<style scoped>
  .custom-small {
    width: 25%;
    min-width: 150px;
  }
  .runtime-section {
    margin-bottom: 1.5rem;
  }
  .runtime-setting {
    align-items: center;
    display: flex;
    gap: 0.5rem;
    width: 100%;
  }
</style>
