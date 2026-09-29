<!-- 
This component is used to initially sign in a user to the phēnixweb. 
It requires a valid username and password.
 -->

<template>
  <div id="signin">
    <b-modal
      v-model="signUpModal"
      has-modal-card
      aria-role="dialog"
      aria-modal
      aria-label="Create a New Account"
      close-button-aria-label="Close"
      :auto-focus="false"
      :destroy-on-hide="false">
      <!-- Kept in the page once closed: a dialog reopened as it was being
           removed stayed open but invisible, over the page. A new form each
           time it opens leaves no value or message from before. -->
      <div class="modal-card" :key="signUpForm">
        <header class="modal-card-head">
          <p class="modal-card-title">Create a New Account</p>
        </header>
        <section class="modal-card-body">
          <b-field
            label="User Name"
            label-for="signup-username"
            :type="{ 'is-danger': userExists }"
            :message="{ 'User already exists': userExists }">
            <b-input
              id="signup-username"
              :compat-fallthrough="false"
              ref="signupUsername"
              type="text"
              autocomplete="username"
              v-model="signUp.username"
              minlength="4"
              maxlength="32"></b-input>
          </b-field>
          <b-field label="First Name" label-for="signup-first-name">
            <b-input
              id="signup-first-name"
              :compat-fallthrough="false"
              type="text"
              autocomplete="given-name"
              v-model="signUp.first_name"></b-input>
          </b-field>
          <b-field label="Last Name" label-for="signup-last-name">
            <b-input
              id="signup-last-name"
              :compat-fallthrough="false"
              type="text"
              autocomplete="family-name"
              v-model="signUp.last_name"></b-input>
          </b-field>
          <b-field label="Password" label-for="signup-password">
            <b-input
              id="signup-password"
              :compat-fallthrough="false"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              v-model="signUp.password"></b-input>
          </b-field>
          <b-field label="Confirm Password" label-for="signup-confirm-password">
            <b-input
              id="signup-confirm-password"
              :compat-fallthrough="false"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              v-model="signUp.confirmPassword"
              @keyup.enter="create"></b-input>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button" @click="create">Create User</button>
        </footer>
      </div>
    </b-modal>
    <div class="signin-form">
      <b-field label="Username" label-for="signin-username">
        <b-input
          id="signin-username"
          :compat-fallthrough="false"
          ref="username"
          type="text"
          autocomplete="username"
          v-model="username"></b-input>
      </b-field>
      <b-field label="Password" label-for="signin-password">
        <b-input
          id="signin-password"
          :compat-fallthrough="false"
          type="password"
          autocomplete="current-password"
          v-model="password"
          @keyup.enter="onSubmit"></b-input>
      </b-field>
      <b-checkbox
        type="is-light"
        size="is-small"
        v-model="rememberMe"
        native-value="false"
        >Remember me</b-checkbox
      >
      <br />
      <button class="button" @click="onSubmit">Submit</button>
      <button
        ref="createAccount"
        class="button is-pulled-right is-small is-text"
        @click="signUpModal = true">
        Create Account
      </button>
    </div>
  </div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import { usePhenixStore } from '@/store.js';
  import { useErrorNotification } from '@/utils/errorNotif';

  // The Create Account dialog's fields, apart from the sign-in form's.
  function blankSignUp() {
    return {
      username: null,
      first_name: null,
      last_name: null,
      password: null,
      confirmPassword: null,
    };
  }

  export default {
    //  this method is called when the Submit button is pressed (or
    //  return key is) executed. It will check that a username
    //  is used, and/or a password. It does not check if they are valid.
    methods: {
      onSubmit() {
        if (!this.username) {
          this.$buefy.toast.open({
            message: 'You must include a username',
            type: 'is-warning',
            duration: 4000,
          });
          return {};
        }

        if (!this.password) {
          this.$buefy.toast.open({
            message: 'You must include a password',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        axiosInstance
          .post('login', { user: this.username, pass: this.password })
          .then(
            (response) => {
              const store = usePhenixStore();
              store.login(response.data, this.rememberMe);
            },
            (response) => {
              if (response.status == 401) {
                this.$buefy.toast.open({
                  message: 'The username and/or password is incorrect',
                  type: 'is-warning',
                  duration: 4000,
                });

                this.username = null;
                this.password = null;
              } else if (response.status == 0) {
                this.$buefy.toast.open({
                  message: 'The data server is not available.',
                  type: 'is-danger',
                  duration: 6000,
                });

                this.username = null;
                this.password = null;
              } else {
                this.$buefy.toast.open({
                  message: 'Getting the user information failed.',
                  type: 'is-danger',
                  duration: 4000,
                });

                this.username = null;
                this.password = null;
              }
            },
          );
      },

      create() {
        if (!this.signUp.username) {
          this.$buefy.toast.open({
            message: 'You must include an username',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        if (!this.signUp.first_name) {
          this.$buefy.toast.open({
            message: 'You must include a first name',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        if (!this.signUp.last_name) {
          this.$buefy.toast.open({
            message: 'You must include a last name',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        if (!this.signUp.password) {
          this.$buefy.toast.open({
            message: 'You must include a password',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        if (!this.signUp.confirmPassword) {
          this.$buefy.toast.open({
            message: 'You must include a password confirmation',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        if (this.signUp.password != this.signUp.confirmPassword) {
          this.$buefy.toast.open({
            message: 'Your passwords do not match',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        axiosInstance
          .post('signup', {
            username: this.signUp.username,
            password: this.signUp.password,
            first_name: this.signUp.first_name,
            last_name: this.signUp.last_name,
          })
          .then((_) => {
            // on success, sign user in
            this.username = this.signUp.username;
            this.password = this.signUp.password;
            this.onSubmit();
            this.signUpModal = false;
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
    },

    // The autofocus attribute works only on a full page load, not after an
    // in-app logout.
    mounted() {
      this.$refs.username.focus();
    },

    watch: {
      // The dialog opens empty, even when it opens again before it has
      // finished closing. Focus goes to its first field as it opens, and
      // back to the button that opened it as it closes.
      signUpModal(open) {
        if (open) {
          this.signUp = blankSignUp();
          this.userExists = false;
          this.signUpForm++;
        }
        this.$nextTick(() =>
          open
            ? this.$refs.signupUsername?.focus()
            : this.$refs.createAccount?.focus(),
        );
      },
    },

    data() {
      return {
        signUpModal: false,
        // The key of the dialog's form, changed each time it opens.
        signUpForm: 0,
        signUp: blankSignUp(),
        username: null,
        password: null,
        rememberMe: false,
        userExists: false,
      };
    },
  };
</script>

<!-- This styling is used for the sign in form. -->
<style scoped>
  .signin-form {
    width: 400px;
    max-width: calc(100% - 32px);
    margin: 30px auto;
    border: 1px solid #eee;
    padding: 20px;
    box-shadow: 0 2px 3px #ccc;
  }

  .signin-form :deep(.label) {
    color: whitesmoke;
  }

  label.checkbox:hover {
    color: whitesmoke;
  }
</style>
