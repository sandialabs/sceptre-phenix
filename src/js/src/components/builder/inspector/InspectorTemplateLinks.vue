<!--
  The notice of the Inspector's Diagram section about the diagram's
  templates that are copies of templates of the library (see templateLink in
  templates.js) and need attention: the library has a newer version, or the
  library template was deleted. It shows only while one or more need
  attention, and lists them, one row each.

  A row of a copy with a newer version has Update, and the notice has
  Update all while two or more have one. Update asks first (it replaces the
  diagram's copy, and changes made to it in the diagram), then copies the
  library's version, as one undo step (the store's updateTemplateCopies).
  Devices made from the templates do not change. A row of a copy whose
  library template was deleted says so, and has Unlink, which keeps the
  copy and removes its link (the store's unlinkTemplate). Nothing deletes a
  copy. A read-only draft shows the rows without buttons.

  The store checks the copies again when the diagram or the library
  changes: when a draft opens, and when the library is read.
-->
<template>
  <section
    v-if="notices.length"
    class="inspector-diagram inspector-template-links"
    aria-labelledby="inspector-template-links-title"
    data-testid="inspector-template-links">
    <h3 id="inspector-template-links-title">Template updates</h3>
    <p
      class="inspector-diagram__none"
      data-testid="inspector-template-links-summary">
      {{ summary }}
    </p>
    <ul class="inspector-template-links__list">
      <li
        v-for="link in notices"
        :key="link.template.id"
        class="inspector-template-links__row"
        :data-testid="`inspector-template-link-${link.template.id}`">
        <span class="inspector-template-links__name">
          <builder-icon
            :name="link.state === 'update' ? 'refresh' : 'warning'"
            :size="14" />
          {{ link.template.name }}
        </span>
        <span
          :id="`inspector-template-link-text-${link.template.id}`"
          :data-testid="`inspector-template-link-text-${link.template.id}`"
          class="inspector-template-links__text">
          {{ templateLinkText(link) }}
        </span>
        <button
          v-if="!store.readOnly && link.state === 'update'"
          type="button"
          class="builder-button"
          aria-haspopup="dialog"
          :aria-label="`Update template ${link.template.name}`"
          :aria-describedby="`inspector-template-link-text-${link.template.id}`"
          :data-testid="`inspector-template-update-${link.template.id}`"
          @click="askUpdate([link])">
          <builder-icon name="refresh" :size="14" />
          Update
        </button>
        <button
          v-else-if="!store.readOnly"
          type="button"
          class="builder-button"
          :aria-label="`Unlink template ${link.template.name}`"
          :aria-describedby="`inspector-template-link-text-${link.template.id}`"
          :data-testid="`inspector-template-unlink-${link.template.id}`"
          @click="unlink(link)">
          Unlink
        </button>
      </li>
    </ul>
    <button
      v-if="!store.readOnly && updates.length > 1"
      type="button"
      class="builder-button"
      aria-haspopup="dialog"
      data-testid="inspector-template-update-all"
      @click="askUpdate(updates)">
      <builder-icon name="refresh" :size="14" />
      Update all
    </button>

    <!-- Update replaces the diagram's copies, which may hold changes made in
         the diagram, so it asks first. Undo brings the copies back. -->
    <builder-confirm
      v-if="question"
      id="inspector-template-update"
      :title="question.title"
      :message="question.message"
      :confirm-label="question.confirmLabel"
      @cancel="question = null"
      @confirm="update" />
  </section>
</template>

<script setup>
  import { computed, nextTick, ref } from 'vue';

  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderIcon from '../BuilderIcon.vue';

  import { count } from '@/builder/announce.js';
  import { focusLost } from '@/builder/commands.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    templateLinkText,
    templateUpdateQuestion,
  } from '@/builder/templates.js';

  const store = useBuilderStore();

  // The copies that need attention, and those of them with an update.
  const notices = computed(() => store.templateLinkNotices);
  const updates = computed(() =>
    notices.value.filter((link) => link.state === 'update'),
  );

  // What the notice says first: how many copies have an update, and how
  // many lost their library template.
  const summary = computed(() => {
    const newer = updates.value.length;
    const gone = notices.value.length - newer;

    return [
      newer
        ? `The library has a newer version of ${count(newer, 'template')} in this diagram.`
        : '',
      gone
        ? `${gone === 1 ? '1 template was' : `${gone} templates were`} deleted from the library or the server.`
        : '',
      'Devices made from these templates do not change.',
    ]
      .filter(Boolean)
      .join(' ');
  });

  // The question before Update: the copies to update, with what the update
  // does (see templateUpdateQuestion).
  const question = ref(null);

  function askUpdate(links) {
    question.value = { ...templateUpdateQuestion(links), links };
  }

  // A row goes once its copy is current, or unlinked, and the notice with
  // the last row. Focus then moves to the next row's button, or to the
  // Inspector's heading, so it does not fall to the page.
  async function keepFocus() {
    await nextTick();

    if (!focusLost()) {
      return;
    }

    (
      document.querySelector(
        '[data-testid="inspector-template-links"] .builder-button',
      ) || document.getElementById('inspector-title')
    )?.focus();
  }

  async function update() {
    const { links } = question.value;

    question.value = null;
    store.updateTemplateCopies(links.map((link) => link.template.id));
    await keepFocus();
  }

  async function unlink(link) {
    store.unlinkTemplate(link.template.id);
    await keepFocus();
  }
</script>

<style scoped>
  .inspector-template-links {
    margin-bottom: 0.5rem;
    padding: 0.1rem 0.6rem 0.6rem;
    border: 1px solid var(--bx-border-strong);
    border-left: 3px solid var(--bx-accent);
    border-radius: var(--bx-radius);
    background: var(--bx-bg-alt);
  }

  .inspector-template-links h3 {
    margin-top: 0.5rem;
  }

  .inspector-template-links__list {
    margin: 0 0 0.5rem;
    padding: 0;
    list-style: none;
  }

  .inspector-template-links__list > li + li {
    margin-top: 0.5rem;
  }

  /* A row is the template's name, what is wrong, and its button, each on a
     line of its own in the narrow column. */
  .inspector-template-links__row {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.2rem;
  }

  .inspector-template-links__name {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .inspector-template-links__name :deep(svg) {
    flex: none;
  }

  .inspector-template-links__text {
    overflow-wrap: anywhere;
  }
</style>
