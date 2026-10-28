import { watch } from 'vue';

// Follows sign-ins and sign-outs for as long as the app runs: onSignIn runs at
// once if auth is disabled or the user is signed in, and on each later sign-in,
// and onSignOut on each sign-out. `auth` is a ref of whether the user is signed
// in. Returns a function that stops following.
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
