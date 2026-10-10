<script setup>
  import { RouterView } from 'vue-router';
  import {
    connectWebsocket,
    disconnectWebsocket,
    isWsConnected,
  } from './utils/websocket';
  import AppHeader from '@/components/AppHeader.vue';
  import AppFooter from '@/components/AppFooter.vue';
  import LogoutWarning from '@/components/LogoutWarning.vue';
  import { computed, onUnmounted, onMounted } from 'vue';
  import { useRoute } from 'vue-router';
  import router from '@/router';
  import { usePhenixStore } from '@/store';
  import { storeToRefs } from 'pinia';
  import { watch } from 'vue';

  import { TimeoutTool } from '@/utils/timeout.js';
  import { focusMode } from '@/builder/focusMode.js';

  const store = usePhenixStore();
  const timeout = new TimeoutTool();
  const route = useRoute();

  // Routes such as the Builder editor fill the viewport under the header
  // instead of sitting in the centered, scrolling page container.
  const fullBleed = computed(() => Boolean(route.meta.fullBleed));

  fetch(router.resolve({ name: 'features' }).href)
    .then((resp) => resp.json())
    .then((data) => {
      store.features = data.features;
    })
    .catch((err) => {
      console.log(err);
    });

  onMounted(() => {
    // connect websockets once user has authenticated (or auth disabled)
    if (import.meta.env.VITE_AUTH === 'disabled' || store.auth) {
      connectWebsocket();
      timeout.fetchAndStart();
    } else {
      const { auth } = storeToRefs(store);
      watch(auth, async (newAuth) => {
        if (newAuth && !isWsConnected()) {
          connectWebsocket();
        } else if (!newAuth && isWsConnected()) {
          disconnectWebsocket();
        }

        if (newAuth) {
          timeout.fetchAndStart();
        }
      });
    }
  });

  onUnmounted(() => {
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
         state. -->
    <app-header v-show="!focusMode.on"></app-header>
    <main
      id="main"
      tabindex="-1"
      :class="fullBleed ? 'main--full-bleed' : 'row container is-fullhd px-4'">
      <router-view></router-view>
    </main>
    <app-footer v-if="!fullBleed"></app-footer>
    <!-- Warns on any page before a logout that would delete Builder
         changes the server does not have (see utils/logout.js). -->
    <logout-warning></logout-warning>
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

  /* Links that do not fit on one line, in a narrow window or with enlarged
     text, wrap instead of pushing the last ones off the page (WCAG 1.4.4,
     1.4.10). The Builder page cannot scroll sideways at all. */
  #app :deep(.navbar-menu) {
    flex-shrink: 1;
    min-width: 0;
  }

  #app :deep(.navbar-start) {
    flex-wrap: wrap;
    min-width: 0;
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
