package service

import (
	"context"
	"encoding/json"
	"errors"

	"ai-auto-annotator/backend/internal/engine"
	"ai-auto-annotator/backend/internal/model"
	"ai-auto-annotator/backend/internal/repository"
)

type ProgressFunc func(job model.Job)

type DetectService struct {
	store  *repository.Store
	engine engine.InferenceEngine
}

func NewDetectService(store *repository.Store, eng engine.InferenceEngine) *DetectService {
	return &DetectService{store: store, engine: eng}
}

func (s *DetectService) Run(ctx context.Context, job model.Job, publish ProgressFunc) error {
	status := s.engine.Status()
	if !status.Loaded {
		if status.Error != "" {
			return errors.New(status.Error)
		}
		return errors.New("inference engine is not loaded")
	}
	// Manually corrected images are always excluded (protecting human work);
	// scope "new" further limits the run to never-detected images so batch
	// uploads only pay for the new material.
	images, err := s.store.ListImagesForDetect(job.DatasetID, detectScopeOf(job))
	if err != nil {
		return err
	}
	classes, err := s.store.ListLabelClasses(job.DatasetID)
	if err != nil {
		return err
	}
	if job.Prompt == "" {
		ds, err := s.store.GetDataset(job.DatasetID)
		if err != nil {
			return err
		}
		job.Prompt = ds.DefaultPrompt
		if job.Prompt == "" {
			job.Prompt = DefaultPrompt(classes)
		}
	}
	total := len(images)
	_ = s.store.UpdateJobProgress(job.ID, 0, total)
	if total == 0 {
		return s.store.SetJobStatus(job.ID, model.StatusSucceeded, "", "")
	}
	fullPrompt := BuildPrompt(job.Prompt)
	maxBoxRatio := detectMaxBoxRatioOf(job)
	for i, img := range images {
		select {
		case <-ctx.Done():
			_ = s.store.SetJobStatus(job.ID, model.StatusCanceled, "canceled", "")
			return ctx.Err()
		default:
		}
		raw, err := s.engine.Locate(ctx, img.FilePath, fullPrompt, job.Mode)
		if err != nil {
			_ = s.store.SetImageStatus(img.ID, "failed")
		} else {
			var dets []model.Detection
			for _, d := range raw {
				labelID, ok := MapLabelToClassID(d.Label, classes)
				if !ok {
					continue
				}
				box, ok := ClipBox(d.Box, img.Width, img.Height)
				if !ok {
					continue
				}
				if IsOversizedBox(box, img.Width, img.Height, maxBoxRatio) {
					continue
				}
				dets = append(dets, model.Detection{
					LabelClassID: labelID,
					RawLabel:     d.Label,
					Box:          box,
					Score:        d.Score,
					Origin:       model.OriginAI,
					Reviewed:     false,
				})
			}
			_ = s.store.ReplaceDetections(img.ID, dets)
			_ = s.store.SetImageStatus(img.ID, model.ImageStatusDetected)
		}
		_ = s.store.UpdateJobProgress(job.ID, i+1, total)
		if publish != nil {
			updated, _ := s.store.GetJob(job.ID)
			publish(updated)
		}
	}
	return s.store.SetJobStatus(job.ID, model.StatusSucceeded, "", "")
}

func detectScopeOf(job model.Job) string {
	var p struct {
		Scope string `json:"scope"`
	}
	if job.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(job.ParamsJSON), &p)
	}
	if p.Scope == model.DetectScopeAll {
		return model.DetectScopeAll
	}
	return model.DetectScopeNew
}

// detectMaxBoxRatioOf reads the optional blanket-box filter threshold (fraction
// of image area). 0 or unset disables filtering.
func detectMaxBoxRatioOf(job model.Job) float64 {
	var p struct {
		MaxBoxRatio float64 `json:"max_box_ratio"`
	}
	if job.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(job.ParamsJSON), &p)
	}
	return p.MaxBoxRatio
}
