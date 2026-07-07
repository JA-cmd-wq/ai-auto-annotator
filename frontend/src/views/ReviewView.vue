<template>
  <section class="plain-band">
    <p class="eyebrow">人工审校</p>
    <h1>审校画布接口已经预留。</h1>
    <p class="lead">首版工作台已经可以读取检测框、覆盖保存人工结果。下一步可在这里接入拖拽框选画布。</p>
    <n-card class="review-placeholder">
      <MousePointerSquareDashed :size="44" />
      <p>选择数据集图片后，画布会显示原图、检测框列表和标签编辑器。</p>
    </n-card>
  </section>
</template>

<script setup lang="ts">
import { MousePointerSquareDashed } from 'lucide-vue-next'
</script>
