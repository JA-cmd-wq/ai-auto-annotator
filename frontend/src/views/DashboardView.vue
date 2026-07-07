<template>
  <section class="beginner-hero">
    <div>
      <p class="eyebrow">新手模式</p>
      <h1>先说要标什么，再上传图片检测。</h1>
      <p class="lead">系统会自动创建项目、数据集和类别。你只需要输入目标、上传素材、点击检测。</p>
      <div class="engine-pill" :class="{ ok: engine?.loaded }">
        <span>{{ engine?.loaded ? '模型已加载' : '模型未加载' }}</span>
        <small>{{ engine?.model_path || engine?.error || '等待后端连接' }}</small>
      </div>
    </div>
    <div class="beginner-visual" aria-label="AI annotation preview">
      <img src="/assets/annotator-hero.png" alt="AI annotation preview" />
    </div>
  </section>

  <section class="beginner-grid">
    <n-card class="step-card">
      <div class="step-index">1</div>
      <h2>输入检测目标</h2>
      <p class="muted">用中文写清楚要标注的类别，例如“标出自行车、人、头盔三类”。</p>
      <n-space vertical>
        <n-input v-model:value="taskName" placeholder="任务名称，例如：骑行安全检测" />
        <n-input
          v-model:value="naturalText"
          type="textarea"
          placeholder="输入要标注的目标"
          :autosize="{ minRows: 3, maxRows: 5 }"
        />
        <n-space>
          <n-button type="primary" :loading="preparing" @click="prepareTask">生成任务配置</n-button>
          <n-button secondary @click="useBikePreset">使用骑行示例</n-button>
        </n-space>
        <n-input
          v-model:value="optimizedPrompt"
          type="textarea"
          placeholder="AI 生成的英文提示词会显示在这里"
          :autosize="{ minRows: 2, maxRows: 4 }"
        />
      </n-space>
    </n-card>

    <n-card class="step-card">
      <div class="step-index">2</div>
      <h2>确认类别</h2>
      <p class="muted">提示词会自动拆成类别。关键词用于把模型返回结果映射到导出类别。</p>
      <div v-if="classes.length" class="class-chip-grid">
        <div v-for="cls in classes" :key="cls.name" class="class-chip">
          <span class="swatch" :style="{ background: cls.color }"></span>
          <div>
            <strong>{{ cls.name }}</strong>
            <small>{{ cls.match_keywords.join(', ') }}</small>
          </div>
        </div>
      </div>
      <n-alert v-else type="info">先点击“生成任务配置”。</n-alert>
    </n-card>
  </section>

  <section class="beginner-grid">
    <n-card class="step-card">
      <div class="step-index">3</div>
      <h2>上传素材</h2>
      <p class="muted">支持图片和视频。视频上传后会自动拆帧；相邻帧几乎相同，按秒抽帧能大幅加快检测，训练效果基本不受影响。</p>
      <n-space vertical>
        <n-space align="center">
          <n-upload :show-file-list="false" multiple :custom-request="customUpload" :disabled="!selectedDatasetId">
            <n-button :disabled="!selectedDatasetId">选择图片或视频</n-button>
          </n-upload>
          <n-select v-model:value="extractFps" :options="fpsOptions" size="small" style="width: 210px" />
        </n-space>
        <n-list v-if="materials.length" bordered>
          <n-list-item v-for="item in materials" :key="item.id">
            <n-thing :title="item.file_name" :description="materialDesc(item)" />
          </n-list-item>
        </n-list>
        <n-alert v-else type="info">任务配置完成后，上传素材会显示在这里。</n-alert>
        <p v-if="detectableCount" class="muted material-summary">共 {{ detectableCount }} 张图片将参与检测</p>
      </n-space>
    </n-card>

    <n-card class="step-card">
      <div class="step-index">4</div>
      <h2>检测与导出</h2>
      <p class="muted">检测只处理新增素材，人工修改过的图片不会被覆盖；可随时暂停，下次点开始检测会从剩余图片继续。</p>
      <n-space vertical size="large">
        <n-space align="center">
          <n-button type="primary" :disabled="!canDetect" :loading="hasActiveDetect" @click="detectNow('new')">开始检测</n-button>
          <n-dropdown trigger="click" :options="datasetExportOptions" :disabled="!canExport" @select="onDatasetExport">
            <n-button secondary :disabled="!canExport">导出数据集</n-button>
          </n-dropdown>
          <n-dropdown trigger="click" :options="imagesExportOptions" :disabled="!canExport" @select="onImagesExport">
            <n-button secondary :disabled="!canExport">导出识别图片</n-button>
          </n-dropdown>
          <n-popconfirm @positive-click="detectNow('all')">
            <template #trigger>
              <n-button text type="primary" size="small" :disabled="!canDetect">全部重新检测</n-button>
            </template>
            将对所有图片重新运行 AI 检测（人工修改过的仍会保留），确定继续？
          </n-popconfirm>
        </n-space>

        <n-space align="center" size="small" class="filter-row">
          <n-checkbox v-model:checked="filterBlanketBoxes">过滤超大框</n-checkbox>
          <span class="muted filter-hint">去掉圈住一大片的错误框（航拍密集场景推荐）</span>
          <n-popconfirm @positive-click="cleanExistingBoxes">
            <template #trigger>
              <n-button text type="warning" size="small" :disabled="!selectedDatasetId || busy">清理现有超大框</n-button>
            </template>
            删除当前数据集里占画面超过 {{ Math.round(blanketRatio * 100) }}% 的 AI 框（人工标注保留），确定？
          </n-popconfirm>
        </n-space>

        <div v-if="activeJobs.length" class="active-jobs">
          <div v-for="job in activeJobs" :key="job.id" class="active-job">
            <div class="active-job-head">
              <span>{{ jobTitle(job) }}</span>
              <span class="active-job-side">
                <small>{{ job.total ? `${job.processed}/${job.total}` : '准备中…' }}</small>
                <n-button v-if="job.type === 'detect'" text type="warning" size="tiny" @click="pauseJob(job)">暂停</n-button>
              </span>
            </div>
            <n-progress type="line" :percentage="progressOf(job)" :processing="true" :show-indicator="false" />
          </div>
        </div>

        <div v-if="latestExports.length" class="download-grid">
          <div v-for="entry in latestExports" :key="entry.job.id" class="download-card">
            <div>
              <strong>{{ entry.label }}</strong>
              <small>任务 #{{ entry.job.id }} · {{ entry.job.total }} 张 · {{ timeOf(entry.job) }}</small>
            </div>
            <n-button tag="a" :href="jobResultUrl(entry.job.id)" type="primary" size="small">下载 ZIP</n-button>
          </div>
        </div>

        <n-collapse v-if="exportHistory.length">
          <n-collapse-item :title="`历史导出（${exportHistory.length} 个，随时可回头下载）`" name="history">
            <template #header-extra>
              <n-popconfirm @positive-click="cleanOldExports">
                <template #trigger>
                  <n-button text size="tiny" @click.stop>清理旧导出</n-button>
                </template>
                删除历史导出（每种格式保留最新一份）？ZIP 文件会一起删除。
              </n-popconfirm>
            </template>
            <div v-for="job in exportHistory" :key="job.id" class="history-item">
              <div class="history-row">
                <span class="history-name">
                  <span>
                    <n-tag size="small" :bordered="false" :type="formatTagType(job)">{{ formatTagOf(job) }}</n-tag>
                    {{ exportLabelOf(job) }}
                  </span>
                  <small class="muted">#{{ job.id }} · {{ job.total }} 张 · {{ dateTimeOf(job) }}</small>
                </span>
                <span class="history-actions">
                  <n-button tag="a" :href="jobResultUrl(job.id)" text type="primary" size="small">下载</n-button>
                  <n-popconfirm @positive-click="removeExport(job)">
                    <template #trigger>
                      <n-button text size="small">删除</n-button>
                    </template>
                    删除这条导出记录和对应的 ZIP 文件？
                  </n-popconfirm>
                </span>
              </div>
            </div>
          </n-collapse-item>
        </n-collapse>

        <div v-if="recentJobs.length" class="job-history">
          <p class="muted history-title">最近任务</p>
          <div v-for="job in recentJobs" :key="job.id" class="history-item">
            <div class="history-row">
              <span>{{ jobTitle(job) }} #{{ job.id }}</span>
              <n-tag size="small" :bordered="false" :type="statusTagType(job.status)">{{ statusText(job) }}</n-tag>
            </div>
            <small v-if="job.status === 'failed' && job.error" class="history-error">{{ job.error }}</small>
          </div>
        </div>
        <n-alert v-else type="info">检测和导出任务会显示在这里。</n-alert>
      </n-space>
    </n-card>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useMessage, type UploadCustomRequestOptions } from 'naive-ui'
import {
  cancelJob,
  cleanBoxes,
  createDataset,
  createProject,
  deleteJob,
  getEngine,
  jobResultUrl,
  listDatasets,
  listImages,
  listJobs,
  listLabelClasses,
  listProjects,
  optimizePrompt,
  saveLabelClasses,
  submitDetect,
  submitExport,
  submitExtract,
  uploadFile
} from '../api/client'
import type { Dataset, EngineStatus, ImageItem, Job, LabelClass, Project } from '../api/types'

const message = useMessage()
const engine = ref<EngineStatus | null>(null)
const projects = ref<Project[]>([])
const datasets = ref<Dataset[]>([])
const selectedProjectId = ref<number | null>(null)
const selectedDatasetId = ref<number | null>(null)
const taskName = ref('骑行安全检测')
const naturalText = ref('标出自行车；人；以及头盔三类')
const optimizedPrompt = ref('')
const classes = ref<LabelClass[]>([])
const images = ref<ImageItem[]>([])
const jobs = ref<Job[]>([])
const preparing = ref(false)

// 超大框过滤：勾选后，检测/清理会丢弃占画面比例超过 blanketRatio 的 AI 框
const filterBlanketBoxes = ref(true)
const blanketRatio = 0.1

// 拆帧密度：0 = 每一帧。默认每秒 2 帧——相邻帧近乎重复，抽帧后检测量下降一个数量级
const extractFps = ref(2)
const fpsOptions = [
  { label: '拆帧：每秒 2 帧（推荐）', value: 2 },
  { label: '拆帧：每秒 1 帧', value: 1 },
  { label: '拆帧：每秒 5 帧', value: 5 },
  { label: '拆帧：每一帧（最慢）', value: 0 }
]

const materials = computed(() => images.value.filter((img) => img.kind !== 'frame'))
const detectableCount = computed(() => images.value.filter((img) => img.kind === 'image' || img.kind === 'frame').length)
const activeJobs = computed(() => jobs.value.filter(isActive))
const hasActiveDetect = computed(() => jobs.value.some((job) => job.type === 'detect' && isActive(job)))
const busy = computed(() => jobs.value.some((job) => (job.type === 'detect' || job.type === 'extract') && isActive(job)))
const canDetect = computed(() => !!selectedDatasetId.value && detectableCount.value > 0 && !busy.value)
const canExport = computed(() => !!selectedDatasetId.value && !busy.value)
const recentJobs = computed(() => jobs.value.slice(0, 5))

// 下载区置顶展示每种格式“最新一次成功”的导出，完整历史收进折叠面板
const latestExports = computed(() => {
  const entries: { label: string; job: Job }[] = []
  for (const format of ['images', 'yolo', 'coco'] as const) {
    const job = jobs.value.find(
      (j) => j.type === 'export' && j.status === 'succeeded' && exportFormatOf(j) === format
    )
    if (job) entries.push({ label: exportLabelOf(job), job })
  }
  return entries
})

const exportHistory = computed(() =>
  jobs.value.filter((j) => j.type === 'export' && j.status === 'succeeded' && j.result_path)
)

// 二级菜单：先选格式（YOLO 给 YOLOv5/v8/v11，COCO JSON 给 DETR/MMDetection 等），再选划分比例
const splitChildren = (format: 'yolo' | 'coco') => [
  { label: '8:2 划分（推荐）— train 80% / val 20%', key: `${format}:0.8` },
  { label: '7:3 划分 — train 70% / val 30%', key: `${format}:0.7` },
  { label: '9:1 划分 — train 90% / val 10%', key: `${format}:0.9` },
  { label: '不划分 — 全部平铺导出', key: `${format}:0` }
]
const datasetExportOptions = [
  { label: 'YOLO 格式（txt · YOLOv5/v8/v11）', key: 'yolo', children: splitChildren('yolo') },
  { label: 'COCO 格式（json · DETR/MMDetection）', key: 'coco', children: splitChildren('coco') }
]
const imagesExportOptions = [
  { label: '随机抽查 30 张（推荐，快速核对效果）', key: '30' },
  { label: '导出全部图片（较慢）', key: '0' }
]

function isActive(job: Job) {
  return job.status === 'pending' || job.status === 'running'
}

function paramsOf(job: Job): { format?: string; train_ratio?: number; sample?: number } {
  try {
    return JSON.parse(job.params_json || '{}')
  } catch {
    return {}
  }
}

function exportFormatOf(job: Job): 'images' | 'yolo' | 'coco' {
  const f = paramsOf(job).format
  return f === 'images' || f === 'coco' ? f : 'yolo'
}

function splitLabelOf(job: Job) {
  const ratio = paramsOf(job).train_ratio
  if (!ratio || ratio <= 0 || ratio >= 1) return '未划分'
  const t = Math.round(ratio * 100)
  return `train/val ${t % 10 === 0 ? `${t / 10}:${10 - t / 10}` : `${t}:${100 - t}`}`
}

function exportLabelOf(job: Job) {
  const format = exportFormatOf(job)
  if (format === 'images') {
    const sample = paramsOf(job).sample
    return sample && sample > 0 ? `识别图片（随机抽查 ${job.total} 张）` : '识别图片（带框标注）'
  }
  if (format === 'coco') return `COCO 数据集（${splitLabelOf(job)}）`
  return `YOLO 数据集（${splitLabelOf(job)}）`
}

function formatTagOf(job: Job) {
  const format = exportFormatOf(job)
  if (format === 'images') return '图片'
  return format.toUpperCase()
}

function formatTagType(job: Job) {
  const format = exportFormatOf(job)
  if (format === 'yolo') return 'info'
  if (format === 'coco') return 'success'
  return 'default'
}

onMounted(refreshAll)
onUnmounted(stopPolling)

function asArray<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

function apiErrorOf(error: unknown): string {
  const detail = (error as { response?: { data?: { error?: string } } }).response?.data?.error
  if (detail) return detail
  return error instanceof Error ? error.message : ''
}

async function refreshAll() {
  engine.value = await getEngine()
  projects.value = asArray(await listProjects())
  selectedProjectId.value = projects.value[0]?.id ?? null
  if (selectedProjectId.value) {
    datasets.value = asArray(await listDatasets(selectedProjectId.value))
    selectedDatasetId.value = datasets.value[0]?.id ?? null
  }
  if (selectedDatasetId.value) {
    await refreshDatasetDetail()
  }
}

async function refreshDatasetDetail() {
  if (!selectedDatasetId.value) return
  classes.value = asArray(await listLabelClasses(selectedDatasetId.value))
  images.value = asArray(await listImages(selectedDatasetId.value))
  jobs.value = asArray(await listJobs(selectedDatasetId.value))
  if (jobs.value.some(isActive)) startPolling()
}

let pollTimer: ReturnType<typeof setInterval> | null = null

function startPolling() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    try {
      await refreshDatasetDetail()
    } catch {
      // 网络抖动时下个周期重试
    }
    if (!jobs.value.some(isActive)) stopPolling()
  }, 1200)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function prepareTask() {
  if (!naturalText.value.trim()) {
    message.error('先输入要检测的目标')
    return
  }
  preparing.value = true
  try {
    optimizedPrompt.value = await optimizePrompt(naturalText.value)
    await ensureTask()
    classes.value = promptToClasses(optimizedPrompt.value)
    classes.value = await saveLabelClasses(selectedDatasetId.value!, classes.value)
    await refreshDatasetDetail()
    message.success('任务配置已生成，可以上传素材了')
  } catch (error) {
    const detail = apiErrorOf(error) || '任务配置失败'
    optimizedPrompt.value = detail
    message.error(detail)
  } finally {
    preparing.value = false
  }
}

async function ensureTask() {
  const projectName = taskName.value.trim() || '默认标注任务'
  if (!selectedProjectId.value) {
    const project = await createProject({ name: projectName, description: '新手模式自动创建' })
    selectedProjectId.value = project.id
  }
  datasets.value = asArray(await listDatasets(selectedProjectId.value))
  const existing = datasets.value.find((dataset) => dataset.name === projectName)
  if (existing) {
    selectedDatasetId.value = existing.id
    return
  }
  const dataset = await createDataset(selectedProjectId.value, {
    name: projectName,
    default_prompt: optimizedPrompt.value,
    default_mode: 0
  })
  selectedDatasetId.value = dataset.id
}

function promptToClasses(prompt: string): LabelClass[] {
  const colors = ['#0066cc', '#ff9500', '#34c759', '#af52de', '#ff2d55', '#5ac8fa']
  return prompt
    .split('</c>')
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part, index) => {
      const name = part.replace(/[^a-zA-Z0-9_\-\s]/g, '').trim() || `class_${index + 1}`
      const words = name.toLowerCase().split(/\s+/).filter(Boolean)
      return {
        name,
        prompt_fragment: part,
        color: colors[index % colors.length],
        match_keywords: Array.from(new Set([name.toLowerCase(), ...words]))
      }
    })
}

function useBikePreset() {
  taskName.value = '骑行安全检测'
  naturalText.value = '标出自行车；人；以及头盔三类'
}

async function customUpload(options: UploadCustomRequestOptions) {
  if (!selectedDatasetId.value || !(options.file.file instanceof File)) return
  try {
    const uploaded = await uploadFile(selectedDatasetId.value, options.file.file)
    options.onFinish()
    if (uploaded.kind === 'video') {
      try {
        await submitExtract(selectedDatasetId.value, { video_image_id: uploaded.id, fps: extractFps.value })
        message.success(
          extractFps.value > 0
            ? `视频上传完成，正在自动拆帧（每秒 ${extractFps.value} 帧）`
            : '视频上传完成，正在自动拆帧（每一帧都会参与检测）'
        )
      } catch (error) {
        message.error(apiErrorOf(error) || '自动拆帧提交失败')
      }
    } else {
      message.success('上传完成')
    }
    await refreshDatasetDetail()
  } catch (error) {
    options.onError()
    message.error(apiErrorOf(error) || '上传失败')
  }
}

async function detectNow(scope: 'new' | 'all') {
  if (!selectedDatasetId.value) return
  try {
    await submitDetect(selectedDatasetId.value, {
      prompt: optimizedPrompt.value,
      mode: 0,
      threads: engine.value?.threads || 8,
      scope,
      max_box_ratio: filterBlanketBoxes.value ? blanketRatio : 0
    })
    message.success(scope === 'all' ? '全量重新检测已提交（人工修改过的图片会保留）' : '检测任务已提交（只处理新增素材）')
    await refreshDatasetDetail()
  } catch (error) {
    message.error(apiErrorOf(error) || '检测任务提交失败')
  }
}

async function cleanExistingBoxes() {
  if (!selectedDatasetId.value) return
  try {
    const { removed } = await cleanBoxes(selectedDatasetId.value, blanketRatio)
    message.success(removed > 0 ? `已清理 ${removed} 个超大框` : '没有发现超大框')
    await refreshDatasetDetail()
  } catch (error) {
    message.error(apiErrorOf(error) || '清理失败')
  }
}

function onDatasetExport(key: string) {
  const [format, ratio] = key.split(':')
  if (!ratio) return // 点击的是一级菜单（YOLO/COCO），等待选择子项
  exportNow(format as 'yolo' | 'coco', Number(ratio))
}

function onImagesExport(key: string) {
  exportNow('images', 0, Number(key))
}

async function exportNow(format: 'yolo' | 'coco' | 'images', trainRatio = 0.8, sample = 0) {
  if (!selectedDatasetId.value) return
  try {
    await submitExport(selectedDatasetId.value, format, trainRatio, sample)
    const label = format === 'images' ? (sample > 0 ? `随机 ${sample} 张识别图片` : '识别图片') : `${format.toUpperCase()} 数据集`
    message.success(`${label}导出中…`)
    await refreshDatasetDetail()
  } catch (error) {
    message.error(apiErrorOf(error) || '导出任务提交失败')
  }
}

async function pauseJob(job: Job) {
  try {
    await cancelJob(job.id)
    message.info('已暂停。点“开始检测”会从剩余图片继续，已完成的结果都保留')
    await refreshDatasetDetail()
  } catch (error) {
    message.error(apiErrorOf(error) || '暂停失败')
  }
}

async function removeExport(job: Job) {
  try {
    await deleteJob(job.id)
    message.success('已删除该导出及 ZIP 文件')
    await refreshDatasetDetail()
  } catch (error) {
    message.error(apiErrorOf(error) || '删除失败')
  }
}

// 清理历史导出，但每种格式保留最新一份，避免误删唯一可用的包
async function cleanOldExports() {
  const keep = new Set(latestExports.value.map((e) => e.job.id))
  const removable = exportHistory.value.filter((j) => !keep.has(j.id))
  if (!removable.length) {
    message.info('没有可清理的旧导出')
    return
  }
  let ok = 0
  for (const job of removable) {
    try {
      await deleteJob(job.id)
      ok++
    } catch {
      // 单条失败不阻塞其余清理
    }
  }
  message.success(`已清理 ${ok} 个旧导出（每种格式保留最新一份）`)
  await refreshDatasetDetail()
}

function frameCountOf(video: ImageItem) {
  return images.value.filter((img) => img.kind === 'frame' && img.source_video === video.file_name).length
}

function extractJobOf(video: ImageItem): Job | undefined {
  return jobs.value.find((job) => {
    if (job.type !== 'extract' || !isActive(job)) return false
    try {
      return JSON.parse(job.params_json || '{}').video_image_id === video.id
    } catch {
      return false
    }
  })
}

function materialDesc(item: ImageItem) {
  if (item.kind === 'video') {
    const active = extractJobOf(item)
    if (active) {
      return active.total ? `视频 · 拆帧中 ${active.processed}/${active.total}…` : '视频 · 拆帧中…'
    }
    const count = frameCountOf(item)
    return count ? `视频 · 已拆 ${count} 帧` : '视频 · 等待拆帧'
  }
  return `图片 · ${item.width || '-'} x ${item.height || '-'}`
}

function progressOf(job: Job) {
  if (job.status === 'succeeded') return 100
  return job.total ? Math.round((job.processed / job.total) * 100) : 0
}

function jobTitle(job: Job) {
  if (job.type === 'detect') return '检测'
  if (job.type === 'extract') return '拆帧'
  if (job.type === 'export') {
    const format = exportFormatOf(job)
    if (format === 'images') return '导出识别图片'
    return `导出 ${format.toUpperCase()}`
  }
  return '任务'
}

function statusText(job: Job) {
  if (job.status === 'failed') return '失败'
  if (job.status === 'succeeded') return `完成 · ${job.processed}/${job.total}`
  if (job.status === 'canceled') return '已取消'
  return `进行中 · ${job.processed}/${job.total}`
}

function statusTagType(status: Job['status']) {
  if (status === 'succeeded') return 'success'
  if (status === 'failed') return 'error'
  if (status === 'canceled') return 'warning'
  return 'info'
}

function timeOf(job: Job) {
  if (!job.created_at) return ''
  const d = new Date(job.created_at)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function dateTimeOf(job: Job) {
  if (!job.created_at) return ''
  const d = new Date(job.created_at)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
</script>
