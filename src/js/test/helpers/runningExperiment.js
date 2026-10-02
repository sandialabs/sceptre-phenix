// The running experiment page run without mounting it. A test file importing
// this mocks the modules the page reaches the server through first (see
// experimentMocks.js).
import { vi } from 'vitest';

import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { makeContext } from './context.js';

// the page on the demo experiment, with its dialogs and toasts recorded
export const page = (fields = {}) =>
  makeContext(RunningExperiment, {
    $route: { params: { id: 'demo' } },
    $buefy: {
      dialog: { confirm: vi.fn(), alert: vi.fn() },
      toast: { open: vi.fn() },
    },
    experiment: { name: 'demo', vms: [] },
    ...fields,
  });
