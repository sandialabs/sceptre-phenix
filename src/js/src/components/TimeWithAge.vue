<template>
  <span v-if="ms !== null" class="time-with-age">
    <time :datetime="time">
      <span class="time-with-age-part">{{ parts[0] }}</span>
      <wbr />
      <span class="time-with-age-part">{{ parts[1] }}</span>
    </time>
    {{ ' ' }}
    <span class="time-with-age-part time-with-age-age has-text-grey-lighter"
      >({{ age }})</span
    >
  </span>
  <span v-else>{{ time || fallback }}</span>
</template>

<script>
  import {
    isoTimeParts,
    now,
    relativeTime,
    rfc3339Ms,
    watchNow,
  } from '@/utils/relativeTime.js';

  // An RFC 3339 time as the server sent it, to the second, then how long ago
  // it was: "2026-10-28T14:03:22Z (12 hours ago)", the age kept current.
  export default {
    name: 'TimeWithAge',
    props: {
      time: { type: String, default: '' },
      // shown when there is no time; text that is not a time shows as it is
      fallback: { type: String, default: '' },
    },
    created() {
      this.stopWatching = watchNow();
    },
    beforeUnmount() {
      this.stopWatching();
    },
    computed: {
      ms() {
        return rfc3339Ms(this.time);
      },
      parts() {
        return isoTimeParts(this.time);
      },
      age() {
        return relativeTime(this.ms, now.value);
      },
    },
  };
</script>

<style scoped>
  /* a narrow column wraps between the date, the time and the age, never inside */
  .time-with-age-part {
    white-space: nowrap;
  }

  .time-with-age-age {
    font-weight: 400;
  }
</style>
