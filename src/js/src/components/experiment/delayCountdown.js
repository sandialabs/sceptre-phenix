import { onBeforeUnmount, readonly, ref, watch } from 'vue';

import { rfc3339Ms } from '@/utils/relativeTime.js';

// The countdown to the start of a VM that waits for a timer: "starts in 4m
// 12s". The server sends the due time of each timer VM with the VM list. One
// clock, shared by every component on the page, ticks each second while any
// of them shows a countdown.

const clockNow = ref(Date.now());
let watchers = 0;
let clock = null;

// the current time in milliseconds, kept current while a countdown shows
export const countdownNow = readonly(clockNow);

// Updates countdownNow every second until every watcher has called the
// function that this returns.
export function watchCountdown() {
  if (watchers++ === 0) {
    clockNow.value = Date.now();
    clock = setInterval(() => (clockNow.value = Date.now()), 1000);
  }
  let watching = true;
  return () => {
    if (!watching) return;
    watching = false;
    if (--watchers === 0) {
      clearInterval(clock);
      clock = null;
    }
  };
}

// Watches the shared clock in a component's setup while active() is true,
// and stops when the component unmounts. Returns countdownNow.
export function useCountdownClock(active) {
  let stop = null;
  const release = () => {
    stop?.();
    stop = null;
  };
  watch(
    () => Boolean(active()),
    (on) => {
      if (on && !stop) stop = watchCountdown();
      else if (!on) release();
    },
    { immediate: true },
  );
  onBeforeUnmount(release);
  return countdownNow;
}

// "1h 4m 12s", "4m 12s" or "12s": a time span in milliseconds, rounded up
// to the second
export function spanText(ms) {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  if (hours) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

// "starts in 4m 12s" before the due time, and "starts now" after it
export function countdownText(dueMs, nowMs) {
  return dueMs > nowMs ? `starts in ${spanText(dueMs - nowMs)}` : 'starts now';
}

// whether the VM still waits for its delayed start
export function waitsForStart(vm) {
  return Boolean(vm?.delayed_start) && vm.state == 'BUILDING';
}

// The due time in milliseconds of a VM that still waits for its timer, or
// null: a VM that waits for a user or C2, or has no due time, has none.
export function startDue(vm, due) {
  if (!waitsForStart(vm)) return null;
  return rfc3339Ms(due);
}

// The earliest due time in milliseconds of the due times by VM name, or null
// when there is none.
export function nextDue(dueTimes) {
  const times = Object.values(dueTimes ?? {})
    .map(rfc3339Ms)
    .filter((ms) => ms !== null);
  return times.length ? Math.min(...times) : null;
}
