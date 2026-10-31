// Slow work, shown on its control (BuilderBusyButton.vue). A group runs one
// task at a time, and controls read the shared state, also for palette work.

import { reactive } from 'vue';

// The task each group runs now, by group.
const running = reactive({});

// Groups that Builder.vue runs for a control: Exp (the task is the
// experiment name), Combine into a new draft, Blank diagram and Edit as a draft.
export const BUSY_GROUPS = Object.freeze({
  experiment: 'experiment',
  combine: 'combine',
  blank: 'blank',
  editPublished: 'editPublished',
});

/**
 * Resolves once the browser has painted, so a busy control shows its ring
 * before work that holds the page. Resolves at once in a hidden tab.
 */
export function afterPaint() {
  if (
    typeof document === 'undefined' ||
    typeof requestAnimationFrame !== 'function' ||
    document.visibilityState === 'hidden'
  ) {
    return Promise.resolve();
  }

  return new Promise((resolve) => {
    requestAnimationFrame(() => setTimeout(resolve, 0));
  });
}

/** The task a group runs now, or '' (reactive). */
export function busyTask(group) {
  return running[group] || '';
}

/**
 * Runs `work` as a task of a group, unless the group is busy (`ran` false).
 * The task shows at once, and the work starts after the next paint.
 */
export async function runBusy(group, task, work) {
  if (running[group]) {
    return { ran: false };
  }

  running[group] = task;

  try {
    await afterPaint();

    return { ran: true, value: await work() };
  } finally {
    delete running[group];
  }
}
