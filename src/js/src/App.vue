<script setup>
  import { RouterView } from 'vue-router';
  import {
    addWsHandler,
    connectWebsocket,
    disconnectWebsocket,
    isWsConnected,
    onWsReconnect,
    removeWsHandler,
  } from './utils/websocket';
  import AppHeader from '@/components/AppHeader.vue';
  import AppFooter from '@/components/AppFooter.vue';
  import { onUnmounted, onMounted } from 'vue';
  import router from '@/router';
  import { usePhenixStore } from '@/store';
  import { storeToRefs } from 'pinia';

  import { TimeoutTool } from '@/utils/timeout.js';
  import { schedulePagePreload } from '@/utils/pageData.js';
  import { useJobStore } from '@/utils/jobs.js';
  import { invalidatePages } from '@/utils/pageCache.js';
  import { followSignIn } from '@/utils/signIn.js';
  import { viewKey } from '@/utils/viewKey.js';

  const store = usePhenixStore();

  // cached pages a live update changes are loaded again on their next visit
  addWsHandler(invalidatePages);
  const timeout = new TimeoutTool();
  const jobs = useJobStore();

  // Background jobs: loaded once signed in, kept current by the websocket,
  // and loaded again after it reconnects (a reload or a lost connection),
  // since messages sent meanwhile are lost.
  const applyJob = (msg) => jobs.apply(msg);
  const loadJobs = () =>
    jobs.load().catch((err) => console.warn('failed to load jobs', err));
  let stopJobReload = null;

  function followJobs() {
    if (stopJobReload) return;
    addWsHandler(applyJob);
    stopJobReload = onWsReconnect(loadJobs);
    loadJobs();
  }

  function forgetJobs() {
    removeWsHandler(applyJob);
    stopJobReload?.();
    stopJobReload = null;
    jobs.clear();
  }

  fetch(router.resolve({ name: 'features' }).href)
    .then((resp) => resp.json())
    .then((data) => {
      store.features = data.features;
    })
    .catch((err) => {
      console.warn('failed to get enabled features', err);
    })
    .finally(() => {
      store.featuresLoaded = true;
    });

  let stopFollowingSignIn = () => {};

  onMounted(() => {
    // connect websockets once user has authenticated (or auth disabled), and
    // drop the user's websocket and jobs on every sign-out, also when the
    // page was loaded already signed in
    const { auth } = storeToRefs(store);
    stopFollowingSignIn = followSignIn(auth, {
      authDisabled: import.meta.env.VITE_AUTH === 'disabled',
      onSignIn: () => {
        if (!isWsConnected()) connectWebsocket();
        followJobs();
        timeout.fetchAndStart();
        schedulePagePreload();
      },
      onSignOut: () => {
        if (isWsConnected()) disconnectWebsocket();
        forgetJobs();
      },
    });
  });

  onUnmounted(() => {
    stopFollowingSignIn();
    forgetJobs();
    removeWsHandler(invalidatePages);
    disconnectWebsocket();
  });
</script>

<template>
  <div id="app" @click="timeout.resetTimer" @keydown="timeout.resetTimer">
    <a class="skip-link" href="#main">Skip to main content</a>
    <app-header></app-header>
    <main id="main" class="row container is-fullhd px-4" tabindex="-1">
      <router-view v-slot="{ Component, route }">
        <component :is="Component" :key="viewKey(route)"></component>
      </router-view>
    </main>
    <app-footer></app-footer>
  </div>
</template>
<style lang="scss" scoped>
  #app {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
  }

  #main {
    width: 100%;
    max-width: 1500px;
    padding-bottom: 20px;
  }

  // Visually hidden until it receives keyboard focus.
  .skip-link {
    position: absolute;
    top: -100px;
    left: 8px;
    z-index: 100;
    padding: 8px 12px;
    color: #20252b;
    background: #ffd166;
    border-radius: 4px;
  }

  .skip-link:focus {
    top: 8px;
  }
</style>
