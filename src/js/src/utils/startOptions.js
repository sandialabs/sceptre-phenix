// The options of an experiment start (POST /experiments/{name}/start): the
// form the start dialog shows, its checks, the request body it sends, and
// how to read the answer.
import { vlanInputError } from '@/utils/vlans.js';

// The start dialog's form as it opens: the start the server does with no
// body. The VLAN fields are text, and empty means the experiment's range.
export const START_DEFAULTS = Object.freeze({
  dryRun: false,
  treatMinimegaErrorsAsWarnings: false,
  honorRunPeriodically: true,
  vlanMin: '',
  vlanMax: '',
});

const isEmpty = (value) => String(value ?? '').trim() === '';

// Why a VLAN field is not valid, or '' when it is; empty is valid.
const vlanFieldError = (value) => (isEmpty(value) ? '' : vlanInputError(value));

// Why each VLAN field of the form is not valid, '' when it is valid.
export function startOptionErrors(form) {
  const vlanMin = vlanFieldError(form.vlanMin);
  const vlanMax = vlanFieldError(form.vlanMax);
  if (
    !vlanMin &&
    !vlanMax &&
    !isEmpty(form.vlanMin) &&
    !isEmpty(form.vlanMax) &&
    Number(form.vlanMin) > Number(form.vlanMax)
  ) {
    return {
      vlanMin: 'The minimum must not be more than the maximum.',
      vlanMax: '',
    };
  }
  return { vlanMin, vlanMax };
}

// The request body for a valid form: only the options that differ from the
// defaults, so {} for the defaults. A dry run starts nothing, so it sends no
// schedule option.
export function startRequestBody(form) {
  const body = {};
  if (form.dryRun) body.dryRun = true;
  if (form.treatMinimegaErrorsAsWarnings) {
    body.treatMinimegaErrorsAsWarnings = true;
  }
  if (!form.dryRun && !form.honorRunPeriodically) {
    body.honorRunPeriodically = false;
  }
  if (!isEmpty(form.vlanMin)) body.vlanMin = Number(form.vlanMin);
  if (!isEmpty(form.vlanMax)) body.vlanMax = Number(form.vlanMax);
  return body;
}

// A start's answer: the Experiment, or a StartResult that holds it with the
// warnings, and for a dry run the minimega script path.
export function startResult(data) {
  if (data?.experiment && typeof data.experiment === 'object') {
    return {
      experiment: data.experiment,
      dryRun: data.dryRun === true,
      warnings: Array.isArray(data.warnings) ? data.warnings.map(String) : [],
      mmScript: typeof data.mmScript === 'string' ? data.mmScript : '',
    };
  }
  return {
    experiment: data ?? null,
    dryRun: false,
    warnings: [],
    mmScript: '',
  };
}
