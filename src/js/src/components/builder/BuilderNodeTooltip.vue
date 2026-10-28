<!-- The info tooltip of canvas nodes (see fixedTooltip.js and nodeInfo.js). It
     is aria-hidden, because the node's description gives the same facts. -->
<template>
  <div
    v-if="tooltip.tip.value"
    :ref="tooltip.setTipEl"
    class="builder-tooltip builder-tooltip--fixed builder-tooltip--info"
    data-testid="node-tooltip"
    aria-hidden="true"
    :style="{
      top: `${tooltip.tip.value.top}px`,
      left: `${tooltip.tip.value.left}px`,
    }">
    <dl class="builder-info">
      <template v-for="row in tooltip.tip.value.text.rows" :key="row.label">
        <dt class="builder-info__label">{{ row.label }}</dt>
        <dd class="builder-info__value">
          <span
            v-for="(line, index) in row.lines"
            :key="index"
            class="builder-info__line">
            {{ line }}
          </span>
        </dd>
      </template>
    </dl>
  </div>
</template>

<script setup>
  defineProps({
    tooltip: { type: Object, required: true },
  });
</script>
