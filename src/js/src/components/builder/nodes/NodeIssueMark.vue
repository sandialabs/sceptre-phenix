<!--
  What the diagram checks found about a node, in two parts:

  - A mark on the node's corner: a cross for errors, and a triangle when
    there are only warnings. The two marks differ by shape as well as color.
  - Hidden text that describes the node with the issues (see toFlowNodes),
    such as "1 error: hostname is required."
-->
<template>
  <span
    class="builder-node__issue"
    :data-level="issue.level"
    data-testid="node-issue"
    aria-hidden="true">
    <builder-icon
      :name="issue.level === 'error' ? 'close' : 'warning'"
      :size="12" />
  </span>
  <span :id="nodeIssueId(nodeId)" class="builder-node__issue-text" hidden>{{
    issue.text
  }}</span>
</template>

<script setup>
  import BuilderIcon from '../BuilderIcon.vue';

  import { nodeIssueId } from '@/builder/adapters/vueflow.js';

  defineProps({
    nodeId: { type: String, required: true },
    issue: { type: Object, required: true },
  });
</script>
