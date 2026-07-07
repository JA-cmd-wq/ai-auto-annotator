package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"ai-auto-annotator/backend/internal/model"
	"ai-auto-annotator/backend/internal/repository"
)

type ExportService struct {
	store      *repository.Store
	exportDir  string
	sidecarURL string
	client     *http.Client
}

func NewExportService(store *repository.Store, exportDir, sidecarURL string) *ExportService {
	return &ExportService{
		store:      store,
		exportDir:  exportDir,
		sidecarURL: normalizeSidecarURL(sidecarURL),
		client:     &http.Client{Timeout: 10 * time.Minute},
	}
}

type exportManifest struct {
	Dataset     string           `json:"dataset"`
	Format      string           `json:"format"`
	FormatLabel string           `json:"format_label"`
	JobID       int64            `json:"job_id"`
	ExportedAt  string           `json:"exported_at"`
	TrainRatio  float64          `json:"train_ratio,omitempty"`
	ImageCount  int              `json:"image_count"`
	SampledFrom int              `json:"sampled_from,omitempty"`
	TrainCount  int              `json:"train_count,omitempty"`
	ValCount    int              `json:"val_count,omitempty"`
	BoxCount    int              `json:"box_count"`
	Classes     []manifestClass  `json:"classes"`
	Sources     []manifestSource `json:"sources"`
}

type manifestClass struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Boxes int    `json:"boxes"`
}

type manifestSource struct {
	Name   string `json:"name"`
	Images int    `json:"images"`
}

// ExportYOLO produces a ready-to-train dataset zip. With trainRatio in (0,1)
// the layout follows the Ultralytics convention (images/train|val,
// labels/train|val + data.yaml); otherwise images/ and labels/ stay flat.
// Stems are prefixed with the image ID so frames from different videos (which
// all share names like frame_000000.jpg) can never overwrite each other.
func (s *ExportService) ExportYOLO(ctx context.Context, job model.Job, trainRatio float64) (string, error) {
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
	var subDirs []string
	if splitEnabled {
		subDirs = []string{SplitTrain, SplitVal}
	} else {
		subDirs = []string{""}
	}
	for _, sub := range subDirs {
		if err := os.MkdirAll(filepath.Join(outDir, "images", sub), 0o755); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Join(outDir, "labels", sub), 0o755); err != nil {
			return "", err
		}
	}
	var classLines []string
	for _, c := range classes {
		classLines = append(classLines, c.Name)
	}
	if err := os.WriteFile(filepath.Join(outDir, "classes.txt"), []byte(strings.Join(classLines, "\n")), 0o644); err != nil {
		return "", err
	}

	boxesByClassID := map[int64]int{}
	trainCount, valCount, boxCount := 0, 0, 0
	sources := newSourceCounter()
	for i, img := range images {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		dets, err := s.store.GetDetections(img.ID)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, d := range dets {
			if d.LabelClassID == nil {
				continue
			}
			classIndex := classIndexByID(classes, *d.LabelClassID)
			if classIndex < 0 {
				continue
			}
			y, ok, err := ToYOLO(d.Box, img.Width, img.Height)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
			lines = append(lines, fmt.Sprintf("%d %.6f %.6f %.6f %.6f", classIndex, y.CX, y.CY, y.W, y.H))
			boxesByClassID[*d.LabelClassID]++
			boxCount++
		}
		sub := ""
		if splitEnabled {
			sub = splits[i]
		}
		if splits[i] == SplitVal {
			valCount++
		} else {
			trainCount++
		}
		sources.add(sourceNameOf(img))
		stem, ext := exportStem(img)
		if err := copyFile(img.FilePath, filepath.Join(outDir, "images", sub, stem+ext)); err != nil {
			return "", fmt.Errorf("copy image %d: %w", img.ID, err)
		}
		// Empty label files are intentional: they mark background images.
		if err := os.WriteFile(filepath.Join(outDir, "labels", sub, stem+".txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return "", err
		}
		_ = s.store.UpdateJobProgress(job.ID, i+1, len(images))
	}

	if err := os.WriteFile(filepath.Join(outDir, "data.yaml"), []byte(dataYAML(classes, splitEnabled)), 0o644); err != nil {
		return "", err
	}
	manifest := exportManifest{
		Dataset:     ds.Name,
		Format:      "yolo",
		FormatLabel: "YOLO 数据集（images + labels）",
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

	zipPath := filepath.Join(s.exportDir, exportZipName(ds.Name, "YOLO", classes, job.ID))
	if err := zipDir(outDir, zipPath); err != nil {
		return "", err
	}
	_ = os.RemoveAll(outDir)
	return zipPath, nil
}

func exportStem(img model.Image) (string, string) {
	base := filepath.Base(img.FileName)
	ext := filepath.Ext(base)
	if ext == "" {
		ext = filepath.Ext(img.FilePath)
	}
	return fmt.Sprintf("%06d_%s", img.ID, strings.TrimSuffix(base, filepath.Ext(base))), ext
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ExportAnnotated renders each detectable image with its detections drawn on
// top (via the sidecar /render endpoint) and bundles the JPEGs into a zip,
// grouped into one folder per source video (standalone uploads go to 单张图片/).
// Images with no detections are included as-is (original frame).
// sampleCount > 0 exports a random spot-check subset instead of everything.
func (s *ExportService) ExportAnnotated(ctx context.Context, job model.Job, sampleCount int) (string, error) {
	ds, err := s.store.GetDataset(job.DatasetID)
	if err != nil {
		return "", err
	}
	classes, err := s.store.ListLabelClasses(job.DatasetID)
	if err != nil {
		return "", err
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].ClassIndex < classes[j].ClassIndex })
	colorByID := map[int64]string{}
	for _, c := range classes {
		colorByID[c.ID] = c.Color
	}
	images, err := s.store.ListDetectableImages(job.DatasetID)
	if err != nil {
		return "", err
	}
	totalAvailable := len(images)
	if sampleCount > 0 && len(images) > sampleCount {
		rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
		rnd.Shuffle(len(images), func(i, j int) { images[i], images[j] = images[j], images[i] })
		images = images[:sampleCount]
		sort.Slice(images, func(i, j int) bool { return images[i].ID < images[j].ID })
	}
	outDir, err := filepath.Abs(filepath.Join(s.exportDir, fmt.Sprintf("job-%d", job.ID)))
	if err != nil {
		return "", err
	}
	imagesDir := filepath.Join(outDir, "images")
	if err := os.RemoveAll(outDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return "", err
	}
	boxesByClassID := map[int64]int{}
	boxCount := 0
	sources := newSourceCounter()
	for i, img := range images {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		dets, err := s.store.GetDetections(img.ID)
		if err != nil {
			return "", err
		}
		payload := make([]map[string]any, 0, len(dets))
		for _, d := range dets {
			color := ""
			if d.LabelClassID != nil {
				color = colorByID[*d.LabelClassID]
				boxesByClassID[*d.LabelClassID]++
			}
			boxCount++
			payload = append(payload, map[string]any{
				"label": d.RawLabel,
				"box":   d.Box,
				"color": color,
			})
		}
		imgAbs, err := filepath.Abs(img.FilePath)
		if err != nil {
			imgAbs = img.FilePath
		}
		source := sourceNameOf(img)
		sources.add(source)
		outPath := filepath.Join(imagesDir, sanitizeToken(source, 60), annotatedFileName(img))
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return "", err
		}
		body, err := json.Marshal(map[string]any{
			"image":      imgAbs,
			"output":     outPath,
			"detections": payload,
		})
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sidecarURL+"/render", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("sidecar /render: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("sidecar /render returned HTTP %d for image %d", resp.StatusCode, img.ID)
		}
		_ = s.store.UpdateJobProgress(job.ID, i+1, len(images))
	}
	formatLabel := "识别图片（带框标注）"
	nameToken := "识别图片"
	if sampleCount > 0 && totalAvailable > len(images) {
		formatLabel = fmt.Sprintf("识别图片（随机抽查 %d/%d 张）", len(images), totalAvailable)
		nameToken = fmt.Sprintf("识别图片抽查%d张", len(images))
	}
	manifest := exportManifest{
		Dataset:     ds.Name,
		Format:      "images",
		FormatLabel: formatLabel,
		JobID:       job.ID,
		ExportedAt:  time.Now().Format("2006-01-02 15:04:05"),
		ImageCount:  len(images),
		BoxCount:    boxCount,
		Classes:     manifestClasses(classes, boxesByClassID),
		Sources:     sources.list(),
	}
	if totalAvailable > len(images) {
		manifest.SampledFrom = totalAvailable
	}
	if err := writeManifest(outDir, manifest); err != nil {
		return "", err
	}
	zipPath := filepath.Join(s.exportDir, exportZipName(ds.Name, nameToken, classes, job.ID))
	if err := zipDir(outDir, zipPath); err != nil {
		return "", err
	}
	_ = os.RemoveAll(outDir)
	return zipPath, nil
}

// annotatedFileName keeps frame names as-is (unique within their video folder)
// and prefixes standalone uploads with the image ID so same-named uploads
// cannot overwrite each other.
func annotatedFileName(img model.Image) string {
	if img.Kind == model.ImageKindFrame && img.SourceVideo != "" {
		return img.FileName
	}
	return fmt.Sprintf("%d_%s", img.ID, img.FileName)
}

func sourceNameOf(img model.Image) string {
	if img.Kind == model.ImageKindFrame && img.SourceVideo != "" {
		return img.SourceVideo
	}
	return "单张图片"
}

// sourceCounter tallies images per source while preserving insertion order.
type sourceCounter struct {
	order  []string
	counts map[string]int
}

func newSourceCounter() *sourceCounter {
	return &sourceCounter{counts: map[string]int{}}
}

func (c *sourceCounter) add(name string) {
	if _, ok := c.counts[name]; !ok {
		c.order = append(c.order, name)
	}
	c.counts[name]++
}

func (c *sourceCounter) list() []manifestSource {
	out := make([]manifestSource, 0, len(c.order))
	for _, name := range c.order {
		out = append(out, manifestSource{Name: name, Images: c.counts[name]})
	}
	return out
}

func manifestClasses(classes []model.LabelClass, boxesByClassID map[int64]int) []manifestClass {
	out := make([]manifestClass, 0, len(classes))
	for _, c := range classes {
		out = append(out, manifestClass{Index: c.ClassIndex, Name: c.Name, Boxes: boxesByClassID[c.ID]})
	}
	return out
}

func dataYAML(classes []model.LabelClass, splitEnabled bool) string {
	var b strings.Builder
	b.WriteString("# Ultralytics YOLO dataset config（相对本文件所在目录解析）\n")
	if splitEnabled {
		b.WriteString("train: images/train\nval: images/val\n")
	} else {
		b.WriteString("train: images\nval: images\n")
	}
	fmt.Fprintf(&b, "nc: %d\n", len(classes))
	b.WriteString("names:\n")
	for _, c := range classes {
		fmt.Fprintf(&b, "  %d: %s\n", c.ClassIndex, c.Name)
	}
	return b.String()
}

func writeManifest(outDir string, m exportManifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "导出说明.txt"), []byte(exportReadme(m)), 0o644)
}

func exportReadme(m exportManifest) string {
	var b strings.Builder
	b.WriteString("导出说明\n========\n")
	fmt.Fprintf(&b, "任务：%s\n", m.Dataset)
	fmt.Fprintf(&b, "内容：%s\n", m.FormatLabel)
	fmt.Fprintf(&b, "导出时间：%s（任务 #%d）\n", m.ExportedAt, m.JobID)
	if m.TrainRatio > 0 {
		fmt.Fprintf(&b, "图片总数：%d（train %d / val %d，比例 %s）\n", m.ImageCount, m.TrainCount, m.ValCount, ratioText(m.TrainRatio))
	} else {
		fmt.Fprintf(&b, "图片总数：%d（未划分 train/val）\n", m.ImageCount)
	}
	fmt.Fprintf(&b, "标注框总数：%d\n", m.BoxCount)
	if len(m.Classes) > 0 {
		b.WriteString("类别统计：\n")
		for _, c := range m.Classes {
			fmt.Fprintf(&b, "  %d %s：%d 个框\n", c.Index, c.Name, c.Boxes)
		}
	}
	if len(m.Sources) > 0 {
		b.WriteString("素材来源：\n")
		for _, s := range m.Sources {
			fmt.Fprintf(&b, "  %s：%d 张\n", s.Name, s.Images)
		}
	}
	if m.Format == "yolo" {
		b.WriteString("\n训练提示：解压后可直接用 Ultralytics 训练：\n")
		b.WriteString("  yolo detect train data=data.yaml model=yolo11n.pt epochs=100 imgsz=640\n")
	}
	if m.Format == "coco" {
		b.WriteString("\n格式说明：COCO JSON 是 DETR / RT-DETR / MMDetection / Detectron2 等\n")
		b.WriteString("非 YOLO 检测器的通用格式；标注在 annotations/instances_<split>.json，\n")
		b.WriteString("bbox 为 [x, y, w, h] 像素坐标，category_id 从 1 开始。\n")
	}
	if m.Format == "yolo" || m.Format == "coco" {
		b.WriteString("说明：train/val 划分是确定性的——同一张图片在历次导出中的归属保持一致，\n")
		b.WriteString("视频相邻帧按时间块整体划入同一侧，避免验证集数据泄漏。\n")
	}
	return b.String()
}

// ratioText renders 0.8 as "8:2", 0.85 as "85:15".
func ratioText(trainRatio float64) string {
	t := int(math.Round(trainRatio * 100))
	if t%10 == 0 {
		return fmt.Sprintf("%d:%d", t/10, 10-t/10)
	}
	return fmt.Sprintf("%d:%d", t, 100-t)
}

// exportZipName builds a product-style download name like
// 骑行安全检测_YOLO_bicycles-people-helmets_20260707-1015_任务23.zip.
// The job ID keeps names unique even when exports finish in the same minute.
func exportZipName(datasetName, formatLabel string, classes []model.LabelClass, jobID int64) string {
	name := sanitizeToken(datasetName, 40)
	if name == "" {
		name = "dataset"
	}
	parts := []string{name, formatLabel}
	if cs := classSummary(classes); cs != "" {
		parts = append(parts, cs)
	}
	parts = append(parts, time.Now().Format("20060102-1504"), fmt.Sprintf("任务%d", jobID))
	return strings.Join(parts, "_") + ".zip"
}

func classSummary(classes []model.LabelClass) string {
	var names []string
	for _, c := range classes {
		if n := sanitizeToken(c.Name, 20); n != "" {
			names = append(names, n)
		}
		if len(names) == 3 {
			break
		}
	}
	if len(names) == 0 {
		return ""
	}
	s := strings.Join(names, "-")
	if len(classes) > 3 {
		s += fmt.Sprintf("等%d类", len(classes))
	}
	return s
}

// sanitizeToken makes a string safe for filenames and Content-Disposition:
// path/URL-hostile characters become '-', whitespace becomes '_' (QueryEscape
// would turn spaces into '+'), and length is capped.
func sanitizeToken(s string, maxRunes int) string {
	var b []rune
	for _, r := range strings.TrimSpace(s) {
		switch {
		case strings.ContainsRune(`/\:*?"<>|#%&+`, r):
			b = append(b, '-')
		case unicode.IsSpace(r):
			b = append(b, '_')
		case unicode.IsControl(r):
			// drop
		default:
			b = append(b, r)
		}
		if len(b) >= maxRunes {
			break
		}
	}
	return strings.Trim(string(b), "-_")
}

func classIndexByID(classes []model.LabelClass, id int64) int {
	for _, c := range classes {
		if c.ID == id {
			return c.ClassIndex
		}
	}
	return -1
}

func zipDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(raw)
		return err
	})
}
