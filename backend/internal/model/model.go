package model

import "time"

const (
	ImageKindImage = "image"
	ImageKindVideo = "video"
	ImageKindFrame = "frame"

	OriginAI     = "ai"
	OriginManual = "manual"

	JobExtract = "extract"
	JobDetect  = "detect"
	JobExport  = "export"

	// DetectScopeNew only runs images that have never produced detections;
	// DetectScopeAll re-runs everything except manually reviewed images.
	DetectScopeNew = "new"
	DetectScopeAll = "all"

	ImageStatusDetected = "detected"

	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"

	ModeHybrid = 0
	ModeSlow   = 1
	ModeFast   = 2
)

type Project struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Dataset struct {
	ID            int64     `json:"id"`
	ProjectID     int64     `json:"project_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	DefaultPrompt string    `json:"default_prompt"`
	DefaultMode   int       `json:"default_mode"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type LabelClass struct {
	ID             int64    `json:"id"`
	DatasetID      int64    `json:"dataset_id"`
	ClassIndex     int      `json:"class_index"`
	Name           string   `json:"name"`
	PromptFragment string   `json:"prompt_fragment"`
	Color          string   `json:"color"`
	MatchKeywords  []string `json:"match_keywords"`
}

type Image struct {
	ID          int64     `json:"id"`
	DatasetID   int64     `json:"dataset_id"`
	JobID       *int64    `json:"job_id,omitempty"`
	Kind        string    `json:"kind"`
	SourceVideo string    `json:"source_video"`
	FrameIndex  *int      `json:"frame_index,omitempty"`
	FilePath    string    `json:"file_path,omitempty"`
	FileName    string    `json:"file_name"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Detection struct {
	ID           int64      `json:"id,omitempty"`
	ImageID      int64      `json:"image_id,omitempty"`
	LabelClassID *int64     `json:"label_class_id"`
	RawLabel     string     `json:"raw_label"`
	Box          [4]float64 `json:"box"`
	Score        float64    `json:"score,omitempty"`
	Origin       string     `json:"origin"`
	Reviewed     bool       `json:"reviewed"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
}

type LocateDetection struct {
	Label string     `json:"label"`
	Box   [4]float64 `json:"box"`
	Score float64    `json:"score,omitempty"`
}

type LocateResult struct {
	Detections []LocateDetection `json:"detections"`
}

type Job struct {
	ID         int64     `json:"id"`
	DatasetID  int64     `json:"dataset_id"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	Prompt     string    `json:"prompt"`
	Mode       int       `json:"mode"`
	Threads    int       `json:"threads"`
	Total      int       `json:"total"`
	Processed  int       `json:"processed"`
	Error      string    `json:"error,omitempty"`
	ParamsJSON string    `json:"params_json,omitempty"`
	ResultPath string    `json:"result_path,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type EngineStatus struct {
	Loaded    bool   `json:"loaded"`
	ModelPath string `json:"model_path"`
	Threads   int    `json:"threads"`
	ABIVersion int   `json:"abi_version"`
	Error      string `json:"error,omitempty"`
}
