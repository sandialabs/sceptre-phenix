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
  import { computed, onUnmounted, onMounted } from 'vue';
  import { useRoute } from 'vue-router';
  import router from '@/router';
  import { usePhenixStore } from '@/store';
  import { storeToRefs } from 'pinia';

  import { TimeoutTool } from '@/utils/timeout.js';
  import { schedulePagePreload } from '@/utils/pageData.js';
  import { useJobStore } from '@/utils/jobs.js';
  import { invalidatePages } from '@/utils/pageCache.js';
  import { answerTokenRequests } from '@/utils/authHandoff.js';
  import { followSignIn } from '@/utils/signIn.js';
  import { viewKey } from '@/utils/viewKey.js';
  import { focusMode } from '@/builder/focusMode.js';

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
    stopJobReload = onWsReconnect(loadJobs, { spread: true });
    loadJobs();
  }

  function forgetJobs() {
    removeWsHandler(applyJob);
    stopJobReload?.();
    stopJobReload = null;
    jobs.clear();
  }
  const route = useRoute();

  // Routes such as the Builder editor fill the viewport under the header
  // instead of sitting in the centered, scrolling page container.
  const fullBleed = computed(() => Boolean(route.meta.fullBleed));
  // Routes such as the State of Health graph use the window's width, with the
  // side gutter, header and footer kept.
  const mainClass = computed(() => {
    if (fullBleed.value) return 'main--full-bleed';
    if (route.meta.wide) return 'row px-4 main--wide';
    return 'row container is-fullhd px-4';
  });

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
  // a signed-in tab gives its token to a console tab that has none
  let stopAnsweringTokens = () => {};

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
        stopAnsweringTokens();
        stopAnsweringTokens = answerTokenRequests();
      },
      onSignOut: () => {
        if (isWsConnected()) disconnectWebsocket();
        forgetJobs();
        stopAnsweringTokens();
        stopAnsweringTokens = () => {};
      },
    });
  });

  onUnmounted(() => {
    stopFollowingSignIn();
    stopAnsweringTokens();
    forgetJobs();
    removeWsHandler(invalidatePages);
    disconnectWebsocket();
  });
</script>

<template>
  <div
    id="app"
    :class="{ 'app--full-bleed': fullBleed }"
    @click="timeout.resetTimer"
    @keydown="timeout.resetTimer">
    <a class="skip-link" href="#main">Skip to main content</a>
    <!-- The Builder's focus mode hides the navigation bar, so the editor
         fills the window (builder/focusMode.js). v-show keeps the header's
         state. A route with meta.header false, such as the VNC console, has
         its own top bar. -->
    <app-header
      v-if="route.meta.header !== false"
      v-show="!focusMode.on"></app-header>
    <main id="main" tabindex="-1" :class="mainClass">
      <router-view v-slot="{ Component, route }">
        <component :is="Component" :key="viewKey(route)"></component>
      </router-view>
    </main>
    <app-footer v-if="!fullBleed"></app-footer>
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

  #app.app--full-bleed {
    height: 100vh;
    height: 100dvh;
    min-height: 0;
    overflow: hidden;
  }

  .app--full-bleed :deep(.navbar) {
    flex: none;
    margin-bottom: 0 !important;
  }

  /* The page itself never scrolls here, so the expanded mobile menu must. */
  .app--full-bleed :deep(.navbar-menu.is-active) {
    max-height: calc(100dvh - 3.25rem);
    overflow-y: auto;
  }

  /* Header links wrap in a narrow window or with enlarged text instead of
     running off the page, which the Builder cannot scroll (WCAG 1.4.4, 1.4.10). */
  #app :deep(.navbar-menu) {
    flex-shrink: 1;
    min-width: 0;
  }

  #app :deep(.navbar-start) {
    flex-wrap: wrap;
    min-width: 0;
  }

  #main.main--wide {
    flex-grow: 1;
    max-width: none;
    margin: 0 auto;
  }

  #main.main--full-bleed {
    display: flex;
    flex: 1 1 auto;
    min-height: 0;
    max-width: none;
    padding: 0;
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
