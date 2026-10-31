<!-- The body of the Disks page's details window for one disk image: a head
colored by the image's health, its sizes as tiles, then its format, partitions,
backing chain, the images built on it, the experiments that use it, its clone
jobs and its actions. It reads the details from GET disks/details when it
opens, and asks the page by event to run an action or open another image. -->
<template>
  <div class="modal-card disk-card">
    <header
      class="modal-card-head disk-card-head"
      :class="`has-health-${health}`">
      <div class="disk-card-heading">
        <div class="disk-card-title-row">
          <p ref="title" class="modal-card-title disk-card-title" tabindex="-1">
            {{ label }}
          </p>
          <b-tag
            :type="healthInfo.type"
            rounded
            class="disk-tag disk-health-tag">
            <b-icon :icon="healthInfo.icon" size="is-small" />
            <span class="is-sr-only">Health: </span>
            <span>{{ healthInfo.label }}</span>
          </b-tag>
          <b-tag class="disk-tag disk-kind-tag" title="Kind">
            <b-icon :icon="kindIcon" size="is-small" />
            <span class="is-sr-only">Kind: </span>
            <span>{{ disk.kind || 'Unknown' }}</span>
          </b-tag>
          <b-tag class="disk-tag disk-type-tag" title="Type">
            <b-icon icon="file-code" size="is-small" />
            <span class="is-sr-only">Type: </span>
            <span>{{ type }}</span>
          </b-tag>
          <b-tag
            v-if="disk.inUse"
            type="is-success"
            class="disk-tag disk-in-use-tag"
            :title="`In use by ${runningText}`">
            <b-icon icon="play-circle" size="is-small" />
            <span>In use</span>
          </b-tag>
          <b-tag
            v-if="disk.readOnly"
            type="is-dark"
            class="disk-tag disk-read-only-tag"
            title="phēnix shows this image but does not change it">
            <b-icon icon="ban" size="is-small" />
            <span>Read-only</span>
          </b-tag>
        </div>
        <div class="disk-card-meta">
          <span class="disk-meta-path">
            <b-icon icon="hdd" size="is-small" />
            <span class="is-sr-only">Full path: </span>
            <span class="disk-mono disk-full-path">{{ disk.fullPath }}</span>
            <b-tooltip
              :label="copyLabel('path')"
              type="is-dark"
              position="is-bottom"
              append-to-body>
              <button
                type="button"
                class="button is-light is-small disk-copy disk-copy-path"
                :aria-label="COPY.path.label"
                @click="copy('path', disk.fullPath)">
                <b-icon :icon="copyIcon('path')" size="is-small" />
              </button>
            </b-tooltip>
          </span>
          <span class="disk-location">
            <b-icon icon="folder" size="is-small" />
            <span class="is-sr-only">Location: </span>
            <span class="disk-location-text">{{ locationText }}</span>
            <DiskWarning
              v-if="disk.outsideFilesDir"
              :reasons="['outside']"
              :files-dir="filesDir" />
          </span>
          <span v-if="shown?.modified" class="disk-modified">
            <b-icon icon="clock" size="is-small" />
            <span>Modified</span>
            <TimeWithAge :time="shown.modified" />
          </span>
        </div>
      </div>
      <div class="disk-card-links">
        <b-tooltip
          label="Disks documentation"
          type="is-dark"
          position="is-left"
          append-to-body>
          <a
            class="button is-light is-small disk-card-docs"
            aria-label="Disks documentation"
            :href="docsPage('disks')"
            target="_blank"
            rel="noopener">
            <b-icon icon="book" />
          </a>
        </b-tooltip>
      </div>
    </header>

    <section class="modal-card-body disk-card-body">
      <p class="is-sr-only disk-copy-status" role="status">
        {{ copyStatus }}
      </p>

      <div
        v-if="problems.length"
        class="disk-problems"
        :class="`is-${health}`"
        aria-label="Problems"
        role="group">
        <p class="disk-section-title">Problems</p>
        <ul>
          <li
            v-for="problem in problems"
            :key="problem.text"
            :class="`is-${problem.level}`">
            <b-icon :icon="problem.icon" size="is-small" />
            <span>{{ problem.text }}</span>
          </li>
        </ul>
      </div>

      <div class="disk-section actions">
        <p class="disk-section-title">
          <b-icon icon="bolt" size="is-small" />
          Actions
        </p>
        <div class="disk-action-buttons">
          <b-tooltip
            v-for="action in actions"
            :key="action.name"
            :label="action.tooltip"
            :active="action.disabled"
            type="is-dark"
            multilined>
            <b-button
              :class="['disk-action', `disk-action-${action.name}`]"
              :type="action.name === 'delete' ? 'is-danger' : 'is-light'"
              :outlined="action.name === 'delete'"
              :icon-left="action.icon"
              :aria-label="action.label"
              :aria-describedby="`${id}-${action.name}`"
              :aria-disabled="action.disabled ? 'true' : undefined"
              expanded
              @click="action.disabled || $emit('action', action.name)">
              <span class="disk-action-label">{{ action.label }}</span>
              <span
                :id="`${id}-${action.name}`"
                class="disk-action-description">
                {{ action.disabled ? action.tooltip : action.description }}
              </span>
            </b-button>
          </b-tooltip>
        </div>
        <div class="disk-command">
          <span class="disk-command-name">qemu-img info</span>
          <code class="disk-command-text info-command">{{ infoCommand }}</code>
          <b-tooltip
            :label="copyLabel('info')"
            type="is-dark"
            position="is-left"
            append-to-body>
            <button
              type="button"
              class="button is-light is-small disk-copy disk-copy-info"
              :aria-label="COPY.info.label"
              @click="copy('info', infoCommand)">
              <b-icon :icon="copyIcon('info')" size="is-small" />
            </button>
          </b-tooltip>
        </div>
      </div>

      <div class="columns is-desktop disk-columns">
        <div class="column is-6-desktop">
          <div class="disk-stats" aria-label="Sizes and layout" role="group">
            <div
              v-for="stat in stats"
              :key="stat.key"
              class="disk-stat"
              :class="[`disk-stat-${stat.key}`, stat.class]">
              <b-icon :icon="stat.icon" />
              <div class="disk-stat-text">
                <p class="disk-stat-value">{{ stat.value }}</p>
                <p class="disk-stat-label">{{ stat.label }}</p>
                <div
                  v-if="stat.bar !== undefined"
                  class="disk-stat-bar"
                  aria-hidden="true">
                  <span
                    :style="{
                      width: `${Number(stat.bar.toFixed(2))}%`,
                    }"></span>
                </div>
              </div>
            </div>
          </div>

          <div class="disk-section disk-format-section">
            <p class="disk-section-title">
              <b-icon icon="file-code" size="is-small" />
              Format and health
            </p>
            <dl class="disk-kv">
              <div>
                <dt>Type</dt>
                <dd><DiskFormat :disk="disk" /></dd>
              </div>
              <div v-for="row in formatRows" :key="row.key">
                <dt>{{ row.term }}</dt>
                <dd :class="row.class" :title="row.title">{{ row.value }}</dd>
              </div>
            </dl>
            <b-taglist v-if="features.length" class="disk-features">
              <b-tag
                v-for="feature in features"
                :key="feature.label"
                :type="feature.type"
                class="disk-tag">
                <b-icon :icon="feature.icon" size="is-small" />
                <span>{{ feature.label }}</span>
              </b-tag>
            </b-taglist>
            <p v-if="shown?.fileUnavailable" class="disk-unavailable">
              <b-icon icon="info-circle" size="is-small" />
              <span> <b>Not available:</b> {{ shown.fileUnavailable }}. </span>
            </p>
            <div
              v-if="formatLevel"
              class="format-issue"
              :class="`is-${formatLevel}`">
              <p class="format-problem">
                <b-icon
                  :icon="
                    formatLevel === 'error'
                      ? 'times-circle'
                      : 'exclamation-triangle'
                  "
                  size="is-small"
                  :class="
                    formatLevel === 'error'
                      ? 'broken-chain-icon'
                      : 'has-text-warning'
                  " />
                <b>{{ formatIssueName(disk) }}:</b>
                {{ disk.formatIssue.problem }}.
              </p>
              <p class="format-fix">
                {{ disk.formatIssue.fix
                }}{{ disk.formatIssue.command ? ':' : '.' }}
              </p>
              <div v-if="disk.formatIssue.command" class="disk-command">
                <code class="disk-command-text format-command">{{
                  disk.formatIssue.command
                }}</code>
                <b-tooltip
                  :label="copyLabel('fix')"
                  type="is-dark"
                  position="is-left"
                  append-to-body>
                  <button
                    type="button"
                    class="button is-light is-small disk-copy disk-copy-fix"
                    :aria-label="COPY.fix.label"
                    @click="copy('fix', disk.formatIssue.command)">
                    <b-icon :icon="copyIcon('fix')" size="is-small" />
                  </button>
                </b-tooltip>
              </div>
              <p>
                <a
                  class="format-docs"
                  :href="FORMATS_DOCS"
                  target="_blank"
                  rel="noopener noreferrer"
                  >The fix for each image format (phēnix docs)</a
                >
              </p>
            </div>
          </div>

          <div class="disk-section disk-partitions-section">
            <p class="disk-section-title">
              <b-icon icon="table-columns" size="is-small" />
              Partitions
            </p>
            <p v-if="unavailable('partitions')" class="disk-unavailable">
              <b-icon icon="info-circle" size="is-small" />
              <span>
                <b>Not available:</b> {{ unavailable('partitions') }}.
              </span>
            </p>
            <p v-else-if="!shown" class="disk-loading">Reading the image…</p>
            <template v-else>
              <p class="disk-table-summary">{{ tableSummary }}</p>
              <p v-if="wholeDiskText" class="disk-whole-disk">
                {{ wholeDiskText }}
              </p>
              <div
                v-if="partitions.length"
                class="disk-table-wrap"
                tabindex="0"
                role="region"
                :aria-label="`Partitions of ${label}`">
                <table class="table is-narrow is-fullwidth disk-partitions">
                  <caption class="is-sr-only">
                    {{
                      `Partitions of ${label}`
                    }}
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">#</th>
                      <th scope="col">Type</th>
                      <th scope="col">File system</th>
                      <th scope="col">Start</th>
                      <th scope="col">Size</th>
                      <th scope="col">Flags</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr
                      v-for="part in partitions"
                      :key="part.number"
                      :class="{
                        'is-inject': part.injectVms.length,
                        'is-logical': part.logical,
                      }">
                      <td class="disk-part-number">{{ part.number }}</td>
                      <td>
                        <span>{{ part.type || 'Unknown' }}</span>
                        <span
                          v-if="part.typeText"
                          class="disk-part-id disk-mono">
                          {{ part.typeText }}
                        </span>
                        <span v-if="part.name" class="disk-part-name">
                          “{{ part.name }}”
                        </span>
                      </td>
                      <td>
                        <span>{{ part.fsText }}</span>
                        <span
                          v-if="part.filesystem?.label"
                          class="disk-part-name">
                          “{{ part.filesystem.label }}”
                        </span>
                      </td>
                      <td class="disk-mono">{{ part.startText }}</td>
                      <td class="disk-mono">{{ part.sizeText }}</td>
                      <td class="disk-part-flags">
                        <b-tag
                          v-if="part.bootable"
                          type="is-dark"
                          class="disk-tag">
                          <b-icon icon="flag" size="is-small" />
                          <span>Boot</span>
                        </b-tag>
                        <b-tag
                          v-if="part.extended"
                          type="is-dark"
                          class="disk-tag">
                          Extended
                        </b-tag>
                        <b-tag
                          v-if="part.logical"
                          type="is-dark"
                          class="disk-tag">
                          Logical
                        </b-tag>
                        <b-tag
                          v-if="part.injectVms.length"
                          class="disk-tag disk-inject-tag"
                          :title="`Inject partition of ${listText(part.injectVms)}`">
                          <b-icon icon="file-download" size="is-small" />
                          <span>Inject</span>
                          <span class="is-sr-only">
                            partition of {{ listText(part.injectVms) }}
                          </span>
                        </b-tag>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <ul
                v-if="injectWarnings.length || notes.length"
                class="disk-notes">
                <li
                  v-for="problem in injectWarnings"
                  :key="problem.number"
                  class="disk-inject-problem">
                  <b-icon
                    icon="exclamation-triangle"
                    size="is-small"
                    class="has-text-warning" />
                  <span>{{ problem.text }}</span>
                </li>
                <li v-for="note in notes" :key="note" class="disk-note">
                  <b-icon icon="info-circle" size="is-small" />
                  <span>{{ note }}.</span>
                </li>
              </ul>
            </template>
          </div>
        </div>

        <div class="column is-6-desktop">
          <div class="disk-section disk-chain-section">
            <p class="disk-section-title">
              <b-icon icon="layer-group" size="is-small" />
              Backing chain
            </p>
            <p v-if="disk.brokenChain" class="chain-problem">
              <b-icon
                icon="times-circle"
                size="is-small"
                class="broken-chain-icon" />
              A VM cannot start from this image: {{ chainProblem(disk) }}.
            </p>
            <p v-if="!chain.length && !disk.brokenChain" class="disk-empty">
              None: this image has no backing image.
            </p>
            <ol
              v-else-if="chain.length"
              class="disk-chain"
              aria-label="Backing chain">
              <li class="disk-chain-item is-self">
                <span class="disk-chain-name">{{ label }}</span>
                <span class="disk-chain-meta">
                  <span>This image</span>
                  <span v-if="disk.size">· {{ disk.size }}</span>
                </span>
              </li>
              <li
                v-for="link in chain"
                :key="link.path"
                class="disk-chain-item"
                :class="{ 'is-missing': link.missing }">
                <span class="disk-chain-arrow" aria-hidden="true">&darr;</span>
                <span class="is-sr-only">Backed by </span>
                <span v-if="link.missing" class="missing-backing">
                  <b-icon
                    icon="times-circle"
                    size="is-small"
                    class="broken-chain-icon" />
                  {{ link.path }} (missing)
                </span>
                <template v-else-if="link.disk">
                  <button
                    type="button"
                    class="plain-button backing-link"
                    @click="$emit('open', link.path)">
                    {{ diskLabel(link.disk) }}
                  </button>
                  <span class="disk-chain-meta">
                    {{ diskType(link.disk) }}
                    <span v-if="link.disk.size">· {{ link.disk.size }}</span>
                    <DiskWarning
                      v-if="diskReasons(link.disk).length"
                      :reasons="diskReasons(link.disk)"
                      :detail="chainProblem(link.disk)"
                      :files-dir="filesDir" />
                  </span>
                </template>
                <template v-else>
                  <span class="disk-chain-name disk-mono">{{ link.path }}</span>
                  <span class="disk-chain-meta">Not in your disk list</span>
                </template>
              </li>
            </ol>
          </div>

          <div class="disk-section disk-overlays-section">
            <p class="disk-section-title">
              <b-icon icon="share-nodes" size="is-small" />
              Built on this image
            </p>
            <p v-if="unavailable('details')" class="disk-unavailable">
              <b-icon icon="info-circle" size="is-small" />
              <span> <b>Not available:</b> {{ unavailable('details') }}. </span>
            </p>
            <p v-else-if="!shown" class="disk-loading">Reading the image…</p>
            <p v-else-if="!overlays.length" class="disk-empty">
              No listed image is built on this image.
            </p>
            <template v-else>
              <ul class="disk-list">
                <li v-for="overlay in overlays" :key="overlay.fullPath">
                  <b-icon icon="layer-group" size="is-small" />
                  <button
                    v-if="overlay.disk"
                    type="button"
                    class="plain-button backing-link overlay-link"
                    @click="$emit('open', overlay.fullPath)">
                    {{ diskLabel(overlay.disk) }}
                  </button>
                  <span v-else class="disk-mono">{{ overlay.fullPath }}</span>
                  <b-tag type="is-dark" class="disk-tag">
                    {{ overlay.direct ? 'Direct' : 'Through another image' }}
                  </b-tag>
                </li>
              </ul>
              <p class="disk-overlay-note">
                <b-icon
                  icon="exclamation-triangle"
                  size="is-small"
                  class="has-text-warning" />
                <span>
                  These images need this file. If you rename or delete it, they
                  stop working.
                </span>
              </p>
            </template>
          </div>

          <div class="disk-section disk-used-section">
            <p class="disk-section-title">
              <b-icon icon="desktop" size="is-small" />
              Used by
            </p>
            <p
              v-if="!uses.length && !shown && !unavailable('details')"
              class="disk-loading">
              Reading the image…
            </p>
            <p v-else-if="!uses.length" class="disk-empty">
              No experiment uses this image.
            </p>
            <ul v-else class="disk-list disk-uses">
              <li v-for="exp in uses" :key="exp.name" class="disk-use">
                <div class="disk-use-head">
                  <router-link
                    v-if="canViewExperiment(exp.name)"
                    class="disk-use-link"
                    :to="{ name: 'experiment', params: { id: exp.name } }">
                    {{ exp.name }}
                  </router-link>
                  <span v-else class="disk-use-name">{{ exp.name }}</span>
                  <b-tag
                    :type="exp.running ? 'is-success' : 'is-dark'"
                    rounded
                    class="disk-tag">
                    <b-icon
                      :icon="exp.running ? 'play-circle' : 'stop'"
                      size="is-small" />
                    <span>{{ exp.running ? 'Running' : 'Stopped' }}</span>
                  </b-tag>
                </div>
                <ul v-if="exp.vms.length" class="disk-use-vms">
                  <li v-for="use in exp.vms" :key="`${use.vm}-${use.drive}`">
                    <b-icon icon="desktop" size="is-small" />
                    <span>{{ vmUseText(use) }}</span>
                  </li>
                </ul>
                <p
                  v-else-if="shown && canViewExperiment(exp.name)"
                  class="disk-use-indirect">
                  Through an image built on this image
                </p>
                <p v-else-if="shown" class="disk-use-hidden">
                  Your role cannot see the VMs of this experiment
                </p>
              </li>
            </ul>
          </div>

          <div class="disk-section clones">
            <p class="disk-section-title">
              <b-icon icon="copy" size="is-small" />
              Clone jobs
            </p>
            <p v-if="!cloneJobs.length" class="disk-empty">No clone jobs.</p>
            <job-progress
              v-for="job in cloneJobs"
              :key="job.id"
              :job="job"
              class="mb-2"></job-progress>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<script>
  import { useId } from 'vue';

  import * as disksApi from '@/api/disks.js';
  import DiskFormat from '@/components/DiskFormat.vue';
  import DiskWarning from '@/components/DiskWarning.vue';
  import JobProgress from '@/components/jobs/JobProgress.vue';
  import TimeWithAge from '@/components/TimeWithAge.vue';
  import { bytesText } from '@/utils/bytes.js';
  import { diskRunningText } from '@/utils/diskActions.js';
  import {
    chainProblem,
    diskLabel,
    diskReasons,
    findDisk,
  } from '@/utils/diskChain.js';
  import {
    HEALTH,
    allocation,
    diskHealth,
    filesystemName,
    healthProblems,
    injectProblems,
    injectTargets,
    partitionTableName,
    qcowFeatures,
    qemuImgInfoCommand,
    usedBy,
  } from '@/utils/diskDetails.js';
  import {
    FORMATS_DOCS,
    diskType,
    formatIssueLevel,
    formatIssueName,
  } from '@/utils/diskFormat.js';
  import { docsPage } from '@/utils/docs.js';
  import { errorLine } from '@/utils/operations.js';
  import { listText, pluralWord } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';

  // what each copy button copies, its accessible name and what it says once
  // it has copied
  const COPY = {
    path: { label: 'Copy the full path', what: 'the full path' },
    info: {
      label: 'Copy the qemu-img info command',
      what: 'the qemu-img info command',
    },
    fix: { label: 'Copy the fix command', what: 'the fix command' },
  };

  // how long a copy button shows that it copied, in milliseconds
  const COPIED_MS = 2000;

  const KIND_ICONS = {
    VM: 'desktop',
    ISO: 'compact-disc',
    Container: 'cube',
  };

  const ENCRYPTION = { none: 'None', aes: 'AES', luks: 'LUKS' };

  const CONTAINER_HIDES = ['virtual', 'allocated', 'partitions', 'table'];

  export default {
    name: 'DiskDetailsCard',
    components: { DiskFormat, DiskWarning, JobProgress, TimeWithAge },
    props: {
      // the disk as the disk list gives it
      disk: { type: Object, required: true },
      // the disk list, which names the images in the chain and the overlays
      disks: { type: Array, default: () => [] },
      // the minimega files directory, when known
      filesDir: { type: String, default: '' },
      // the actions offered: { name, label, description, icon, disabled,
      // tooltip }, the tooltip saying why when disabled
      actions: { type: Array, default: () => [] },
      // the disk's clone jobs, as the jobs store keeps them
      cloneJobs: { type: Array, default: () => [] },
    },
    emits: ['action', 'open'],
    setup() {
      return {
        id: useId(),
        COPY,
        FORMATS_DOCS,
        chainProblem,
        diskLabel,
        diskReasons,
        diskType,
        docsPage,
        formatIssueName,
        listText,
      };
    },

    data() {
      return {
        // the details of detailsPath, or null before they load
        details: null,
        detailsPath: '',
        loadError: '',
        // the copy button that just copied, and what the live region says
        copied: '',
        copyStatus: '',
      };
    },

    computed: {
      label() {
        return diskLabel(this.disk);
      },
      type() {
        return diskType(this.disk);
      },
      // the details of this disk, or null while they load
      shown() {
        return this.detailsPath === this.disk.fullPath ? this.details : null;
      },
      health() {
        return diskHealth(this.disk, this.shown);
      },
      healthInfo() {
        return HEALTH[this.health];
      },
      problems() {
        return healthProblems(this.disk, this.shown);
      },
      kindIcon() {
        return KIND_ICONS[this.disk.kind] ?? 'question-circle';
      },
      runningText() {
        return diskRunningText(this.disk);
      },
      locationText() {
        if (this.disk.outsideFilesDir) {
          return 'Outside the standard images directory';
        }
        const where = this.filesDir
          ? `The standard images directory (${this.filesDir})`
          : 'The standard images directory';
        return this.disk.readOnly
          ? `${where}, at a path the disk list leaves out`
          : where;
      },
      formatLevel() {
        return formatIssueLevel(this.disk);
      },
      infoCommand() {
        return qemuImgInfoCommand(this.disk.fullPath);
      },
      // the images below this one, nearest first, then the missing file
      chain() {
        const links = (this.disk.backingImages ?? []).map((path) => ({
          path,
          disk: findDisk(this.disks, path),
        }));
        if (this.disk.missingBacking) {
          links.push({ path: this.disk.missingBacking, missing: true });
        }
        return links;
      },
      stats() {
        const d = this.shown;
        const bytes = (n) => (n > 0 ? bytesText(n) : '');
        const used = allocation(this.disk, d);
        const depth = this.disk.backingImages?.length ?? 0;
        const parts = this.partitionCount;
        // a container image has no virtual disk, so these tiles do not apply
        const container = this.disk.kind === 'Container';
        const tiles = [
          {
            key: 'size',
            icon: 'hdd',
            value: this.disk.size || bytes(d?.fileBytes) || '?',
            label: 'Size on disk',
          },
          {
            key: 'virtual',
            icon: 'expand',
            value: this.disk.virtualSize || bytes(d?.virtualBytes) || '?',
            label: 'Virtual size',
          },
          {
            key: 'allocated',
            icon: 'chart-pie',
            value: used?.text ?? '?',
            label: 'Allocated',
            bar: used?.bar,
          },
          {
            key: 'partitions',
            icon: 'table-columns',
            value: parts ?? (this.reading ? '…' : '?'),
            label: pluralWord(parts ?? 2, 'Partition'),
          },
          {
            key: 'table',
            icon: 'table-list',
            value:
              partitionTableName(d?.partitionTable) ||
              (this.reading ? '…' : '?'),
            label: 'Partition table',
          },
          {
            key: 'backing',
            icon: this.disk.brokenChain ? 'times-circle' : 'layer-group',
            value: depth,
            label: this.disk.brokenChain
              ? 'Backing images, chain broken'
              : pluralWord(depth, 'Backing image'),
            class: this.disk.brokenChain ? 'is-danger' : '',
          },
        ];
        return container
          ? tiles.filter((t) => !CONTAINER_HIDES.includes(t.key))
          : tiles;
      },
      // whether the details are still on their way
      reading() {
        return !this.shown && !this.loadError;
      },
      // the partitions phēnix read, or null when it read none
      partitionCount() {
        const scheme = this.shown?.partitionTable;
        if (!['mbr', 'gpt', 'none'].includes(scheme)) return null;
        return this.shown.partitions?.length ?? 0;
      },
      formatRows() {
        const d = this.shown;
        const q = d?.qcow;
        const rows = [];
        const add = (key, term, value, extra = {}) =>
          value !== '' && rows.push({ key, term, value, ...extra });
        if (q) {
          const version = Number(d.formatVersion) || 0;
          add(
            'version',
            'Version',
            q.compat ? `${version} (compat ${q.compat})` : String(version),
          );
          add('cluster', 'Cluster size', bytesText(q.clusterSize));
          if (q.refcountBits) {
            add('refcount', 'Refcount width', `${q.refcountBits} bits`);
          }
          add('encryption', 'Encryption', ENCRYPTION[q.encryption] ?? '');
          add('compression', 'Compression', q.compression ?? '');
          add('snapshots', 'Internal snapshots', String(q.snapshots ?? 0));
          if (q.backingFile) {
            const format = q.backingFormat ? ` (${q.backingFormat})` : '';
            add('header', 'Header backing file', `${q.backingFile}${format}`, {
              class: 'disk-mono',
            });
          }
        }
        if (d?.fileBytes > 0) {
          add('file', 'File size', bytesText(d.fileBytes), {
            title: `${d.fileBytes} bytes`,
          });
        }
        if (d?.allocatedBytes > 0) {
          add('allocated', 'Space used', bytesText(d.allocatedBytes), {
            title: `${d.allocatedBytes} bytes`,
          });
        }
        if (d?.virtualBytes > 0) {
          add('virtual', 'Virtual size', bytesText(d.virtualBytes), {
            title: `${d.virtualBytes} bytes`,
          });
        }
        return rows;
      },
      features() {
        return qcowFeatures(this.shown?.qcow);
      },
      partitions() {
        const targets = injectTargets(this.shown);
        return (this.shown?.partitions ?? []).map((part) => ({
          ...part,
          typeText: part.typeId && part.typeId !== part.type ? part.typeId : '',
          fsText: part.extended
            ? '—'
            : filesystemName(part.filesystem) || 'None found',
          startText: bytesText(part.start),
          sizeText: bytesText(part.size),
          injectVms: targets.get(part.number) ?? [],
        }));
      },
      tableSummary() {
        const d = this.shown;
        const scheme = d?.partitionTable;
        if (scheme === 'none') return 'No partition table.';
        if (scheme === 'unknown') {
          return 'phēnix does not know this partition table.';
        }
        if (!partitionTableName(scheme)) {
          return 'phēnix did not read the partition table.';
        }
        const parts = [`${partitionTableName(scheme)} partition table`];
        if (d.diskId) parts.push(`disk ID ${d.diskId}`);
        if (d.sectorSize) parts.push(`${d.sectorSize}-byte sectors`);
        return `${parts.join(', ')}.`;
      },
      // the file system on a disk with no partition table, in a sentence
      wholeDiskText() {
        const fs = this.shown?.filesystem;
        if (!fs?.type) return '';
        const label = fs.label ? ` labeled “${fs.label}”` : '';
        return `The whole disk holds ${filesystemName(fs)}${label}.`;
      },
      notes() {
        return this.shown?.partitionNotes ?? [];
      },
      injectWarnings() {
        return injectProblems(this.shown);
      },
      overlays() {
        return (this.shown?.overlays ?? []).map((overlay) => ({
          ...overlay,
          disk: findDisk(this.disks, overlay.fullPath),
        }));
      },
      uses() {
        return usedBy(this.disk, this.shown);
      },
    },

    watch: {
      // A new list entry for the disk reloads its details; another disk
      // also takes the focus to its name, as the old button is gone.
      disk: {
        immediate: true,
        handler(disk, old) {
          this.load();
          if (old && old.fullPath !== disk.fullPath) {
            this.copied = '';
            this.$nextTick(() => this.$refs.title?.focus());
          }
        },
      },
    },

    beforeUnmount() {
      this.controller?.abort();
      clearTimeout(this.copyTimer);
    },

    methods: {
      async load() {
        this.controller?.abort();
        const path = this.disk.fullPath;
        if (!path) return;
        const controller = new AbortController();
        this.controller = controller;
        if (this.detailsPath !== path) this.loadError = '';
        try {
          const resp = await disksApi.getDiskDetails(path, {
            signal: controller.signal,
          });
          if (controller.signal.aborted) return;
          this.details = resp.data ?? {};
          this.detailsPath = path;
          this.loadError = '';
        } catch (err) {
          if (controller.signal.aborted) return;
          if (this.detailsPath !== path) {
            this.details = null;
            this.loadError = `phēnix cannot load the details: ${errorLine(err)}`;
          }
        }
      },
      // why a part of the card is not shown, or '' when it is
      unavailable(part) {
        if (this.loadError) return this.loadError;
        if (part === 'partitions')
          return this.shown?.partitionsUnavailable ?? '';
        return '';
      },
      canViewExperiment(name) {
        return roleAllowed('experiments', 'get', name);
      },
      vmUseText(use) {
        const inject =
          use.drive === 0 && use.injectPartition > 0
            ? `, inject partition ${use.injectPartition}`
            : '';
        return `VM ${use.vm}, drive ${use.drive}${inject}`;
      },
      copyIcon(key) {
        return this.copied === key ? 'check-circle' : 'clipboard';
      },
      copyLabel(key) {
        return this.copied === key ? 'Copied' : COPY[key].label;
      },
      // Copies text and says in the live region whether it did; the page has
      // no Clipboard API when it is served over plain http.
      async copy(key, text) {
        const { what } = COPY[key];
        // an empty region first, so that the same message is read again
        this.copyStatus = '';
        let done = true;
        try {
          await navigator.clipboard.writeText(text);
        } catch {
          done = false;
        }
        clearTimeout(this.copyTimer);
        this.copied = done ? key : '';
        this.copyStatus = done
          ? `Copied ${what}.`
          : `The browser did not copy ${what}.`;
        this.copyTimer = setTimeout(() => {
          this.copied = '';
          this.copyStatus = '';
        }, COPIED_MS);
      },
    },
  };
</script>

<style scoped>
  .disk-card {
    width: min(1280px, 95vw);
  }

  .disk-card-head {
    align-items: flex-start;
    gap: 1rem;
    border-top: 6px solid #8a8a8a;
    background-color: #4f4f4f;
    /* room for the modal's close X on the head's corner */
    padding-right: 3.5rem;
  }

  .disk-card-head.has-health-ok {
    border-top-color: hsl(141, 53%, 53%);
  }
  .disk-card-head.has-health-warning {
    border-top-color: hsl(48, 100%, 67%);
  }
  .disk-card-head.has-health-danger {
    border-top-color: hsl(348, 86%, 61%);
  }

  .disk-card-heading {
    flex: 1;
    min-width: 0;
  }

  .disk-card-title-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }

  .disk-card-title {
    margin: 0 !important;
    flex: 0 1 auto;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  /* the icon and text centered in a tag: b-tag wraps them in a span that
     would sit them on the text baseline, and Bulma pulls a first icon left */
  .disk-tag {
    font-weight: 600;
  }

  :deep(.disk-tag > span) {
    display: inline-flex;
    align-items: center;
    gap: 0.35em;
    line-height: 1;
  }

  .disk-tag .icon {
    margin: 0 !important;
    width: 1em;
    height: 1em;
  }

  /* the kind and type tags, 7.1:1 */
  .disk-kind-tag,
  .disk-type-tag {
    border-radius: 4px;
    background-color: #2f5d7c;
    color: #ffffff;
  }

  .disk-inject-tag {
    background-color: #00796b;
    color: #ffffff;
  }

  .disk-card-links {
    display: flex;
    flex: none;
    gap: 0.4rem;
  }

  .disk-card-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem 1.25rem;
    margin-top: 0.5rem;
    color: #e0e0e0;
    font-size: 0.9rem;
  }

  .disk-card-meta > span {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    min-width: 0;
  }

  .disk-card-meta > span > .icon {
    opacity: 0.8;
  }

  .disk-full-path {
    overflow-wrap: anywhere;
  }

  .disk-mono {
    font-family: monospace;
    font-size: 0.9rem;
  }

  .disk-copy {
    height: 1.75rem;
    width: 1.75rem;
    padding: 0;
  }

  .disk-card-body {
    padding-top: 1rem;
  }

  .disk-problems {
    margin-bottom: 0.75rem;
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    border-left: 4px solid hsl(48, 100%, 67%);
    background-color: #574f36;
  }

  .disk-problems.is-danger {
    border-left-color: hsl(348, 86%, 61%);
    background-color: #5c3a41;
  }

  .disk-problems ul {
    list-style: none;
    margin: 0 !important;
  }

  .disk-problems li {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    margin: 0 !important;
  }

  .disk-problems li + li {
    margin-top: 0.25rem !important;
  }

  .disk-problems li .icon {
    flex: none;
    align-self: center;
  }

  /* the pale red and yellow are 6.7:1 and 6.3:1 on the problem boxes */
  .disk-problems li.is-danger .icon {
    color: #ffc8cf;
  }
  .disk-problems li.is-warning .icon {
    color: #ffe08a;
  }

  .disk-section {
    background-color: #484848;
    border-radius: 8px;
    padding: 0.75rem 0.9rem;
    margin-bottom: 0.75rem;
  }

  .disk-section-title {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem !important;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #cfcfcf;
  }

  .disk-columns {
    margin-bottom: 0 !important;
  }

  .disk-action-buttons {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
    gap: 0.5rem;
  }

  .disk-action-buttons :deep(.b-tooltip),
  .disk-action-buttons :deep(.tooltip-trigger) {
    display: block;
    width: 100%;
    height: 100%;
  }

  .disk-action-buttons :deep(.button) {
    justify-content: flex-start;
    height: 100%;
    min-height: 2.5rem;
    white-space: normal;
    text-align: start;
  }

  .disk-action-buttons :deep(.button > span:not(.icon)) {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
  }

  .disk-action-label {
    font-weight: 600;
  }

  /* 4.9:1 on a light button */
  .disk-action-description {
    font-size: 0.75rem;
    line-height: 1.3;
    color: #f0f0f0;
  }

  /* Bulma's outlined red text is too faint on the section's gray; this is
     4.7:1 */
  .disk-action-buttons
    :deep(
      .button.is-danger.is-outlined:not(:hover):not([aria-disabled='true'])
    ) {
    border-color: #ff9eb0;
    color: #ff9eb0;
  }

  .disk-action-buttons
    :deep(
      .button.is-danger.is-outlined:not(:hover):not([aria-disabled='true'])
        .disk-action-description
    ) {
    color: #ff9eb0;
  }

  /* one that cannot run now is gray, hovered too; its text says why. The
     :not()s outrank the colored buttons' hover rules in _general.scss. */
  .disk-action-buttons :deep(.button[aria-disabled='true']:not(.is-inverted)) {
    border-color: transparent;
    background-color: #5a5a5a;
    color: #e0e0e0;
    opacity: 1;
    cursor: not-allowed;
  }

  .disk-action-buttons
    :deep(.button[aria-disabled='true'] .disk-action-description) {
    color: #e0e0e0;
  }

  .disk-command {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.6rem;
    min-width: 0;
  }

  .disk-command-name {
    flex: none;
    color: #cfcfcf;
    font-size: 0.85rem;
  }

  .disk-command-text {
    flex: 1;
    min-width: 0;
    padding: 0.25em 0.5em;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: #f5f5f5;
    background-color: #333;
  }

  .disk-stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(9.5rem, 1fr));
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }

  .disk-stat {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    background-color: #484848;
    border-radius: 8px;
    border-left: 4px solid #62c0d7;
    padding: 0.5rem 0.75rem;
  }

  .disk-stat > .icon {
    color: #62c0d7;
  }

  .disk-stat.is-danger {
    border-left-color: hsl(348, 86%, 61%);
  }
  .disk-stat.is-danger > .icon {
    color: #ffc8cf;
  }

  .disk-stat-text {
    flex: 1;
    min-width: 0;
  }

  .disk-stat-value {
    font-size: 1.15rem;
    font-weight: 700;
    line-height: 1.2;
    margin: 0 !important;
  }

  .disk-stat-label {
    font-size: 0.75rem;
    color: #cfcfcf;
    margin: 0 !important;
  }

  .disk-stat-bar {
    height: 0.3rem;
    margin-top: 0.25rem;
    border-radius: 999px;
    background-color: #686868;
    overflow: hidden;
  }

  .disk-stat-bar span {
    display: block;
    height: 100%;
    background-color: #62c0d7;
  }

  .disk-kv {
    display: grid;
    grid-template-columns: fit-content(45%) minmax(0, 1fr);
    gap: 0.25rem 0.75rem;
    margin: 0 0 0.5rem !important;
  }

  .disk-kv > div {
    display: contents;
  }

  .disk-kv dt {
    color: #cfcfcf;
    font-size: 0.85rem;
  }

  .disk-kv dd {
    margin: 0 !important;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .disk-features {
    margin-bottom: 0.25rem !important;
  }

  .disk-unavailable,
  .disk-overlay-note,
  .chain-problem,
  .disk-notes li {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }

  .disk-unavailable .icon,
  .disk-overlay-note .icon,
  .chain-problem .icon,
  .disk-notes li .icon {
    flex: none;
    align-self: center;
  }

  .disk-unavailable {
    color: #d0d0d0;
    margin: 0 !important;
  }

  .format-issue {
    margin-top: 0.5rem;
    padding: 0.5rem 0.75rem;
    border-radius: 6px;
    border-left: 3px solid hsl(48, 100%, 67%);
    background-color: #545454;
  }

  .format-issue.is-error {
    border-left-color: hsl(348, 86%, 61%);
  }

  .format-issue p {
    margin-bottom: 0.4rem !important;
  }

  .format-issue .disk-command {
    margin: 0 0 0.4rem;
  }

  /* links are 4.75:1 on the issue box and 5.7:1 on the sections */
  .format-docs,
  .format-docs:hover,
  .format-docs:focus,
  .plain-button.backing-link,
  .disk-use-link {
    color: #8fd8ea;
    text-decoration: underline;
  }

  .disk-table-summary,
  .disk-whole-disk {
    margin-bottom: 0.4rem !important;
  }

  .disk-table-wrap {
    overflow-x: auto;
  }

  .disk-partitions {
    margin-bottom: 0.5rem !important;
    background-color: transparent;
    font-size: 0.9rem;
  }

  .disk-partitions th {
    color: #cfcfcf;
    font-size: 0.75rem;
    font-weight: 700;
  }

  .disk-partitions td,
  .disk-partitions th {
    border-color: #686868;
    vertical-align: middle;
  }

  .disk-partitions tr.is-inject td {
    background-color: #3f5560;
  }

  .disk-partitions tr.is-inject td:first-child {
    box-shadow: inset 3px 0 0 #62c0d7;
  }

  .disk-partitions tr.is-logical .disk-part-number {
    padding-left: 1.25rem;
  }

  .disk-part-number {
    font-weight: 700;
  }

  /* the type ID and names, 5:1 on an inject row and 5.9:1 elsewhere */
  .disk-part-id,
  .disk-part-name {
    margin-left: 0.35rem;
    color: #d0d0d0;
  }

  .disk-partitions td.disk-mono {
    white-space: nowrap;
  }

  .disk-part-flags .tag {
    margin: 0.1rem 0.25rem 0.1rem 0;
  }

  .disk-notes {
    list-style: none;
    margin: 0 !important;
  }

  .disk-notes li + li {
    margin-top: 0.25rem;
  }

  .chain-problem {
    margin-bottom: 0.5rem !important;
  }

  .disk-chain {
    list-style: none;
    margin: 0 !important;
  }

  .disk-chain-item {
    position: relative;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 0.6rem;
    padding: 0.4rem 0.6rem;
    border: 1px solid #686868;
    border-radius: 6px;
    background-color: #545454;
  }

  .disk-chain-item + .disk-chain-item {
    margin-top: 1.4rem !important;
  }

  .disk-chain-item.is-self {
    border-color: #62c0d7;
  }

  .disk-chain-item.is-missing {
    border-color: hsl(348, 86%, 61%);
    background-color: #5c3a41;
  }

  /* the arrow sits in the gap above the box */
  .disk-chain-arrow {
    position: absolute;
    top: -1.35rem;
    left: 1rem;
    color: #cfcfcf;
  }

  .disk-chain-name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .plain-button.backing-link {
    font-weight: 600;
    overflow-wrap: anywhere;
    text-align: start;
  }

  /* 5.7:1 on the chain's boxes */
  .disk-chain-meta {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    color: #e0e0e0;
    font-size: 0.85rem;
  }

  .missing-backing {
    overflow-wrap: anywhere;
  }

  /* _general.scss sets two columns on every ul */
  .disk-card ul {
    columns: auto;
  }

  .disk-list {
    list-style: none;
    margin: 0 !important;
  }

  .disk-list > li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  .disk-list > li + li {
    margin-top: 0.4rem;
  }

  .disk-overlay-note {
    margin: 0.5rem 0 0 !important;
    font-size: 0.85rem;
  }

  .disk-use {
    display: block !important;
  }

  .disk-use-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .disk-use-link,
  .disk-use-name {
    font-weight: 600;
  }

  .disk-use-vms {
    list-style: none;
    margin: 0.25rem 0 0 1rem !important;
  }

  .disk-use-vms li {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .disk-use-indirect,
  .disk-use-hidden,
  .disk-empty,
  .disk-loading {
    color: #bdbdbd;
    font-style: italic;
    margin: 0 !important;
  }

  .disk-use-indirect,
  .disk-use-hidden {
    margin: 0.25rem 0 0 1rem !important;
  }

  @media screen and (max-width: 768px) {
    .disk-card-body {
      padding: 0.75rem;
    }

    .disk-action-buttons {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
