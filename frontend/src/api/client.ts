import axios from 'axios'
import type { Dataset, Detection, EngineStatus, ImageItem, Job, LabelClass, Project } from './types'

export const api = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '/api/v1',
  timeout: 30000
})

export async function getEngine() {
  return (await api.get<EngineStatus>('/engine/status')).data
}

export async function listProjects() {
  return (await api.get<Project[]>('/projects')).data
}

export async function createProject(body: { name: string; description?: string }) {
  return (await api.post<Project>('/projects', body)).data
}

export async function listDatasets(projectId: number) {
  return (await api.get<Dataset[]>(`/projects/${projectId}/datasets`)).data
}

export async function createDataset(projectId: number, body: { name: string; description?: string; default_prompt?: string; default_mode?: number }) {
  return (await api.post<Dataset>(`/projects/${projectId}/datasets`, body)).data
}

export async function listLabelClasses(datasetId: number) {
  return (await api.get<LabelClass[]>(`/datasets/${datasetId}/label-classes`)).data
}

export async function saveLabelClasses(datasetId: number, classes: LabelClass[]) {
  return (await api.put<LabelClass[]>(`/datasets/${datasetId}/label-classes`, { classes })).data
}

export async function uploadFile(datasetId: number, file: File) {
  const body = new FormData()
  body.append('file', file)
  // 视频可达 2GB，禁用默认 30s 超时，避免大文件上传被掐断
  return (await api.post<ImageItem>(`/datasets/${datasetId}/uploads`, body, { timeout: 0 })).data
}

export async function listImages(datasetId: number) {
  return (await api.get<ImageItem[]>(`/datasets/${datasetId}/images`, { params: { page: 1, page_size: 2000 } })).data
}

export async function getDetections(imageId: number) {
  return (await api.get<Detection[]>(`/images/${imageId}/detections`)).data
}

export async function saveDetections(imageId: number, detections: Detection[]) {
  return (await api.put(`/images/${imageId}/detections`, { detections })).data
}

export async function submitDetect(
  datasetId: number,
  body: { prompt?: string; mode?: number; threads?: number; scope?: 'new' | 'all'; max_box_ratio?: number }
) {
  return (await api.post<Job>(`/datasets/${datasetId}/jobs/detect`, body)).data
}

// 删除数据集里过大的 AI 框（占画面比例超过 maxBoxRatio），人工框保留
export async function cleanBoxes(datasetId: number, maxBoxRatio: number) {
  return (await api.post<{ removed: number }>(`/datasets/${datasetId}/clean-boxes`, { max_box_ratio: maxBoxRatio })).data
}

// trainRatio 对 yolo/coco 生效（0 = 不划分）；sample 对 images 生效（0 = 全部）
export async function submitExport(
  datasetId: number,
  format: 'yolo' | 'coco' | 'images' = 'yolo',
  trainRatio = 0.8,
  sample = 0
) {
  return (await api.post<Job>(`/datasets/${datasetId}/jobs/export`, { format, train_ratio: trainRatio, sample })).data
}

export async function deleteJob(jobId: number) {
  return (await api.delete(`/jobs/${jobId}`)).data
}

export async function cancelJob(jobId: number) {
  return (await api.post(`/jobs/${jobId}/cancel`)).data
}

export async function submitExtract(datasetId: number, body: { video_image_id: number; fps?: number; max_frames?: number }) {
  return (await api.post<Job>(`/datasets/${datasetId}/jobs/extract`, body)).data
}

export async function listJobs(datasetId: number) {
  return (await api.get<Job[]>(`/datasets/${datasetId}/jobs`)).data
}

export async function optimizePrompt(text: string) {
  return (await api.post<{ prompt: string }>('/prompt/optimize', { text })).data.prompt
}

export function fileUrl(imageId: number) {
  return `${api.defaults.baseURL}/images/${imageId}/file`
}

export function jobResultUrl(jobId: number) {
  return `${api.defaults.baseURL}/jobs/${jobId}/result`
}
