<!--
A thin bar at the top of the window while a navigation waits, mostly for the
chunk of a page that was not loaded yet. Vue Router keeps the old page until
then, so without it a slow first visit looks like a frozen UI. It shows only
after SHOW_AFTER_MS, so fast navigations do not make it flash.
-->
<template>
  <div
    v-if="shown"
    class="page-load-bar"
    role="progressbar"
    aria-label="Loading page"></div>
</template>

<script setup>
  import { onBeforeUnmount, ref } from 'vue';
  import { useRouter } from 'vue-router';

  const SHOW_AFTER_MS = 150;

  const router = useRouter();
  const shown = ref(false);
  // the navigation under way, which a newer one cancels
  let current = null;
  let timer = null;

  function stop() {
    clearTimeout(timer);
    timer = null;
    current = null;
    shown.value = false;
  }

  // the end of a cancelled navigation does not hide the bar of the newer one
  const settle = (to) => {
    if (!to || to === current) stop();
  };

  const removeGuards = [
    router.beforeEach((to) => {
      current = to;
      clearTimeout(timer);
      timer = setTimeout(() => {
        shown.value = true;
      }, SHOW_AFTER_MS);
    }),
    // also called for an aborted, cancelled or duplicate navigation
    router.afterEach(settle),
    router.onError((_, to) => settle(to)),
  ];

  onBeforeUnmount(() => {
    stop();
    for (const remove of removeGuards) remove();
  });
</script>
