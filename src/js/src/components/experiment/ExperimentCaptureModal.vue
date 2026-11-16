<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    has-modal-card
    ref="modal">
    <form class="modal-card experiment-capture-card" @submit.prevent="start">
      <header class="modal-card-head">
        <h2 class="modal-card-title">{{ title }}</h2>
      </header>
      <section class="modal-card-body">
        <b-notification
          v-for="(notice, i) in notices"
          :key="i"
          class="capture-budget-notice"
          :type="notice.type"
          :closable="false"
          role="status">
          {{ notice.text }}
        </b-notification>

        <fieldset
          class="field capture-vlans-field"
          :aria-describedby="`${uid}-vlans-help`">
          <legend class="label">VLANs</legend>
          <div v-if="vlanChoices.length" class="capture-vlans">
            <b-checkbox
              v-for="v in vlanChoices"
              :key="v.alias"
              v-model="form.vlans"
              :native-value="v.alias">
              {{ v.alias }}{{ v.vlan ? ` (${v.vlan})` : '' }}
            </b-checkbox>
          </div>
          <p :id="`${uid}-vlans-help`" class="help">
            Empty means all experiment VLANs.
          </p>
        </fieldset>

        <div class="field">
          <label class="label" :for="`${uid}-filter`">
            BPF filter (optional)
          </label>
          <div class="control">
            <input
              :id="`${uid}-filter`"
              v-model="form.filter"
              class="input capture-filter"
              :class="{ 'is-danger': problems.filter }"
              type="text"
              placeholder="tcp port 80"
              autocomplete="off"
              spellcheck="false"
              :aria-invalid="problems.filter ? 'true' : undefined"
              :aria-describedby="`${uid}-filter-help ${uid}-filter-error`" />
          </div>
          <p :id="`${uid}-filter-help`" class="help">
            Kernel filter on each host. Quotes, backslash and # are not allowed.
          </p>
          <p
            :id="`${uid}-filter-error`"
            class="help is-danger capture-filter-error"
            aria-live="polite">
            {{ problems.filter }}
          </p>
        </div>

        <div class="field">
          <label class="label" :for="`${uid}-snaplen`">Snaplen</label>
          <div class="control">
            <input
              :id="`${uid}-snaplen`"
              v-model="form.snaplen"
              class="input capture-snaplen"
              :class="{ 'is-danger': problems.snaplen }"
              type="text"
              inputmode="numeric"
              :aria-invalid="problems.snaplen ? 'true' : undefined"
              :aria-describedby="`${uid}-snaplen-help ${uid}-snaplen-error`" />
          </div>
          <p :id="`${uid}-snaplen-help`" class="help">
            Bytes kept per packet, 14 to 65535.
          </p>
          <p
            :id="`${uid}-snaplen-error`"
            class="help is-danger capture-snaplen-error"
            aria-live="polite">
            {{ problems.snaplen }}
          </p>
        </div>

        <div class="field">
          <span :id="`${uid}-format`" class="label">Format</span>
          <div
            class="capture-format-choices"
            role="radiogroup"
            :aria-labelledby="`${uid}-format`"
            :aria-describedby="`${uid}-format-help`">
            <b-radio
              v-for="f in CAPTURE_FORMATS"
              :key="f"
              v-model="form.format"
              :name="`${uid}-format`"
              :native-value="f">
              {{ f }}
            </b-radio>
          </div>
          <p :id="`${uid}-format-help`" class="help">{{ FORMAT_HELP }}</p>
        </div>

        <div class="field">
          <label class="label" :for="`${uid}-name`">File name (optional)</label>
          <div class="control">
            <input
              :id="`${uid}-name`"
              v-model="form.name"
              class="input capture-name"
              type="text"
              autocomplete="off"
              spellcheck="false"
              :placeholder="defaultName" />
          </div>
        </div>

        <p class="capture-trunk-note">
          <b-icon icon="info-circle" size="is-small" />
          Frames from hardware behind a trunk are not captured.
        </p>

        <p v-if="error" class="help is-danger capture-start-error" role="alert">
          {{ error }}
        </p>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="$refs.modal.cancel('x')">
          {{ full ? 'Close' : 'Cancel' }}
        </button>
        <b-button
          class="capture-start"
          type="is-success"
          native-type="submit"
          :loading="starting"
          :disabled="!startable">
          Start capture
        </b-button>
      </footer>
    </form>
  </b-modal>
</template>

<script>
  import * as experimentsApi from '@/api/experiments.js';
  import { errorLine } from '@/utils/operations.js';
  import {
    CAPTURE_FORMATS,
    DEFAULT_SNAPLEN,
    FORMAT_HELP,
    budgetFull,
    budgetNotices,
    defaultCaptureName,
    filterProblem,
    snaplenProblem,
  } from './experimentCaptures.js';

  let dialogs = 0;

  const emptyForm = (vlan) => ({
    vlans: vlan ? [vlan] : [],
    filter: '',
    snaplen: String(DEFAULT_SNAPLEN),
    format: CAPTURE_FORMATS[0],
    name: '',
  });

  // The dialog that starts an experiment capture: the traffic of the
  // experiment's VLANs on every host, merged into one file on the head
  // node. It warns of the Open vSwitch mirror budget from the capture list,
  // and refuses to start all VLANs where no mirror is left. It sends the
  // request itself: `starting` comes before it, and `started` with the
  // capture (or null when it failed) after.
  export default {
    name: 'ExperimentCaptureModal',
    props: {
      active: { type: Boolean, default: false },
      experiment: { type: String, required: true },
      // the experiment's VLANs, each { alias, vlan }
      vlans: { type: Array, default: () => [] },
      // the VLAN alias the dialog opens with, or '' for all VLANs
      vlan: { type: String, default: '' },
      // the budget rows of the capture list
      budget: { type: Array, default: () => [] },
    },
    emits: ['update:active', 'starting', 'started'],
    setup() {
      return {
        CAPTURE_FORMATS,
        FORMAT_HELP,
        uid: `experiment-capture-${++dialogs}`,
      };
    },
    data() {
      return {
        form: emptyForm(this.vlan),
        openedAt: new Date(),
        starting: false,
        error: '',
      };
    },
    computed: {
      title() {
        return this.vlan
          ? 'Capture VLAN traffic'
          : 'Capture all experiment traffic';
      },
      // the experiment's VLANs, plus the one the dialog opened with if the
      // list lacks it
      vlanChoices() {
        const list = this.vlans.filter((v) => v?.alias);
        if (!this.vlan || list.some((v) => v.alias === this.vlan)) return list;
        return [{ alias: this.vlan }, ...list];
      },
      problems() {
        return {
          filter: filterProblem(this.form.filter),
          snaplen: snaplenProblem(this.form.snaplen),
        };
      },
      notices() {
        return budgetNotices(this.budget, this.experiment, {
          someVlans: this.form.vlans.length > 0,
        });
      },
      // The rows do not say which VLANs use a bridge, so only a capture of
      // all VLANs surely needs a full one; the server refuses the others.
      full() {
        return this.form.vlans.length === 0 && budgetFull(this.budget);
      },
      defaultName() {
        return defaultCaptureName(
          this.experiment,
          this.form.vlans,
          this.form.format,
          this.openedAt,
        );
      },
      startable() {
        return (
          !this.starting &&
          !this.full &&
          !this.problems.filter &&
          !this.problems.snaplen
        );
      },
    },
    watch: {
      active(on) {
        if (!on) return;
        this.form = emptyForm(this.vlan);
        this.openedAt = new Date();
        this.error = '';
      },
    },
    methods: {
      async start() {
        if (!this.startable) return;
        this.starting = true;
        this.error = '';
        this.$emit('starting');
        let started = null;
        try {
          const resp = await experimentsApi.startExperimentCapture(
            this.experiment,
            {
              vlans: [...this.form.vlans],
              filter: this.form.filter.trim(),
              snaplen: Number(this.form.snaplen),
              name: this.form.name.trim(),
              format: this.form.format,
            },
          );
          started = resp.data?.capture ?? null;
          const warnings = resp.data?.warnings ?? [];
          const id = started?.id ? ` ${started.id}` : '';
          this.$buefy.toast.open({
            message: [`Started experiment capture${id}.`, ...warnings].join(
              ' ',
            ),
            type: warnings.length ? 'is-warning' : 'is-success',
          });
          this.$emit('update:active', false);
        } catch (err) {
          this.error = errorLine(err);
        } finally {
          this.starting = false;
          this.$emit('started', started);
        }
      },
    },
  };
</script>

<style scoped>
  .experiment-capture-card {
    width: 36rem;
  }

  .capture-vlans {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
    max-height: 9rem;
    overflow-y: auto;
  }

  .capture-format-choices {
    display: flex;
    gap: 1.5rem;
  }

  .capture-trunk-note {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .capture-vlans-field {
    min-width: 0;
    margin: 0 0 0.75rem;
    padding: 0;
    border: 0;
  }
</style>
