<template>
  <div class="shell">
    <header class="topbar">
      <div class="brand">
        <ScanLine :size="18" />
        <span>AI Auto Annotator</span>
      </div>
      <nav class="toplinks">
        <RouterLink to="/dashboard">新手模式</RouterLink>
        <span>YOLO 导出</span>
      </nav>
    </header>
    <main class="content">
      <RouterView />
    </main>
  </div>
</template>

<script setup lang="ts">
import { ScanLine } from 'lucide-vue-next'
</script>
