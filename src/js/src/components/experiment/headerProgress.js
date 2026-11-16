// The start or stop progress of the experiment page's header. Base.vue keeps
// it, so it stays when the page changes between its stopped and running views.

import { onBeforeUnmount, provide, reactive, readonly } from 'vue';

import { showError } from '@/utils/errorNotif.js';
import {
  afterMessage,
  failureLabel,
  isSending,
  sendingProgress,
} from '@/utils/experimentProgress.js';
import { addWsHandler, removeWsHandler } from '@/utils/websocket';

// The key the views and the header inject the progress with.
export const EXPERIMENT_PROGRESS = Symbol('experimentProgress');

// Follows the start and stop messages of the experiment `name` and gives
// the views and the header { state: { progress, failure }, begin }.
export function provideExperimentProgress(name) {
  const state = reactive({ progress: null, failure: null });
  // the operation of this page's own request, whose failure it tells itself
  let pending = null;

  const handle = (msg) => {
    if (msg.resource?.type !== 'experiment' || msg.resource.name !== name) {
      return;
    }
    const action = msg.resource.action;
    Object.assign(state, afterMessage(state, action, msg.result));
    if (!['errorStarting', 'errorStopping'].includes(action) || pending) {
      return;
    }
    const verb = action === 'errorStarting' ? 'start' : 'stop';
    const body = msg.result && typeof msg.result === 'object';
    showError(
      `The ${name} experiment did not ${verb}`,
      body ? failureLabel(msg.result) : '',
    );
  };
  addWsHandler(handle);
  onBeforeUnmount(() => removeWsHandler(handle));

  const tracker = {
    state: readonly(state),
    // Shows the bar of `operation` from the click. Call done() when the server
    // answers, or undo() when the request failed, to put back the old bar.
    begin(operation) {
      const before = state.progress;
      state.failure = null;
      state.progress = sendingProgress(operation);
      pending = operation;
      return {
        done() {
          pending = null;
        },
        undo() {
          pending = null;
          if (isSending(state.progress)) state.progress = before;
        },
      };
    },
  };
  provide(EXPERIMENT_PROGRESS, tracker);
  return tracker;
}
