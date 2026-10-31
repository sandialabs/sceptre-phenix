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
      <!-- The page keeps the dialog after it closes, so one reopened while it
           closes still shows. A new form key on each open drops old values. -->
      <form
        class="modal-card"
        :key="signUpForm"
        novalidate
        @submit.prevent="create">
        <header class="modal-card-head">
          <h2 class="modal-card-title">Create a New Account</h2>
        </header>
        <section class="modal-card-body">
          <b-field
            label="User Name"
            label-for="signup-username"
            :type="signUpError('username') ? 'is-danger' : null">
            <b-input
              id="signup-username"
              :compat-fallthrough="false"
              ref="signupUsername"
              type="text"
              autocomplete="username"
              v-model="signUp.username"
              v-bind="invalid(signUpError('username'), 'signup-username-error')"
              minlength="4"
              maxlength="32"></b-input>
            <template #message="{ messages }">
              <span
                v-if="signUpError('username') || messages.length"
                id="signup-username-error"
                >{{ signUpError('username') || messages.join(' ') }}</span
              >
            </template>
          </b-field>
          <b-field
            label="First Name"
            label-for="signup-first-name"
            :type="signUpErrors.first_name ? 'is-danger' : null">
            <b-input
              id="signup-first-name"
              :compat-fallthrough="false"
              ref="signupFirstName"
              type="text"
              autocomplete="given-name"
              v-model="signUp.first_name"
              v-bind="
                invalid(signUpErrors.first_name, 'signup-first-name-error')
              "></b-input>
            <template #message>
              <span
                v-if="signUpErrors.first_name"
                id="signup-first-name-error"
                >{{ signUpErrors.first_name }}</span
              >
            </template>
          </b-field>
          <b-field
            label="Last Name"
            label-for="signup-last-name"
            :type="signUpErrors.last_name ? 'is-danger' : null">
            <b-input
              id="signup-last-name"
              :compat-fallthrough="false"
              ref="signupLastName"
              type="text"
              autocomplete="family-name"
              v-model="signUp.last_name"
              v-bind="
                invalid(signUpErrors.last_name, 'signup-last-name-error')
              "></b-input>
            <template #message>
              <span v-if="signUpErrors.last_name" id="signup-last-name-error">{{
                signUpErrors.last_name
              }}</span>
            </template>
          </b-field>
          <b-field
            label="Password"
            label-for="signup-password"
            :type="signUpErrors.password ? 'is-danger' : null">
            <b-input
              id="signup-password"
              :compat-fallthrough="false"
              ref="signupPassword"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              v-model="signUp.password"
              v-bind="
                invalid(signUpErrors.password, 'signup-password-error')
              "></b-input>
            <template #message="{ messages }">
              <span
                v-if="signUpErrors.password || messages.length"
                id="signup-password-error"
                >{{ signUpErrors.password || messages.join(' ') }}</span
              >
            </template>
          </b-field>
          <b-field
            label="Confirm Password"
            label-for="signup-confirm-password"
            :type="signUpErrors.confirmPassword ? 'is-danger' : null">
            <b-input
              id="signup-confirm-password"
              :compat-fallthrough="false"
              ref="signupConfirmPassword"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              v-model="signUp.confirmPassword"
              v-bind="
                invalid(
                  signUpErrors.confirmPassword,
                  'signup-confirm-password-error',
                )
              "></b-input>
            <template #message="{ messages }">
              <span
                v-if="signUpErrors.confirmPassword || messages.length"
                id="signup-confirm-password-error"
                >{{ signUpErrors.confirmPassword || messages.join(' ') }}</span
              >
            </template>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button type="submit" class="button">Create User</button>
        </footer>
      </form>
    </b-modal>
    <!-- A form, so Enter submits it and password managers know its fields. -->
    <form class="signin-form" novalidate @submit.prevent="onSubmit">
      <h1 class="title is-4">Sign in</h1>
      <b-field
        label="Username"
        label-for="signin-username"
        :type="errors.username ? 'is-danger' : null">
        <b-input
          id="signin-username"
          :compat-fallthrough="false"
          ref="username"
          type="text"
          autocomplete="username"
          v-model="username"
          v-bind="invalid(errors.username, 'signin-username-error')"></b-input>
        <template #message>
          <span v-if="errors.username" id="signin-username-error">{{
            errors.username
          }}</span>
        </template>
      </b-field>
      <b-field
        label="Password"
        label-for="signin-password"
        :type="errors.password ? 'is-danger' : null">
        <b-input
          id="signin-password"
          :compat-fallthrough="false"
          ref="password"
          type="password"
          autocomplete="current-password"
          v-model="password"
          v-bind="invalid(errors.password, 'signin-password-error')"></b-input>
        <template #message>
          <span v-if="errors.password" id="signin-password-error">{{
            errors.password
          }}</span>
        </template>
      </b-field>
      <!-- Stays on the page until the next attempt, unlike a toast. -->
      <div class="signin-error" role="alert">
        <p v-if="errors.form" class="help is-danger">{{ errors.form }}</p>
      </div>
      <b-checkbox
        type="is-light"
        size="is-small"
        v-model="rememberMe"
        native-value="false"
        >Remember me</b-checkbox
      >
      <br />
      <button type="submit" class="button">Submit</button>
      <button
        ref="createAccount"
        type="button"
        class="button is-pulled-right is-small is-text"
        @click="signUpModal = true">
        Create Account
      </button>
    </form>
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

  // The inline errors of the Create Account fields.
  function blankSignUpErrors() {
    return {
      username: null,
      first_name: null,
      last_name: null,
      password: null,
      confirmPassword: null,
    };
  }

  // The inline errors of the sign-in form.
  function blankErrors() {
    return { username: null, password: null, form: null };
  }

  export default {
    methods: {
      // The attributes that tie a field to its inline error, if it has one.
      invalid(error, id) {
        return error
          ? { 'aria-invalid': 'true', 'aria-describedby': id }
          : { 'aria-invalid': null, 'aria-describedby': null };
      },

      // The Create Account field error, including a name that is taken.
      signUpError(field) {
        if (field === 'username' && this.userExists) {
          return 'User already exists';
        }
        return this.signUpErrors[field];
      },

      //  this method is called when the Submit button is pressed (or
      //  return key is) executed. It will check that a username
      //  is used, and/or a password. It does not check if they are valid.
      onSubmit() {
        this.errors = blankErrors();

        if (!this.username) {
          this.errors.username = 'You must include a username';
        }
        if (!this.password) {
          this.errors.password = 'You must include a password';
        }
        if (this.errors.username || this.errors.password) {
          (this.errors.username
            ? this.$refs.username
            : this.$refs.password
          )?.focus();
          return {};
        }

        axiosInstance
          .post('login', { user: this.username, pass: this.password })
          .then(
            (response) => {
              const store = usePhenixStore();
              store.login(response.data, this.rememberMe);
            },
            (err) => {
              const status = err.status ?? err.response?.status;

              if (status == 401) {
                this.errors.form = 'The username and/or password is incorrect';
              } else if (!status) {
                this.errors.form = 'The data server is not available.';
              } else {
                this.errors.form = 'Getting the user information failed.';
              }

              // The username stays, so only the password is typed again.
              this.password = null;
              this.$refs.password?.focus();
            },
          );
      },

      create() {
        const checks = [
          ['username', 'You must include a username'],
          ['first_name', 'You must include a first name'],
          ['last_name', 'You must include a last name'],
          ['password', 'You must include a password'],
          ['confirmPassword', 'You must include a password confirmation'],
        ];
        const refs = {
          username: 'signupUsername',
          first_name: 'signupFirstName',
          last_name: 'signupLastName',
          password: 'signupPassword',
          confirmPassword: 'signupConfirmPassword',
        };

        this.signUpErrors = blankSignUpErrors();
        for (const [field, message] of checks) {
          if (!this.signUp[field]) {
            this.signUpErrors[field] = message;
          }
        }
        if (
          !this.signUpErrors.confirmPassword &&
          this.signUp.password &&
          this.signUp.password != this.signUp.confirmPassword
        ) {
          this.signUpErrors.confirmPassword = 'Your passwords do not match';
        }

        const first = checks.find(([field]) => this.signUpErrors[field]);
        if (first) {
          this.$refs[refs[first[0]]]?.focus();
          return {};
        }

        // Keep a copy, because reopening the dialog clears the form before the
        // request finishes. Close the dialog only if it was not reopened.
        const form = this.signUpForm;
        const account = {
          username: this.signUp.username,
          password: this.signUp.password,
          first_name: this.signUp.first_name,
          last_name: this.signUp.last_name,
        };

        axiosInstance
          .post('signup', account)
          .then((_) => {
            // on success, sign user in
            this.username = account.username;
            this.password = account.password;
            this.onSubmit();
            if (this.signUpForm === form) {
              this.signUpModal = false;
            }
          })
          .catch((err) => {
            // Another account has the name: the User Name field says so,
            // a toast announces it, and focus goes back to the field.
            if (err.response?.status === 409) {
              this.$buefy.toast.open({
                message: 'User already exists',
                type: 'is-warning',
                duration: 4000,
              });
              if (this.signUpForm === form) {
                this.userExists = true;
                this.$refs.signupUsername?.focus();
              }

              return;
            }

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
      // Each open starts empty, even one before the close has finished. Focus
      // goes to the first field on open and back to Create Account on close.
      signUpModal(open) {
        if (open) {
          this.signUp = blankSignUp();
          this.signUpErrors = blankSignUpErrors();
          this.userExists = false;
          this.signUpForm++;
        }
        this.$nextTick(() =>
          open
            ? this.$refs.signupUsername?.focus()
            : this.$refs.createAccount?.focus(),
        );
      },
      // After the user changes the name, the form does not know if it is
      // taken.
      'signUp.username'() {
        this.userExists = false;
      },
    },

    data() {
      return {
        signUpModal: false,
        // The key of the dialog's form, changed each time it opens.
        signUpForm: 0,
        signUp: blankSignUp(),
        signUpErrors: blankSignUpErrors(),
        errors: blankErrors(),
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

  /* Buefy fields keep their message slot; it shows only with an error. */
  .signin-form :deep(.help:empty) {
    display: none;
  }

  .signin-error p {
    margin: -0.25rem 0 0.75rem;
  }

  .modal-card :deep(.help:empty) {
    display: none;
  }
</style>
