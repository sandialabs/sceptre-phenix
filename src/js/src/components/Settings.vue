<template>
  <section>
    <hr>
    <div class="settings-section">
      <form class="content">
        <h3>Password Settings</h3>
        <b-field>
          <b-switch v-model="settings_obj.password_settings.lowercase_req" >
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
          <b-numberinput v-model="settings_obj.password_settings.min_length"
            class="custom-small"
            min="8"
            max="32"
            :controls="false">
          </b-numberinput>
        </b-field>
        <hr>
        <b-button @click="sendSettingsToServer">Save Changes</b-button>
      </form>
    </div>
  </section>
</template>
<script>
export default {

  beforeDestroy () {
  },
  
  async created () {
    this.getSettings();
  },

  methods: {
    getSettings(){
      this.$http.get('settings').then(
        response => {
          response.json().then( state => {
            // console.log(state)
            this.settings_obj = state
          })
        }
      )
    },
    printSettings(){
      console.log(this.settings_obj)
    },
    sendSettingsToServer(){
      this.$http.post(
        'settings', this.settings_obj, { timeout: 0 }
      ).then(
          resp => {
            // console.log(resp);
            this.$buefy.toast.open({
              message: "Settings updated",
              type: 'is-success',
              duration: 3000
            });
            
          }, err => {
            this.errorNotification(err);
          }
        )
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
      },
    };
  },
}
</script>
<style scoped>
.settings-section {
  padding: 10px;
  margin: auto;
  max-width: 800px;
}
.custom-small {
  width: 25%;
  min-width: 150px;
}
</style>
