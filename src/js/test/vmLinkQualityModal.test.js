// The dialog that sets or clears the link quality (minimega qos) of a VM
// interface: what it reads as it opens, what it accepts, and what it sends.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);

import VmLinkQualityModal from '@/components/experiment/VmLinkQualityModal.vue';
import { qosForm } from '@/components/experiment/linkQuality.js';
import { httpError } from './helpers/fixtures.js';
import { buttons, mountComponent, textOf, toasts } from './helpers/mount.js';

beforeEach(() => resetMocks());

const QOS_URL = 'experiments/demo/vms/web/qos';
const IFACE_URL = 'experiments/demo/vms/web/interfaces/1/qos';

// the GET answer, with interface 1 set to `entry`
const answer = (entry) => ({
  data: {
    interfaces: [
      { interface: 0, loss: '', delay: '', rate: '' },
      { interface: 1, loss: '', delay: '', rate: '', ...entry },
    ],
  },
});

// the dialog for web's interface 1, open, once it read `entry`
async function open(entry = {}) {
  axios.get.mockResolvedValue(answer(entry));
  const wrapper = await mountComponent(VmLinkQualityModal, {
    props: {
      active: true,
      experiment: 'demo',
      vmName: 'web',
      index: 1,
      network: 'dmz (149)',
    },
  });
  await flushPromises();
  return wrapper;
}

const field = (wrapper, id) => wrapper.find(`#link-quality-${id}`);
const values = (wrapper) => ({
  loss: field(wrapper, 'loss').element.value,
  delay: field(wrapper, 'delay').element.value,
  rate: field(wrapper, 'rate').element.value,
  unit: wrapper.find('select').element.value,
});
const footer = (wrapper) => buttons(wrapper.find('footer'));
const help = (wrapper) => wrapper.findAll('.help').map(textOf).filter(Boolean);
const press = async (wrapper, cls) => {
  await wrapper.find(`.link-quality-${cls}`).trigger('click');
  await flushPromises();
};
const closed = (wrapper) =>
  wrapper.emitted('update:active')?.some(([on]) => on === false) ?? false;

describe('opening', () => {
  it('says what it shapes and loads the values set on the interface', async () => {
    const wrapper = await open({ loss: '5%', delay: '100ms', rate: '' });
    expect(axios.get).toHaveBeenCalledWith(QOS_URL);
    expect(textOf(wrapper.find('h2'))).toBe(
      'Link quality of interface 1 of web',
    );
    expect(textOf(wrapper.find('.link-quality-about'))).toBe(
      'This shapes the traffic that the VM receives on dmz (149). Rate cannot combine with loss or delay.',
    );
    expect(values(wrapper)).toEqual({
      loss: '5',
      delay: '100ms',
      rate: '',
      unit: 'mbit',
    });
    expect(footer(wrapper)).toEqual(['Cancel', 'Clear', 'Apply']);
    // the labels give the units and range the server accepts
    expect(wrapper.findAll('label.label').map(textOf)).toEqual([
      'Loss (%)',
      'Delay (us, ms or s)',
      'Rate (1 to 999999999)',
    ]);
  });

  it.each([
    ['10mbit', '10', 'mbit'],
    ['2 GBIT', '2', 'gbit'],
    ['fast', 'fast', 'mbit'],
  ])('reads the rate %s as %s %s', async (rate, number, unit) => {
    const wrapper = await open({ rate });
    expect(values(wrapper)).toMatchObject({ rate: number, unit });
  });

  it('spins while it loads, and greys out Clear for an interface with nothing set', async () => {
    const get = Promise.withResolvers();
    axios.get.mockReturnValue(get.promise);
    const wrapper = await mountComponent(VmLinkQualityModal, {
      props: { active: true, experiment: 'demo', vmName: 'web', index: 1 },
    });
    expect(textOf(wrapper.find('[role="status"]'))).toBe(
      'Loading the link quality…',
    );
    expect(footer(wrapper)).toEqual([
      'Cancel',
      'Clear (disabled)',
      'Apply (disabled)',
    ]);
    get.resolve(answer({}));
    await flushPromises();
    expect(wrapper.find('[role="status"]').exists()).toBe(false);
    expect(footer(wrapper)).toEqual([
      'Cancel',
      'Clear (disabled)',
      'Apply (disabled)',
    ]);
    expect(help(wrapper)).toEqual(['Enter a loss, a delay or a rate.']);
  });

  it.each([
    [
      'the message the server gives',
      httpError(409, { message: 'not running' }),
      'not running',
    ],
    [
      'that it could not',
      new Error('down'),
      'The link quality could not be read.',
    ],
  ])('says %s when it cannot read them', async (_, err, text) => {
    axios.get.mockRejectedValue(err);
    const wrapper = await mountComponent(VmLinkQualityModal, {
      props: { active: true, experiment: 'demo', vmName: 'web', index: 1 },
    });
    await flushPromises();
    expect(textOf(wrapper.find('[role="alert"]'))).toBe(text);
    expect(values(wrapper)).toMatchObject({ loss: '', delay: '', rate: '' });
  });

  it('reads them again each time it opens, keeping only the last answer', async () => {
    const wrapper = await open({ loss: '5' });
    await wrapper.setProps({ active: false });
    const first = Promise.withResolvers();
    const second = Promise.withResolvers();
    axios.get.mockReturnValueOnce(first.promise);
    axios.get.mockReturnValueOnce(second.promise);
    await wrapper.setProps({ active: true });
    await wrapper.setProps({ active: false });
    await wrapper.setProps({ active: true });

    second.resolve(answer({ delay: '20ms' }));
    await flushPromises();
    first.resolve(answer({ loss: '50' }));
    await flushPromises();
    expect(values(wrapper)).toMatchObject({ loss: '', delay: '20ms' });

    // a failed older read does not replace the newer one either
    await wrapper.setProps({ active: false });
    const third = Promise.withResolvers();
    axios.get.mockReturnValueOnce(third.promise);
    axios.get.mockResolvedValueOnce(answer({ rate: '1kbit' }));
    await wrapper.setProps({ active: true });
    await wrapper.setProps({ active: false });
    await wrapper.setProps({ active: true });
    await flushPromises();
    third.reject(new Error('late'));
    await flushPromises();
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(values(wrapper)).toMatchObject({ rate: '1', unit: 'kbit' });
  });
});

describe('what it accepts', () => {
  const LOSS = 'Enter a number from 0 to less than 100.';
  const DELAY = 'Enter a number and the unit us, ms or s, such as 100ms.';
  const RATE = 'Enter a whole number from 1 to 999999999.';
  const BOTH = 'Rate cannot be set with loss or delay. Clear one.';

  it.each([
    // loss, delay, rate: whether Apply may send it, and what the dialog says
    ['5', '', '', true, []],
    ['0.5', '1.5s', '', true, []],
    ['0', '1.5s', '', true, []],
    ['', '', '100', true, []],
    ['', '', '999999999', true, []],
    ['99.9', '250us', '', true, []],
    ['', '100ms', '', true, []],
    ['100', '', '', false, [LOSS]],
    ['-1', '', '', false, [LOSS]],
    ['five', '', '', false, [LOSS]],
    ['1e1', '', '', false, [LOSS]],
    ['', '100', '', false, [DELAY]],
    ['', '10 ms', '', false, [DELAY]],
    ['', '1m30s', '', false, [DELAY]],
    ['', '1m', '', false, [DELAY]],
    ['', '5ns', '', false, [DELAY]],
    ['', '5µs', '', false, [DELAY]],
    ['', '1h', '', false, [DELAY]],
    ['', '10MS', '', false, [DELAY]],
    ['', '', '0', false, [RATE]],
    ['', '', '010', false, [RATE]],
    ['', '', '1000000000', false, [RATE]],
    ['', '', '1.5', false, [RATE]],
    ['5', '', '10', false, [BOTH]],
    ['', '10ms', '10', false, [BOTH]],
    ['', ' ', '', false, ['Enter a loss, a delay or a rate.']],
  ])(
    'loss %j, delay %j, rate %j: may apply %s',
    async (loss, delay, rate, valid, said) => {
      const wrapper = await open();
      await field(wrapper, 'loss').setValue(loss);
      await field(wrapper, 'delay').setValue(delay);
      await field(wrapper, 'rate').setValue(rate);
      expect(footer(wrapper).at(-1)).toBe(valid ? 'Apply' : 'Apply (disabled)');
      expect(help(wrapper)).toEqual(said);
      // the rate's clash with loss or delay shows as an error
      expect(wrapper.find('.link-quality-problem.is-danger').exists()).toBe(
        said[0] === BOTH,
      );
    },
  );
});

describe('applying and clearing', () => {
  it('sends only the values set, spinning until the server answers, then closes', async () => {
    const put = Promise.withResolvers();
    axios.put.mockReturnValue(put.promise);
    const wrapper = await open();
    await field(wrapper, 'rate').setValue(' 20 ');
    await wrapper.find('select').setValue('kbit');
    await press(wrapper, 'apply');

    expect(axios.put).toHaveBeenCalledWith(IFACE_URL, { rate: '20kbit' });
    expect(footer(wrapper)).toEqual([
      'Cancel',
      'Clear (disabled)',
      'Apply (spinning) (disabled)',
    ]);
    put.resolve({
      data: { interface: 1, loss: '', delay: '', rate: '20kbit' },
    });
    await flushPromises();
    expect(closed(wrapper)).toBe(true);
    expect(toasts()).toEqual([
      {
        text: 'Link quality set on interface 1 of the web VM.',
        type: 'is-success',
      },
    ]);
  });

  it('sends loss and delay together', async () => {
    axios.put.mockResolvedValue({ data: {} });
    const wrapper = await open();
    await field(wrapper, 'loss').setValue('2.5');
    await field(wrapper, 'delay').setValue('40ms');
    await press(wrapper, 'apply');
    expect(axios.put).toHaveBeenCalledWith(IFACE_URL, {
      loss: '2.5',
      delay: '40ms',
    });
  });

  it('shows why the server refused, and stays open', async () => {
    const err = httpError(400, { message: 'invalid loss' });
    axios.put.mockRejectedValue(err);
    const wrapper = await open();
    await field(wrapper, 'loss').setValue('5');
    await press(wrapper, 'apply');
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(closed(wrapper)).toBe(false);
    expect(footer(wrapper)).toEqual(['Cancel', 'Clear (disabled)', 'Apply']);
  });

  it('clears what is set, spinning until the server answers, then closes', async () => {
    const del = Promise.withResolvers();
    axios.delete.mockReturnValue(del.promise);
    const wrapper = await open({ delay: '100ms' });
    await press(wrapper, 'clear');

    expect(axios.delete).toHaveBeenCalledWith(IFACE_URL);
    expect(footer(wrapper)).toEqual([
      'Cancel',
      'Clear (spinning) (disabled)',
      'Apply (disabled)',
    ]);
    del.resolve({ status: 204 });
    await flushPromises();
    expect(closed(wrapper)).toBe(true);
    expect(toasts()[0].text).toBe(
      'Link quality cleared on interface 1 of the web VM.',
    );
  });

  it('shows why it could not clear, and stays open', async () => {
    const err = httpError(409, { message: 'not running' });
    axios.delete.mockRejectedValue(err);
    const wrapper = await open({ rate: '1mbit' });
    await press(wrapper, 'clear');
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(closed(wrapper)).toBe(false);
  });

  it('closes on Cancel', async () => {
    const wrapper = await open();
    await wrapper.findAll('footer button')[0].trigger('click');
    expect(closed(wrapper)).toBe(true);
  });
});

describe('qosForm', () => {
  it('reads no entry as nothing set', () => {
    expect(qosForm(undefined)).toEqual({
      loss: '',
      delay: '',
      rate: '',
      unit: 'mbit',
    });
  });
});
