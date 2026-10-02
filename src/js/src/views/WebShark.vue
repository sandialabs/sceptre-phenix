<template>
  <div class="webshark-page">
    <div v-if="!installed" class="webshark-missing">
      <p v-if="!featuresLoaded">Loading…</p>
      <template v-else>
        <p>WebShark is not installed on this phēnix server.</p>
        <p>
          See the
          <a :href="docsPage('webshark')" target="_blank" rel="noopener"
            >WebShark documentation</a
          >
          for how to install it.
        </p>
      </template>
    </div>
    <template v-else>
      <div class="webshark-bar">
        <span class="webshark-label">Experiment</span>
        <b-select
          class="webshark-exp"
          size="is-small"
          placeholder="Pick an experiment"
          aria-label="Experiment"
          :model-value="exp"
          :loading="!experimentsLoaded"
          @update:model-value="pickExperiment">
          <option v-for="e in experimentOptions" :key="e.name" :value="e.name">
            {{ e.name }}{{ e.running ? ' (running)' : '' }}
          </option>
        </b-select>
        <span class="webshark-label">Capture</span>
        <b-select
          class="webshark-capture"
          size="is-small"
          :placeholder="capturePlaceholder"
          aria-label="Capture"
          :model-value="selected ? selected.key : null"
          :loading="listsLoading"
          :disabled="!exp"
          @update:model-value="pickCapture">
          <optgroup v-if="liveOptions.length" label="Live captures">
            <option v-for="t in liveOptions" :key="t.key" :value="t.key">
              {{ t.label }}
            </option>
          </optgroup>
          <optgroup v-if="fileOptions.length" label="Saved files">
            <option v-for="t in fileOptions" :key="t.key" :value="t.key">
              {{ t.label
              }}{{ t.size != null ? ` (${formatFileSize(t.size)})` : '' }}
            </option>
          </optgroup>
        </b-select>
        <b-tooltip label="Reload the captures" type="is-dark" :delay="400">
          <b-button
            class="webshark-reload"
            size="is-small"
            type="is-light"
            icon-left="sync-alt"
            aria-label="Reload the captures"
            :disabled="!exp || listsLoading"
            @click="reload" />
        </b-tooltip>
        <b-tag v-if="capture && capture.live" type="is-danger" rounded>
          Live
        </b-tag>
        <div class="webshark-bar-end">
          <a
            v-if="capture"
            class="button is-light is-small webshark-new-tab"
            :href="newTabURL"
            target="_blank"
            rel="noopener">
            <b-icon icon="up-right-from-square" size="is-small" />
            <span>Open in new tab</span>
          </a>
          <b-tooltip
            v-if="streamTarget"
            label="Copy a command that streams this capture into a local Wireshark"
            type="is-dark"
            position="is-bottom"
            multilined>
            <b-button
              class="webshark-copy"
              size="is-small"
              type="is-light"
              icon-left="copy"
              @click="copyWiresharkCommand">
              Copy Wireshark command
            </b-button>
          </b-tooltip>
          <b-tooltip
            label="WebShark documentation"
            type="is-dark"
            position="is-left"
            :delay="400">
            <a
              class="button is-light is-small"
              :href="docsPage('webshark')"
              target="_blank"
              rel="noopener"
              aria-label="WebShark documentation">
              <b-icon icon="book" size="is-small" />
            </a>
          </b-tooltip>
        </div>
      </div>
      <!-- sized to the room left in the window (fitFrame) -->
      <div ref="frame" class="webshark-frame" :style="{ height: frameHeight }">
        <iframe
          v-if="capture"
          :key="capture.capture"
          :src="frameURL"
          :title="capture.title || 'WebShark'"
          @load="frameLoading = false"></iframe>
        <div v-else class="webshark-empty">{{ emptyText }}</div>
        <b-loading
          :is-full-page="false"
          :model-value="opening || frameLoading"
          :can-cancel="false" />
      </div>
    </template>
  </div>
</template>

<script>
  import { usePhenixStore } from '@/store.js';
  import axiosInstance from '@/utils/axios.js';
  import { docsPage } from '@/utils/docs.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { cachedPage } from '@/utils/pageCache.js';
  import { experimentKey, experimentList } from '@/utils/pageData.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { roomInWindow } from '@/utils/viewportFit.js';
  import { vmInterfaces } from '@/components/experiment/vmActions.js';
  import {
    captureStreamCLI,
    captureStreamURL,
    fileTarget,
    isCaptureFile,
    liveTarget,
    openFailure,
    openInWebShark,
    queryTarget,
    signInToken,
    webSharkInstalled,
    webSharkURL,
    wiresharkCommand,
  } from '@/utils/webshark.js';

  // the targets a picker group lists, plus the one the URL names when the
  // lists do not have it (not loaded yet, or a capture that has stopped), so
  // the picker shows it
  const withSelected = (targets, selected, live) =>
    selected &&
    selected.live === live &&
    !targets.some((t) => t.key === selected.key)
      ? [...targets, selected]
      : targets;

  // The WebShark page: pick an experiment and one of its running captures or
  // saved capture files, and WebShark shows its packets in a frame. The URL
  // names the pick (see queryTarget), so it can be shared and reloaded.
  export default {
    mixins: [formattingMixin],
    setup() {
      return { docsPage };
    },

    created() {
      if (this.installed) this.init();
    },

    mounted() {
      window.addEventListener('resize', this.fitFrame);
      this.fitFrame();
    },

    beforeUnmount() {
      window.removeEventListener('resize', this.fitFrame);
      // answers still on their way are for a page no longer shown
      this.listsRequest = (this.listsRequest ?? 0) + 1;
      this.openRequest = (this.openRequest ?? 0) + 1;
      this.left = true;
    },

    watch: {
      // the server's features can arrive after the page opens
      installed(on) {
        if (!on) return;
        this.init();
        this.$nextTick(this.fitFrame);
      },
      '$route.query'() {
        if (this.started) this.applyQuery();
      },
    },

    computed: {
      installed() {
        return webSharkInstalled(usePhenixStore().features);
      },
      featuresLoaded() {
        return !!usePhenixStore().featuresLoaded;
      },
      // the experiment the URL names stays pickable when the list lacks it
      experimentOptions() {
        if (!this.exp || this.experiments.some((e) => e.name === this.exp)) {
          return this.experiments;
        }
        return [{ name: this.exp, running: false }, ...this.experiments];
      },
      liveOptions() {
        return withSelected(this.liveCaptures, this.selected, true);
      },
      fileOptions() {
        return withSelected(this.savedFiles, this.selected, false);
      },
      frameURL() {
        return this.capture ? webSharkURL(this.capture.capture) : null;
      },
      newTabURL() {
        return this.capture
          ? webSharkURL(this.capture.capture, { embed: false })
          : null;
      },
      // the running capture the Wireshark command streams: the picked one, or
      // the one still writing the picked file
      streamTarget() {
        const selected = this.selected;
        if (!this.capture?.live || !selected) return null;
        if (selected.live) return selected;
        return (
          this.liveCaptures.find((c) =>
            c.filepath?.endsWith(`/files/${selected.path}`),
          ) ?? null
        );
      },
      capturePlaceholder() {
        if (!this.exp) return 'Pick an experiment first';
        if (this.listsLoading) return 'Loading captures…';
        if (!this.liveOptions.length && !this.fileOptions.length) {
          return 'No captures';
        }
        return 'Pick a capture';
      },
      emptyText() {
        if (this.failure) return this.failure;
        if (this.opening) return `Opening ${this.selected?.label}…`;
        if (!this.exp) {
          return 'Pick an experiment, then one of its live captures or saved capture files to view its packets.';
        }
        if (this.listsLoading) return 'Loading captures…';
        if (!this.liveOptions.length && !this.fileOptions.length) {
          return `${this.exp} has no live captures or saved capture files.`;
        }
        return 'Pick a live capture or a saved capture file to view its packets.';
      },
    },

    methods: {
      init() {
        if (this.started) return;
        this.started = true;
        this.loadExperiments();
        this.applyQuery();
      },

      async loadExperiments() {
        try {
          if (roleAllowed('experiments', 'list')) {
            const list = await experimentList();
            this.experiments = (list ?? [])
              .map(({ name, running }) => ({ name, running: !!running }))
              .sort((a, b) => a.name.localeCompare(b.name));
          }
        } catch (err) {
          if (!this.left) useErrorNotification(err);
        } finally {
          this.experimentsLoaded = true;
        }
      },

      // Follows the page's URL, which the pickers set: lists the named
      // experiment's captures and opens the named capture.
      applyQuery() {
        if (this.$route.name !== 'webshark') return;
        const { exp, target } = queryTarget(this.$route.query);

        if (exp !== this.exp) {
          this.exp = exp;
          this.liveCaptures = [];
          this.savedFiles = [];
          this.loadLists();
        }
        if ((target?.key ?? null) !== (this.selected?.key ?? null)) {
          this.selected = target;
          this.openSelected();
        }
      },

      pickExperiment(name) {
        this.$router.replace({ query: name ? { exp: name } : {} });
      },

      pickCapture(key) {
        const target = [...this.liveOptions, ...this.fileOptions].find(
          (t) => t.key === key,
        );
        if (!target) return;
        this.$router.replace({ query: { exp: this.exp, ...target.query } });
      },

      // lists the captures again, and retries a capture that failed to open
      reload() {
        this.loadLists();
        if (this.failure) this.openSelected();
      },

      async loadLists() {
        const exp = this.exp;
        const request = (this.listsRequest = (this.listsRequest ?? 0) + 1);
        if (!exp) {
          this.listsLoading = false;
          return;
        }

        this.listsLoading = true;
        const [live, files] = await Promise.allSettled([
          this.fetchLiveCaptures(exp),
          this.fetchSavedFiles(exp),
        ]);
        if (request !== this.listsRequest) return;

        this.listsLoading = false;
        if (live.status === 'fulfilled') {
          this.liveCaptures = live.value;
        } else {
          useErrorNotification(live.reason);
        }
        if (files.status === 'fulfilled') {
          this.savedFiles = files.value;
        } else {
          useErrorNotification(files.reason);
        }
        this.$nextTick(this.fitFrame);
      },

      // the experiment's running captures the role may open, named after
      // their interface's network when the experiment page has listed the VM
      async fetchLiveCaptures(exp) {
        if (!roleAllowed('experiments/captures', 'list', exp)) return [];

        const resp = await axiosInstance.get(
          `experiments/${encodeURIComponent(exp)}/captures`,
        );
        const vms = cachedPage(experimentKey(exp))?.data?.vms ?? [];
        return (resp.data?.captures ?? [])
          .filter((c) => roleAllowed('vms/captures', 'list', `${exp}/${c.vm}`))
          .map((c) => {
            const vm = vms.find((v) => v.name === c.vm);
            const network = vm ? vmInterfaces(vm)[c.interface]?.network : null;
            return {
              ...liveTarget(c.vm, c.interface, network),
              filepath: c.filepath,
            };
          })
          .sort((a, b) => a.vm.localeCompare(b.vm) || a.iface - b.iface);
      },

      // the experiment's capture files, newest first
      async fetchSavedFiles(exp) {
        if (
          !roleAllowed('experiments/files', 'list', exp) ||
          !roleAllowed('experiments/files', 'get', exp)
        ) {
          return [];
        }

        const resp = await axiosInstance.get(
          `experiments/${encodeURIComponent(exp)}/files`,
          { params: { sortCol: 'date', sortDir: 'desc' } },
        );
        return (resp.data?.files ?? [])
          .filter((f) => !f.isDir && isCaptureFile(f.name))
          .map((f) => fileTarget(f.path, f.size));
      },

      // has the server ready the picked capture, then frames it
      async openSelected() {
        const target = this.selected;
        const exp = this.exp;
        const request = (this.openRequest = (this.openRequest ?? 0) + 1);
        this.capture = null;
        this.failure = null;
        this.frameLoading = false;

        if (!target || !exp) {
          this.opening = false;
          return;
        }

        this.opening = true;
        try {
          const capture = await openInWebShark(exp, target);
          if (!capture?.capture) {
            throw new Error('the server did not name the capture to open');
          }
          if (request !== this.openRequest) return;
          this.capture = capture;
          this.frameLoading = true;
        } catch (err) {
          if (request !== this.openRequest) return;
          this.failure = `Could not open ${target.label}. ${openFailure(err)}`;
          useErrorNotification(err);
        } finally {
          if (request === this.openRequest) this.opening = false;
        }
        // the toolbar's buttons change with the capture
        this.$nextTick(this.fitFrame);
      },

      async copyWiresharkCommand() {
        const target = this.streamTarget;
        if (!target) return;
        const command = wiresharkCommand(
          captureStreamURL(
            this.exp,
            target.vm,
            target.iface,
            window.location.origin,
          ),
          signInToken(usePhenixStore().token),
        );

        let copied = false;
        try {
          // there is no Clipboard API on a page served over plain http
          if (!navigator.clipboard) throw new Error('no clipboard');
          await navigator.clipboard.writeText(command);
          copied = true;
        } catch {
          // shown below instead
        }

        // Behind a sign-in proxy, the command reaches phēnix only if it signs
        // in to the proxy too, which the page cannot do for it.
        const proxy = import.meta.env.VITE_AUTH === 'proxy';
        if (copied && !proxy) {
          this.$buefy.toast.open({
            message: 'Copied the Wireshark command',
            type: 'is-success',
            duration: 4000,
          });
          return;
        }

        const block = (text) =>
          '<code style="display: block; margin: 0.75rem 0;' +
          ' white-space: pre-wrap; word-break: break-all;' +
          ` user-select: all">${escapeHTML(text)}</code>`;
        let message =
          (copied
            ? 'Copied this command, which streams the capture into a local Wireshark:'
            : 'Run this command to stream the capture into a local Wireshark:') +
          block(command);
        if (proxy) {
          message +=
            'phēnix is behind a sign-in proxy, which turns the command away' +
            ' unless it signs in to the proxy the way your browser does,' +
            " usually with the proxy's sign-in cookie. Copy that cookie from" +
            " your browser's developer tools and add it to the command as" +
            " <code>-b 'NAME=VALUE'</code>. On the phēnix server, this" +
            ' command streams the capture without going through the proxy:' +
            block(captureStreamCLI(this.exp, target.vm, target.iface));
        }

        this.$buefy.dialog.alert({
          title: copied ? 'Copied the Wireshark command' : 'Wireshark command',
          message,
          confirmText: 'Close',
        });
      },

      // Sizes the frame to the room left in the window, so WebShark scrolls
      // inside it and the page never does.
      fitFrame() {
        const room = roomInWindow(this.$refs.frame);
        if (room == null) return;
        this.frameHeight = `${Math.max(Math.floor(room), 300)}px`;
      },
    },

    data() {
      return {
        // { name, running } of the experiments the role may list, by name
        experiments: [],
        experimentsLoaded: false,
        // the experiment the URL names
        exp: null,
        // what the capture picker lists (see utils/webshark.js)
        liveCaptures: [],
        savedFiles: [],
        listsLoading: false,
        // the target the URL names
        selected: null,
        // the server's answer for it: { capture, live, title }
        capture: null,
        opening: false,
        frameLoading: false,
        // why the picked capture could not be opened
        failure: null,
        // sized by fitFrame; null until measured
        frameHeight: null,
        // whether the page has started following its URL
        started: false,
      };
    },
  };
</script>

<style scoped>
  .webshark-missing {
    padding: 2rem 1rem;
    text-align: center;
  }

  .webshark-missing p + p {
    margin-top: 0.5rem;
  }

  /* experiment and capture pickers, and what can be done with the capture */
  .webshark-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    padding: 0.45rem 0.6rem;
    background: #484848;
    border-bottom: 1px solid #6e6e6e;
  }

  .webshark-label {
    color: #cfcfcf;
    font-size: 0.85rem;
  }

  .webshark-label + .webshark-exp,
  .webshark-label + .webshark-capture {
    margin-left: -0.15rem;
  }

  .webshark-exp + .webshark-label {
    margin-left: 0.5rem;
  }

  .webshark-capture :deep(select) {
    max-width: 36rem;
  }

  .webshark-bar-end {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .webshark-frame {
    position: relative;
    height: 600px;
    background: #2b2b2b;
  }

  .webshark-frame iframe {
    display: block;
    width: 100%;
    height: 100%;
    border: 0;
    background: white;
  }

  .webshark-empty {
    display: flex;
    height: 100%;
    align-items: center;
    justify-content: center;
    padding: 1rem;
    color: #cfcfcf;
    text-align: center;
  }
</style>
