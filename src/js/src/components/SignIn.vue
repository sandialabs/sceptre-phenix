<!-- 
This component is used to initially sign in a user to the phēnixweb. 
It requires a valid email (user ID) and password.
 -->

<template>
  <div id="signin">
    <b-modal :active.sync="signUpModal" has-modal-card>
      <div class="modal-card" style="width: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">Create a New Account</p>
        </header>
        <section class="modal-card-body">
          <b-field label="User Name" 
            :type="{ 'is-danger' : userExists }" 
            :message="{ 'User already exists' : userExists }">
            <b-input type="text" v-model="newUser.username" minlength="4" maxlength="32" autofocus></b-input>
          </b-field>
          <b-field label="First Name">
            <b-input type="text" v-model="newUser.first_name"></b-input>
          </b-field>
          <b-field label="Last Name">
            <b-input type="text" v-model="newUser.last_name"></b-input>
          </b-field>
          <b-field label="Password">
            <b-input type="password" minlength="8" maxlength="32" v-model="newUser.password"></b-input>
          </b-field>
          <b-field label="Confirm Password">
            <b-input type="password" minlength="8" maxlength="32" v-model="newUser.confirmPassword" @keyup.native.enter="create"></b-input>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="create">Create User</button>
        </footer>
      </div>
    </b-modal>
    <div class="signin-form">
      <b-field label="Username">
        <b-input type="text" v-model="username" autofocus></b-input>
      </b-field>
      <b-field label="Password">
        <b-input type="password" v-model="password" @keyup.native.enter="onSubmit"></b-input>
      </b-field>
      <b-checkbox type="is-light" size="is-small" v-model="rememberMe" native-value=false>Remember me</b-checkbox>
      <br>
      <button class="button is-light" @click="onSubmit">Submit</button>
      <button class="button is-pulled-right is-small is-text" @click="signUpModal = true">Create Account</button>
    </div>
  </div>
</template>

<script>
  export default {
    //  this method is called when the Submit button is pressed (or 
    //  return key is) executed. It will check that an email address 
    //  is used, and/or a password. It does not check if they are valid. 
    methods: {
      onSubmit () {
        if ( !this.username ) {
          this.$buefy.toast.open({
            message: 'You must include an email address',
            type: 'is-warning',
            duration: 4000
          });
          return {}
        }
        
        if ( !this.password ) {
          this.$buefy.toast.open({
            message: 'You must include a password',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }
        
        this.$http.post(
          'login', {'user': this.username, 'pass': this.password}
        ).then(
          response => {
            response.json().then(
              user => {
                this.$store.commit( 'LOGIN', { "loginResponse": user, "remember": this.rememberMe } );
              }
            )
          }, response => {
            if ( response.status == 401 ) {
              this.$buefy.toast.open({
                message: 'The username and/or password is incorrect',
                type: 'is-warning',
                duration: 4000
              });
              
              this.username = null;
              this.password = null;
            } else if ( response.status == 0 ) {
              this.$buefy.toast.open({
                message: 'The data server is not available.',
                type: 'is-danger',
                duration: 6000
              });
              
              this.username = null;
              this.password = null;
            } else {
              this.$buefy.toast.open({
                message: 'Getting the user information failed.',
                type: 'is-danger',
                duration: 4000
              });
              
              this.username = null;
              this.password = null;
            }
          }
        );     
      },
      
      create () {
        if ( !this.newUser.username ) {
          this.$buefy.toast.open({
            message: 'You must include an username',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }
        
        if ( !this.newUser.first_name ) {
          this.$buefy.toast.open({
            message: 'You must include a first name',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }
        
        if ( !this.newUser.last_name ) {
          this.$buefy.toast.open({
            message: 'You must include a last name',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }
        
        if ( !this.newUser.password ) {
          this.$buefy.toast.open({
            message: 'You must include a password',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }
        
        if ( !this.newUser.confirmPassword ) {
          this.$buefy.toast.open({
            message: 'You must include a password confirmation',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }
        
        if ( this.newUser.password != this.newUser.confirmPassword ) {
          this.$buefy.toast.open({
            message: 'Your passwords do not match',
            type: 'is-warning',
            duration: 4000
          });
          
          return {}
        }       
        
        this.$http.post(
          'signup', {
            "username": this.newUser.username,
            "password": this.newUser.password,
            "first_name": this.newUser.first_name,
            "last_name": this.newUser.last_name
          }
        ).then(
          response => { 
            return response.json().then(
              user => {
                this.$store.commit( 'SIGN_UP', user );
                this.newUser = {
                  username: null,
                  password: null,
                  confirmPassword: null,
                  first_name: null,
                  last_name: null,
                }
              }
            )
          }, err => {
            this.errorNotification(err);
            this.newUser.confirmPassword = null
          }
        );

        this.signUpModal = false;
      }
    },

    data () {
      return {
        signUpModal: false,
        username: null,
        password: null,
        rememberMe: false,
        userExists: false,
        newUser: {
          username: null,
          password: null,
          confirmPassword: null,
          first_name: null,
          last_name: null,
        },
      }
    }
  }
</script>

<!-- This styling is used for the sign in form. -->
<style scoped>
  .signin-form {
    width: 400px;
    margin: 30px auto;
    border: 1px solid #eee;
    padding: 20px;
    box-shadow: 0 2px 3px #ccc;
  }
  
  .signin-form >>> .label {
    color: whitesmoke;
  }

  label.checkbox:hover {
    color: whitesmoke;
  }
</style>
