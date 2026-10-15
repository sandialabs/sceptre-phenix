import { watch } from 'vue';

// Follows the user signing in and out for as long as the app runs: calls
// onSignIn at once when auth is disabled or the user is already signed in
// (as after a reload), and again on each later sign-in, and onSignOut on each
// sign-out, so what one user started is torn down before the next signs in
// without a reload. `auth` is a ref of whether the user is signed in. Returns
// a function that stops following.
export function followSignIn(auth, { authDisabled, onSignIn, onSignOut }) {
  if (authDisabled) {
    onSignIn();
    return () => {};
  }

  if (auth.value) onSignIn();

  return watch(auth, (signedIn, was) => {
    if (signedIn && !was) onSignIn();
    else if (!signedIn && was) onSignOut();
  });
}
