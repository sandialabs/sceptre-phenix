<template>
  <div id="signup">
    <div class="signup-form">
      <b-field label="Username">
        <b-input type="text" :value="username" disabled></b-input>
      </b-field>
      <b-field label="First Name">
        <b-input type="text" v-model="firstName" autofocus></b-input>
      </b-field>
      <b-field label="Last Name">
        <b-input type="text" v-model="lastName"></b-input>
      </b-field>
      <br>
      <button class="button is-light" @click="onSubmit">Submit</button>
    </div>
  </div>
</template>

<script>
  export default {
    props: {
      username: {
        type: String,
        default: ''
      }
    },

    created () {
      if ( !this.username ) {
        this.$router.replace( '/' );
      }
    },

    methods: {
      onSubmit () {
        if ( !this.firstName ) {
          this.$buefy.toast.open({
            message: 'You must include a first name',
            type: 'is-warning',
            duration: 4000
          });
          
          return;
        }
        
        if ( !this.lastName ) {
          this.$buefy.toast.open({
            message: 'You must include a last name',
            type: 'is-warning',
            duration: 4000
          });
          
          return;
        }

        let params = {
          "username":   this.username,
          "first_name": this.firstName,
          "last_name":  this.lastName
        }
        
        this.$http.post('signup', params).then(
          response => { 
            return response.json().then(
              user => {
                this.$store.commit( 'LOGIN', { "loginResponse": user, "remember": false } );
              }
            )
          }, err => {
            this.errorNotification(err);
          }
        );
      }
    },

    data () {
      return {
        firstName: null,
        lastName:  null
      }
    }
  }
</script>

<!-- This styling is used for the sign up form. -->
<style scoped>
  .signup-form {
    width: 400px;
    margin: 30px auto;
    border: 1px solid #eee;
    padding: 20px;
    box-shadow: 0 2px 3px #ccc;
  }
  
  .signup-form :deep(.label) {
    color: whitesmoke;
  }
</style>
