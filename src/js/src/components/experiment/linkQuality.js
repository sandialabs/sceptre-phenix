// The link quality (minimega qos) of a VM interface as the dialog edits it:
// a loss percent, a delay duration, and a rate with its unit. The server
// gives and takes each as a string, '' when unset.

export const RATE_UNITS = ['kbit', 'mbit', 'gbit'];

// The server's rules: a loss percent, a delay with one unit (us, ms or s),
// and a rate of 1 to 999999999 without leading zeros.
const LOSS = /^\d+(\.\d+)?$/;
const DELAY = /^\d+(\.\d+)?(us|ms|s)$/;
const RATE_NUMBER = /^[1-9]\d{0,8}$/;
// a rate as the server gives it, such as 10mbit
const RATE = /^(\d+)\s*(kbit|mbit|gbit)$/i;

// The form's fields for an interface's entry from GET .../qos.
export function qosForm(entry) {
  const rate = RATE.exec((entry?.rate ?? '').trim());
  return {
    loss: (entry?.loss ?? '').trim().replace(/%$/, ''),
    delay: (entry?.delay ?? '').trim(),
    rate: rate ? rate[1] : (entry?.rate ?? '').trim(),
    unit: rate ? rate[2].toLowerCase() : 'mbit',
  };
}

// Whether an entry from the server sets anything.
export const qosSet = (entry) =>
  !!(entry && (entry.loss || entry.delay || entry.rate));

// What is wrong with each field of the form, '' when nothing, and with the
// form as a whole (`form`).
export function qosProblems(form) {
  const loss = form.loss.trim();
  const delay = form.delay.trim();
  const rate = form.rate.trim();
  const problems = { loss: '', delay: '', rate: '', form: '' };
  if (loss && !(LOSS.test(loss) && Number(loss) < 100)) {
    problems.loss = 'Enter a number from 0 to less than 100.';
  }
  if (delay && !DELAY.test(delay)) {
    problems.delay = 'Enter a number and the unit us, ms or s, such as 100ms.';
  }
  if (rate && !RATE_NUMBER.test(rate)) {
    problems.rate = 'Enter a whole number from 1 to 999999999.';
  }
  if (rate && (loss || delay)) {
    problems.form = 'Rate cannot be set with loss or delay. Clear one.';
  } else if (!loss && !delay && !rate) {
    problems.form = 'Enter a loss, a delay or a rate.';
  }
  return problems;
}

// The PUT body for a valid form: only the fields it sets.
export function qosBody(form) {
  const loss = form.loss.trim();
  const delay = form.delay.trim();
  const rate = form.rate.trim();
  return {
    ...(loss && { loss }),
    ...(delay && { delay }),
    ...(rate && { rate: rate + form.unit }),
  };
}
