package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ai-auto-annotator/backend/internal/model"
	"ai-auto-annotator/backend/internal/repository"
)

// ExtractService splits an uploaded video into frames by calling the Python
// sidecar's /extract endpoint (cv2), then registers each frame as an image of
// kind="frame" so the detect job can pick it up via ListDetectableImages.
type ExtractService struct {
	store        *repository.Store
	sidecarURL   string
	storageDir   string
	client       *http.Client
}

func NewExtractService(store *repository.Store, sidecarURL, storageDir string) *ExtractService {
	return &ExtractService{
		store:      store,
		sidecarURL: normalizeSidecarURL(sidecarURL),
		storageDir: storageDir,
		client:     &http.Client{Timeout: 10 * time.Minute},
	}
}

type extractParams struct {
	VideoImageID int64   `json:"video_image_id"`
	FPS          float64 `json:"fps"`
	MaxFrames    int     `json:"max_frames"`
}

type extractFrame struct {
	Path       string `json:"path"`
	FrameIndex int    `json:"frame_index"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

type extractResponse struct {
	Frames      []extractFrame `json:"frames"`
	FPS         float64        `json:"fps"`
	TotalFrames int            `json:"total_frames"`
	Sampled     int            `json:"sampled"`
}

func (s *ExtractService) Run(ctx context.Context, job model.Job, publish ProgressFunc) error {
	var params extractParams
	if job.ParamsJSON != "" {
		if err := json.Unmarshal([]byte(job.ParamsJSON), &params); err != nil {
			return fmt.Errorf("extract params: %w", err)
		}
	}
	if params.VideoImageID == 0 {
		return fmt.Errorf("extract params: video_image_id is required")
	}
	// FPS <= 0 means "every frame"; MaxFrames <= 0 means no cap.
	if params.FPS < 0 {
		params.FPS = 0
	}
	if params.MaxFrames < 0 {
		params.MaxFrames = 0
	}

	video, err := s.store.GetImage(params.VideoImageID)
	if err != nil {
		return err
	}
	if video.Kind != model.ImageKindVideo {
		return fmt.Errorf("image %d is not a video (kind=%s)", video.ID, video.Kind)
	}
	if video.DatasetID != job.DatasetID {
		return fmt.Errorf("video %d does not belong to dataset %d", video.ID, job.DatasetID)
	}

	outDir, err := filepath.Abs(filepath.Join(s.storageDir, fmt.Sprintf("dataset-%d", job.DatasetID), fmt.Sprintf("frames-job-%d", job.ID)))
	if err != nil {
		return err
	}
	videoAbs, err := filepath.Abs(video.FilePath)
	if err != nil {
		videoAbs = video.FilePath
	}

	body, err := json.Marshal(map[string]any{
		"video":      videoAbs,
		"output_dir": outDir,
		"fps":        params.FPS,
		"max_frames": params.MaxFrames,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sidecarURL+"/extract", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sidecar /extract: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("sidecar /extract read: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return fmt.Errorf("sidecar /extract: %s", e.Error)
		}
		return fmt.Errorf("sidecar /extract returned HTTP %d", resp.StatusCode)
	}
	var out extractResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("sidecar /extract decode: %w", err)
	}

	videoName := filepath.Base(video.FileName)

	// Re-extracting the same video replaces its previous frames (and their
	// detections via FK cascade) so frames never pile up in the dataset.
	stalePaths, err := s.store.DeleteFramesBySourceVideo(job.DatasetID, videoName)
	if err != nil {
		return err
	}
	for _, p := range stalePaths {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if p != "" && p != outDir && filepath.Dir(p) != outDir {
			_ = os.Remove(p)
		}
	}

	total := len(out.Frames)
	_ = s.store.UpdateJobProgress(job.ID, 0, total)
	if total == 0 {
		return s.store.SetJobStatus(job.ID, model.StatusSucceeded, "", "")
	}
	for i, fr := range out.Frames {
		select {
		case <-ctx.Done():
			_ = s.store.SetJobStatus(job.ID, model.StatusCanceled, "canceled", "")
			return ctx.Err()
		default:
		}
		frameIdx := fr.FrameIndex
		if _, err := s.store.CreateImage(model.Image{
			DatasetID:   job.DatasetID,
			JobID:       &job.ID,
			Kind:        model.ImageKindFrame,
			SourceVideo: videoName,
			FrameIndex:  &frameIdx,
			FilePath:    fr.Path,
			FileName:    filepath.Base(fr.Path),
			Width:       fr.Width,
			Height:      fr.Height,
			Status:      "ready",
		}); err != nil {
			return err
		}
		_ = s.store.UpdateJobProgress(job.ID, i+1, total)
		if publish != nil {
			updated, _ := s.store.GetJob(job.ID)
			publish(updated)
		}
	}
	return s.store.SetJobStatus(job.ID, model.StatusSucceeded, "", "")
}

func normalizeSidecarURL(u string) string {
	for len(u) > 0 && u[len(u)-1] == '/' {
		u = u[:len(u)-1]
	}
	return u
}
