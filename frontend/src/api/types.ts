export interface Project {
  id: number
  name: string
  description: string
  created_at: string
  updated_at: string
}

export interface Dataset {
  id: number
  project_id: number
  name: string
  description: string
  default_prompt: string
  default_mode: number
}

export interface LabelClass {
  id?: number
  dataset_id?: number
  class_index?: number
  name: string
  prompt_fragment: string
  color: string
  match_keywords: string[]
}

export interface ImageItem {
  id: number
  dataset_id: number
  kind: 'image' | 'video' | 'frame'
  source_video: string
  file_name: string
  width: number
  height: number
  status: string
  created_at: string
}

export interface Detection {
  id?: number
  image_id?: number
  label_class_id: number | null
  raw_label: string
  box: [number, number, number, number]
  score?: number
  origin: 'ai' | 'manual'
  reviewed: boolean
}

export interface Job {
  id: number
  dataset_id: number
  type: 'extract' | 'detect' | 'export'
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled'
  prompt: string
  mode: number
  threads: number
  total: number
  processed: number
  error?: string
  params_json?: string
  result_path?: string
  created_at?: string
}

export interface EngineStatus {
  loaded: boolean
  model_path: string
  threads: number
  abi_version: number
  error?: string
}
