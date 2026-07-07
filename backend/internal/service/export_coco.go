package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"ai-auto-annotator/backend/internal/model"
)

type cocoImage struct {
	ID       int64  `json:"id"`
	FileName string `json:"file_name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type cocoAnnotation struct {
	ID         int64      `json:"id"`
	ImageID    int64      `json:"image_id"`
	CategoryID int        `json:"category_id"`
	BBox       [4]float64 `json:"bbox"` // [x, y, w, h] in pixels
	Area       float64    `json:"area"`
	IsCrowd    int        `json:"iscrowd"`
}

type cocoCategory struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Supercategory string `json:"supercategory"`
}

type cocoFile struct {
	Info        map[string]any   `json:"info"`
	Images      []cocoImage      `json:"images"`
	Annotations []cocoAnnotation `json:"annotations"`
	Categories  []cocoCategory   `json:"categories"`
}

// ExportCOCO produces a COCO-format dataset zip (the convention used by
// DETR/RT-DETR/MMDetection and most non-YOLO detectors): images/<split>/ plus
// annotations/instances_<split>.json. Splitting reuses the same deterministic
// AssignSplits as YOLO, so an image keeps the same side across formats too.
func (s *ExportService) ExportCOCO(ctx context.Context, job model.Job, trainRatio float64) (string, error) {
	ds, err := s.store.GetDataset(job.DatasetID)
	if err != nil {
		return "", err
	}
	classes, err := s.store.ListLabelClasses(job.DatasetID)
	if err != nil {
		return "", err
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].ClassIndex < classes[j].ClassIndex })
	images, err := s.store.ListDetectableImages(job.DatasetID)
	if err != nil {
		return "", err
	}
	splitEnabled := trainRatio > 0 && trainRatio < 1
	splits := AssignSplits(images, trainRatio)

	outDir := filepath.Join(s.exportDir, fmt.Sprintf("job-%d", job.ID))
	if err := os.RemoveAll(outDir); err != nil {
		return "", err
	}
	var splitNames []string
	if splitEnabled {
		splitNames = []string{SplitTrain, SplitVal}
	} else {
		splitNames = []string{"all"}
	}
	if err := os.MkdirAll(filepath.Join(outDir, "annotations"), 0o755); err != nil {
		return "", err
	}
	files := map[string]*cocoFile{}
	categories := make([]cocoCategory, 0, len(classes))
	// COCO category IDs start at 1 by convention.
	for _, c := range classes {
		categories = append(categories, cocoCategory{ID: c.ClassIndex + 1, Name: c.Name, Supercategory: "none"})
	}
	for _, sp := range splitNames {
		imgDir := "images"
		if splitEnabled {
			imgDir = filepath.Join("images", sp)
		}
		if err := os.MkdirAll(filepath.Join(outDir, imgDir), 0o755); err != nil {
			return "", err
		}
		files[sp] = &cocoFile{
			Info: map[string]any{
				"description": fmt.Sprintf("%s (%s split)", ds.Name, sp),
				"date_created": time.Now().Format("2006-01-02"),
			},
			Images:      []cocoImage{},
			Annotations: []cocoAnnotation{},
			Categories:  categories,
		}
	}

	catIDByClassID := map[int64]int{}
	for _, c := range classes {
		catIDByClassID[c.ID] = c.ClassIndex + 1
	}
	boxesByClassID := map[int64]int{}
	trainCount, valCount, boxCount := 0, 0, 0
	var annID int64 = 1
	sources := newSourceCounter()
	for i, img := range images {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		sp := "all"
		if splitEnabled {
			sp = splits[i]
		}
		if splits[i] == SplitVal {
			valCount++
		} else {
			trainCount++
		}
		sources.add(sourceNameOf(img))
		stem, ext := exportStem(img)
		imgDir := "images"
		if splitEnabled {
			imgDir = filepath.Join("images", sp)
		}
		if err := copyFile(img.FilePath, filepath.Join(outDir, imgDir, stem+ext)); err != nil {
			return "", fmt.Errorf("copy image %d: %w", img.ID, err)
		}
		f := files[sp]
		f.Images = append(f.Images, cocoImage{ID: img.ID, FileName: stem + ext, Width: img.Width, Height: img.Height})

		dets, err := s.store.GetDetections(img.ID)
		if err != nil {
			return "", err
		}
		for _, d := range dets {
			if d.LabelClassID == nil {
				continue
			}
			catID, ok := catIDByClassID[*d.LabelClassID]
			if !ok {
				continue
			}
			box, ok := ClipBox(d.Box, img.Width, img.Height)
			if !ok {
				continue
			}
			w := box[2] - box[0]
			h := box[3] - box[1]
			f.Annotations = append(f.Annotations, cocoAnnotation{
				ID:         annID,
				ImageID:    img.ID,
				CategoryID: catID,
				BBox:       [4]float64{box[0], box[1], w, h},
				Area:       w * h,
				IsCrowd:    0,
			})
			annID++
			boxesByClassID[*d.LabelClassID]++
			boxCount++
		}
		_ = s.store.UpdateJobProgress(job.ID, i+1, len(images))
	}

	for sp, f := range files {
		raw, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(outDir, "annotations", "instances_"+sp+".json"), raw, 0o644); err != nil {
			return "", err
		}
	}

	manifest := exportManifest{
		Dataset:     ds.Name,
		Format:      "coco",
		FormatLabel: "COCO 数据集（JSON，DETR/MMDetection）",
		JobID:       job.ID,
		ExportedAt:  time.Now().Format("2006-01-02 15:04:05"),
		ImageCount:  len(images),
		BoxCount:    boxCount,
		Classes:     manifestClasses(classes, boxesByClassID),
		Sources:     sources.list(),
	}
	if splitEnabled {
		manifest.TrainRatio = trainRatio
		manifest.TrainCount = trainCount
		manifest.ValCount = valCount
	}
	if err := writeManifest(outDir, manifest); err != nil {
		return "", err
	}
	zipPath := filepath.Join(s.exportDir, exportZipName(ds.Name, "COCO", classes, job.ID))
	if err := zipDir(outDir, zipPath); err != nil {
		return "", err
	}
	_ = os.RemoveAll(outDir)
	return zipPath, nil
}
