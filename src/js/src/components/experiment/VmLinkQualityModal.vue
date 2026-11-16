<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    has-modal-card
    ref="modal">
    <div class="modal-card link-quality-card">
      <header class="modal-card-head">
        <h2 class="modal-card-title">
          Link quality of interface {{ index }} of {{ vmName }}
        </h2>
      </header>
      <section class="modal-card-body">
        <p class="link-quality-about">
          This shapes the traffic that the VM receives on
          <span class="is-family-monospace">{{ network }}</span
          >. Rate cannot combine with loss or delay.
        </p>
        <p v-if="loading" class="link-quality-state" role="status">
          <span class="capture-spinner"></span>
          Loading the link quality…
        </p>
        <p
          v-else-if="loadError"
          class="link-quality-state has-text-danger"
          role="alert">
          {{ loadError }}
        </p>
        <b-field
          label="Loss (%)"
          label-for="link-quality-loss"
          :type="problems.loss ? 'is-danger' : undefined"
          :message="problems.loss">
          <b-input
            id="link-quality-loss"
            v-model="form.loss"
            :compat-fallthrough="false"
            inputmode="decimal"
            placeholder="5" />
        </b-field>
        <b-field
          label="Delay (us, ms or s)"
          label-for="link-quality-delay"
          :type="problems.delay ? 'is-danger' : undefined"
          :message="problems.delay">
          <b-input
            id="link-quality-delay"
            v-model="form.delay"
            :compat-fallthrough="false"
            placeholder="100ms" />
        </b-field>
        <b-field
          label="Rate (1 to 999999999)"
          label-for="link-quality-rate"
          :type="problems.rate ? 'is-danger' : undefined"
          :message="problems.rate"
          :addons="false">
          <div class="field has-addons">
            <b-input
              id="link-quality-rate"
              v-model="form.rate"
              :compat-fallthrough="false"
              inputmode="numeric"
              placeholder="10"
              expanded />
            <b-select
              v-model="form.unit"
              :compat-fallthrough="false"
              aria-label="Rate unit">
              <option v-for="unit in RATE_UNITS" :key="unit" :value="unit">
                {{ unit }}
              </option>
            </b-select>
          </div>
        </b-field>
        <p
          class="help link-quality-problem"
          :class="{ 'is-danger': combined }"
          aria-live="polite">
          {{ problems.form }}
        </p>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="$refs.modal.cancel('x')">
          Cancel
        </button>
        <b-button
          class="link-quality-clear"
          type="is-danger"
          outlined
          :loading="clearing"
          :disabled="!wasSet || busy"
          @click="clear">
          Clear
        </b-button>
        <b-button
          class="link-quality-apply"
          type="is-success"
          :loading="applying"
          :disabled="!valid || busy"
          @click="apply">
          Apply
        </b-button>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  import * as vmsApi from '@/api/vms.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import {
    RATE_UNITS,
    qosBody,
    qosForm,
    qosProblems,
    qosSet,
  } from './linkQuality.js';

  // The dialog that sets or clears the link quality (minimega qos) of one
  // VM interface. It reads the values set when it opens, and sends the
  // requests itself.
  export default {
    name: 'VmLinkQualityModal',
    setup() {
      return { RATE_UNITS };
    },
    props: {
      active: { type: Boolean, default: false },
      experiment: { type: String, required: true },
      vmName: { type: String, default: '' },
      index: { type: Number, default: 0 },
      // the interface's network, as minimega names it
      network: { type: String, default: '' },
    },
    emits: ['update:active'],
    data() {
      return {
        form: qosForm(null),
        wasSet: false,
        loading: false,
        loadError: '',
        applying: false,
        clearing: false,
        // counts the loads, so an answer to an older one is dropped
        loads: 0,
      };
    },
    computed: {
      problems() {
        return qosProblems(this.form);
      },
      valid() {
        return Object.values(this.problems).every((p) => !p);
      },
      // whether the form sets a rate with a loss or a delay
      combined() {
        return !!this.problems.form && !!this.form.rate.trim();
      },
      busy() {
        return this.loading || this.applying || this.clearing;
      },
    },
    watch: {
      active: {
        immediate: true,
        handler(on) {
          if (on) this.load();
        },
      },
    },
    methods: {
      async load() {
        const load = ++this.loads;
        this.form = qosForm(null);
        this.wasSet = false;
        this.loadError = '';
        this.loading = true;
        try {
          const resp = await vmsApi.getVmQos(this.experiment, this.vmName);
          if (load !== this.loads) return;
          const entry = (resp.data?.interfaces ?? []).find(
            (i) => i.interface === this.index,
          );
          this.form = qosForm(entry);
          this.wasSet = qosSet(entry);
        } catch (err) {
          if (load !== this.loads) return;
          this.loadError =
            err?.response?.data?.message ??
            'The link quality could not be read.';
        } finally {
          if (load === this.loads) this.loading = false;
        }
      },

      async apply() {
        this.applying = true;
        try {
          await vmsApi.setVmQos(
            this.experiment,
            this.vmName,
            this.index,
            qosBody(this.form),
          );
          this.done(`Link quality set on interface ${this.index}`);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.applying = false;
        }
      },

      async clear() {
        this.clearing = true;
        try {
          await vmsApi.clearVmQos(this.experiment, this.vmName, this.index);
          this.done(`Link quality cleared on interface ${this.index}`);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.clearing = false;
        }
      },

      done(what) {
        this.$buefy.toast.open({
          message: `${what} of the ${this.vmName} VM.`,
          type: 'is-success',
        });
        this.$emit('update:active', false);
      },
    },
  };
</script>

<style scoped>
  .link-quality-card {
    width: 32rem;
  }

  .link-quality-about,
  .link-quality-state {
    margin-bottom: 0.75rem;
  }

  .link-quality-state {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .link-quality-problem {
    min-height: 1.5em;
  }

  /* a small spinner while the dialog reads the link quality */
  .capture-spinner {
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: capture-spin 0.6s linear infinite;
  }

  @keyframes capture-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
