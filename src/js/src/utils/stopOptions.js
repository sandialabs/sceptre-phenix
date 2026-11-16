// The options of an experiment stop (POST /experiments/{name}/stop) and of a
// restart (POST /experiments/{name}/restart), which takes the stop options and
// the start options without a dry run.
import { START_DEFAULTS, startRequestBody } from '@/utils/startOptions.js';

// The stop dialog's form as it opens: a plain stop.
export const STOP_DEFAULTS = Object.freeze({
  keepInjectionSnapshots: false,
  force: false,
});

// The restart dialog's form as it opens: a plain stop, then a plain start.
export const RESTART_DEFAULTS = Object.freeze({
  ...START_DEFAULTS,
  ...STOP_DEFAULTS,
});

// The stop options that are set, so {} for a plain stop.
export function stopRequestBody(form) {
  const body = {};
  if (form.keepInjectionSnapshots) body.keepInjectionSnapshots = true;
  if (form.force) body.force = true;
  return body;
}

// The request body of a restart for a valid form: only the options that
// differ from the defaults, so {} for the defaults. It never asks for a dry run.
export const restartRequestBody = (form) => ({
  ...startRequestBody({ ...form, dryRun: false }),
  ...stopRequestBody(form),
});
