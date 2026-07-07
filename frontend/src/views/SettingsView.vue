<template>
  <section class="plain-band">
    <p class="eyebrow">系统设置</p>
    <h1>模型、存储与提示词服务。</h1>
    <div class="grid-two">
      <n-card title="推理引擎">
        <n-descriptions :column="1" bordered>
          <n-descriptions-item label="状态">{{ engine?.loaded ? '已加载' : '未加载' }}</n-descriptions-item>
          <n-descriptions-item label="模型路径">{{ engine?.model_path || 'Stub Engine' }}</n-descriptions-item>
          <n-descriptions-item label="线程数">{{ engine?.threads }}</n-descriptions-item>
          <n-descriptions-item label="ABI">{{ engine?.abi_version }}</n-descriptions-item>
        </n-descriptions>
      </n-card>
      <n-card title="LLM 提示词优化">
        <p class="muted">密钥只从后端环境变量 MATPOOL_KEY 读取，不进入数据库或前端代码。</p>
      </n-card>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getEngine } from '../api/client'
import type { EngineStatus } from '../api/types'

const engine = ref<EngineStatus | null>(null)
onMounted(async () => {
  engine.value = await getEngine()
})
</script>
