// The running experiment page run without mounting it. A test file importing
// this mocks the modules the page reaches the server through first (see
// mocks.js).
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { pageContext } from './page.js';

// the page on the demo experiment, with its dialogs and toasts recorded
export const page = (fields = {}) =>
  pageContext(RunningExperiment, {
    id: 'demo',
    fields: { experiment: { name: 'demo', vms: [] }, ...fields },
  });
