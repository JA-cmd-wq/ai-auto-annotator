package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-auto-annotator/backend/internal/config"
	"ai-auto-annotator/backend/internal/engine"
	"ai-auto-annotator/backend/internal/model"
	"ai-auto-annotator/backend/internal/repository"
	"ai-auto-annotator/backend/internal/service"
	"ai-auto-annotator/backend/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type API struct {
	cfg     config.Config
	store   *repository.Store
	engine  engine.InferenceEngine
	prompts *service.PromptService
	hub     *ws.Hub
	// cancels maps a running job ID to its context.CancelFunc so the pause
	// endpoint can stop it; combined with incremental detect (scope=new) a
	// canceled detect run is effectively "paused" and resumable.
	cancels sync.Map
}

func NewRouter(cfg config.Config, store *repository.Store, eng engine.InferenceEngine, prompts *service.PromptService, hub *ws.Hub) *gin.Engine {
	api := &API{cfg: cfg, store: store, engine: eng, prompts: prompts, hub: hub}
	r := gin.Default()
	r.Use(cors(cfg.FrontendOrigin))
	v1 := r.Group("/api/v1")
	v1.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	v1.GET("/engine/status", api.engineStatus)
	v1.GET("/projects", api.listProjects)
	v1.POST("/projects", api.createProject)
	v1.GET("/projects/:pid", api.getProject)
	v1.PATCH("/projects/:pid", api.updateProject)
	v1.DELETE("/projects/:pid", api.deleteProject)
	v1.GET("/projects/:pid/datasets", api.listDatasets)
	v1.POST("/projects/:pid/datasets", api.createDataset)
	v1.GET("/datasets/:did", api.getDataset)
	v1.GET("/datasets/:did/label-classes", api.listLabelClasses)
	v1.PUT("/datasets/:did/label-classes", api.saveLabelClasses)
	v1.GET("/datasets/:did/images", api.listImages)
	v1.POST("/datasets/:did/uploads", api.upload)
	v1.GET("/images/:iid/file", api.imageFile)
	v1.GET("/images/:iid/detections", api.getDetections)
	v1.PUT("/images/:iid/detections", api.saveDetections)
	v1.GET("/datasets/:did/jobs", api.listJobs)
	v1.POST("/datasets/:did/jobs/detect", api.submitDetect)
	v1.POST("/datasets/:did/clean-boxes", api.cleanBoxes)
	v1.POST("/datasets/:did/jobs/extract", api.submitExtract)
	v1.POST("/datasets/:did/jobs/export", api.submitExport)
	v1.GET("/jobs/:jid", api.getJob)
	v1.GET("/jobs/:jid/result", api.jobResult)
	v1.POST("/jobs/:jid/cancel", api.cancelJob)
	v1.DELETE("/jobs/:jid", api.deleteJob)
	v1.POST("/prompt/optimize", api.optimizePrompt)
	v1.GET("/ws", hub.Handle)
	return r
}

func cors(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (a *API) engineStatus(c *gin.Context) { c.JSON(http.StatusOK, a.engine.Status()) }

func (a *API) listProjects(c *gin.Context) {
	items, err := a.store.ListProjects()
	respond(c, items, err)
}

func (a *API) createProject(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bind(c, &req) {
		return
	}
	name, err := validateName(req.Name, 100)
	if err != nil {
		bad(c, err)
		return
	}
	if len([]rune(req.Description)) > 1000 {
		bad(c, errors.New("description exceeds 1000 characters"))
		return
	}
	p, err := a.store.CreateProject(name, req.Description)
	respond(c, p, err)
}

func (a *API) getProject(c *gin.Context) {
	p, err := a.store.GetProject(idParam(c, "pid"))
	respond(c, p, err)
}

func (a *API) updateProject(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bind(c, &req) {
		return
	}
	name, err := validateName(req.Name, 100)
	if err != nil {
		bad(c, err)
		return
	}
	p, err := a.store.UpdateProject(idParam(c, "pid"), name, req.Description)
	respond(c, p, err)
}

func (a *API) deleteProject(c *gin.Context) {
	err := a.store.DeleteProject(idParam(c, "pid"))
	respond(c, gin.H{"deleted": true}, err)
}

func (a *API) listDatasets(c *gin.Context) {
	items, err := a.store.ListDatasets(idParam(c, "pid"))
	respond(c, items, err)
}

func (a *API) createDataset(c *gin.Context) {
	var req struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		DefaultPrompt string `json:"default_prompt"`
		DefaultMode   int    `json:"default_mode"`
	}
	if !bind(c, &req) {
		return
	}
	name, err := validateName(req.Name, 100)
	if err != nil {
		bad(c, err)
		return
	}
	ds, err := a.store.CreateDataset(idParam(c, "pid"), name, req.Description, req.DefaultPrompt, req.DefaultMode)
	respond(c, ds, err)
}

func (a *API) getDataset(c *gin.Context) {
	ds, err := a.store.GetDataset(idParam(c, "did"))
	respond(c, ds, err)
}

func (a *API) listLabelClasses(c *gin.Context) {
	items, err := a.store.ListLabelClasses(idParam(c, "did"))
	respond(c, items, err)
}

func (a *API) saveLabelClasses(c *gin.Context) {
	var req struct {
		Classes []model.LabelClass `json:"classes"`
	}
	if !bind(c, &req) {
		return
	}
	if len(req.Classes) == 0 || len(req.Classes) > 1000 {
		bad(c, errors.New("classes count must be 1 to 1000"))
		return
	}
	seen := map[string]bool{}
	for _, cls := range req.Classes {
		name, err := validateName(cls.Name, 100)
		if err != nil {
			bad(c, err)
			return
		}
		key := strings.ToLower(name)
		if seen[key] {
			bad(c, fmt.Errorf("duplicate class: %s", name))
			return
		}
		seen[key] = true
	}
	items, err := a.store.SaveLabelClasses(idParam(c, "did"), req.Classes)
	respond(c, items, err)
}

func (a *API) listImages(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, err := a.store.ListImages(idParam(c, "did"), page, size)
	respond(c, items, err)
}

func (a *API) upload(c *gin.Context) {
	datasetID := idParam(c, "did")
	file, err := c.FormFile("file")
	if err != nil {
		bad(c, err)
		return
	}
	kind, err := validateUpload(file.Filename, file.Header.Get("Content-Type"), file.Size, a.cfg)
	if err != nil {
		bad(c, err)
		return
	}
	src, err := file.Open()
	if err != nil {
		respond(c, nil, err)
		return
	}
	defer src.Close()
	ext := strings.ToLower(filepath.Ext(file.Filename))
	name := uuid.NewString() + ext
	dstDir := filepath.Join(a.cfg.StorageDir, fmt.Sprintf("dataset-%d", datasetID))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		respond(c, nil, err)
		return
	}
	path := filepath.Join(dstDir, name)
	dst, err := os.Create(path)
	if err != nil {
		respond(c, nil, err)
		return
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		respond(c, nil, err)
		return
	}
	_ = dst.Close()
	w, h := imageSize(path)
	img, err := a.store.CreateImage(model.Image{
		DatasetID: datasetID,
		Kind:      kind,
		FilePath:  path,
		FileName:  filepath.Base(file.Filename),
		Width:     w,
		Height:    h,
		Status:    "ready",
	})
	respond(c, img, err)
}

func (a *API) imageFile(c *gin.Context) {
	img, err := a.store.GetImage(idParam(c, "iid"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.File(img.FilePath)
}

func (a *API) getDetections(c *gin.Context) {
	items, err := a.store.GetDetections(idParam(c, "iid"))
	respond(c, items, err)
}

func (a *API) saveDetections(c *gin.Context) {
	imageID := idParam(c, "iid")
	img, err := a.store.GetImage(imageID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	var req struct {
		Detections []model.Detection `json:"detections"`
	}
	if !bind(c, &req) {
		return
	}
	for i := range req.Detections {
		d := &req.Detections[i]
		if d.Box[0] < 0 || d.Box[1] < 0 || d.Box[2] > float64(img.Width) || d.Box[3] > float64(img.Height) || d.Box[0] >= d.Box[2] || d.Box[1] >= d.Box[3] {
			bad(c, errors.New("invalid detection box"))
			return
		}
		d.Origin = model.OriginManual
		d.Reviewed = true
	}
	err = a.store.ReplaceDetections(imageID, req.Detections)
	respond(c, gin.H{"saved": true}, err)
}

func (a *API) listJobs(c *gin.Context) {
	items, err := a.store.ListJobs(idParam(c, "did"))
	respond(c, items, err)
}

func (a *API) submitDetect(c *gin.Context) {
	var req struct {
		Prompt      string   `json:"prompt"`
		Mode        int      `json:"mode"`
		Threads     int      `json:"threads"`
		Scope       string   `json:"scope"`
		MaxBoxRatio *float64 `json:"max_box_ratio"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Threads <= 0 {
		req.Threads = a.cfg.Threads
	}
	scope := model.DetectScopeNew
	if req.Scope == model.DetectScopeAll {
		scope = model.DetectScopeAll
	}
	maxBoxRatio := 0.0
	if req.MaxBoxRatio != nil {
		maxBoxRatio = *req.MaxBoxRatio
		if maxBoxRatio < 0 || maxBoxRatio > 1 {
			bad(c, errors.New("max_box_ratio must be between 0 and 1"))
			return
		}
	}
	datasetID := idParam(c, "did")
	if !a.ensureNoActiveJob(c, datasetID, "已有拆帧或检测任务在进行中，请等待完成后再开始检测", model.JobExtract, model.JobDetect) {
		return
	}
	params, _ := json.Marshal(map[string]any{"scope": scope, "max_box_ratio": maxBoxRatio})
	job, err := a.store.CreateJob(datasetID, model.JobDetect, req.Prompt, req.Mode, req.Threads, string(params))
	if err != nil {
		respond(c, nil, err)
		return
	}
	go a.runDetect(job)
	c.JSON(http.StatusAccepted, job)
}

// cleanBoxes removes AI "blanket boxes" (larger than max_box_ratio of the
// image) from an already-detected dataset, leaving manual annotations intact.
func (a *API) cleanBoxes(c *gin.Context) {
	var req struct {
		MaxBoxRatio float64 `json:"max_box_ratio"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.MaxBoxRatio <= 0 || req.MaxBoxRatio > 1 {
		bad(c, errors.New("max_box_ratio must be between 0 and 1"))
		return
	}
	datasetID := idParam(c, "did")
	removed, err := a.store.DeleteOversizedAIDetections(datasetID, req.MaxBoxRatio)
	respond(c, gin.H{"removed": removed}, err)
}

// ensureNoActiveJob rejects the request with 409 when the dataset already has
// a pending/running job of one of the given types.
func (a *API) ensureNoActiveJob(c *gin.Context, datasetID int64, message string, types ...string) bool {
	active, err := a.store.HasActiveJob(datasetID, types...)
	if err != nil {
		respond(c, nil, err)
		return false
	}
	if active {
		c.JSON(http.StatusConflict, gin.H{"error": message})
		return false
	}
	return true
}

func (a *API) runDetect(job model.Job) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancels.Store(job.ID, cancel)
	defer a.cancels.Delete(job.ID)
	_ = a.store.SetJobStatus(job.ID, model.StatusRunning, "", "")
	job, _ = a.store.GetJob(job.ID)
	a.hub.Publish(job)
	err := service.NewDetectService(a.store, a.engine).Run(ctx, job, a.hub.Publish)
	updated, _ := a.store.GetJob(job.ID)
	if err != nil && updated.Status != model.StatusCanceled {
		_ = a.store.SetJobStatus(job.ID, model.StatusFailed, err.Error(), "")
		updated, _ = a.store.GetJob(job.ID)
	}
	a.hub.Publish(updated)
}

func (a *API) submitExport(c *gin.Context) {
	var req struct {
		Format     string   `json:"format"`
		TrainRatio *float64 `json:"train_ratio"`
		Sample     int      `json:"sample"`
	}
	_ = c.ShouldBindJSON(&req)
	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = "yolo"
	}
	if format != "yolo" && format != "coco" && format != "images" {
		bad(c, fmt.Errorf("format must be 'yolo', 'coco' or 'images'"))
		return
	}
	// train_ratio 0 disables the split; unset falls back to the 8:2 default.
	trainRatio := 0.8
	if req.TrainRatio != nil {
		trainRatio = *req.TrainRatio
		if trainRatio != 0 && (trainRatio < 0.5 || trainRatio > 0.95) {
			bad(c, errors.New("train_ratio must be 0 (no split) or between 0.5 and 0.95"))
			return
		}
	}
	if req.Sample < 0 || req.Sample > 10000 {
		bad(c, errors.New("sample must be between 0 and 10000"))
		return
	}
	datasetID := idParam(c, "did")
	if !a.ensureNoActiveJob(c, datasetID, "拆帧或检测任务还在进行中，请等待完成后再导出", model.JobExtract, model.JobDetect) {
		return
	}
	params, _ := json.Marshal(map[string]any{"format": format, "train_ratio": trainRatio, "sample": req.Sample})
	job, err := a.store.CreateJob(datasetID, model.JobExport, "", model.ModeHybrid, a.cfg.Threads, string(params))
	if err != nil {
		respond(c, nil, err)
		return
	}
	go a.runExport(job)
	c.JSON(http.StatusAccepted, job)
}

func (a *API) runExport(job model.Job) {
	_ = a.store.SetJobStatus(job.ID, model.StatusRunning, "", "")
	format := "yolo"
	trainRatio := 0.8
	sample := 0
	if job.ParamsJSON != "" {
		var p struct {
			Format     string   `json:"format"`
			TrainRatio *float64 `json:"train_ratio"`
			Sample     int      `json:"sample"`
		}
		_ = json.Unmarshal([]byte(job.ParamsJSON), &p)
		if p.Format != "" {
			format = p.Format
		}
		if p.TrainRatio != nil {
			trainRatio = *p.TrainRatio
		}
		sample = p.Sample
	}
	svc := service.NewExportService(a.store, a.cfg.ExportDir, a.cfg.PythonURL)
	ctx := context.Background()
	var (
		path string
		err  error
	)
	switch format {
	case "images":
		path, err = svc.ExportAnnotated(ctx, job, sample)
	case "coco":
		path, err = svc.ExportCOCO(ctx, job, trainRatio)
	default:
		path, err = svc.ExportYOLO(ctx, job, trainRatio)
	}
	if err != nil {
		_ = a.store.SetJobStatus(job.ID, model.StatusFailed, err.Error(), "")
	} else {
		_ = a.store.SetJobStatus(job.ID, model.StatusSucceeded, "", path)
	}
	updated, _ := a.store.GetJob(job.ID)
	a.hub.Publish(updated)
}

func (a *API) submitExtract(c *gin.Context) {
	var req struct {
		VideoImageID int64   `json:"video_image_id"`
		FPS          float64 `json:"fps"`
		MaxFrames    int     `json:"max_frames"`
	}
	if !bind(c, &req) {
		return
	}
	if req.VideoImageID <= 0 {
		bad(c, errors.New("video_image_id is required"))
		return
	}
	datasetID := idParam(c, "did")
	if !a.ensureNoActiveJob(c, datasetID, "已有拆帧或检测任务在进行中，请稍后再拆帧", model.JobExtract, model.JobDetect) {
		return
	}
	params, _ := json.Marshal(map[string]any{
		"video_image_id": req.VideoImageID,
		"fps":            req.FPS,
		"max_frames":     req.MaxFrames,
	})
	job, err := a.store.CreateJob(datasetID, model.JobExtract, "", model.ModeHybrid, a.cfg.Threads, string(params))
	if err != nil {
		respond(c, nil, err)
		return
	}
	go a.runExtract(job)
	c.JSON(http.StatusAccepted, job)
}

func (a *API) runExtract(job model.Job) {
	_ = a.store.SetJobStatus(job.ID, model.StatusRunning, "", "")
	err := service.NewExtractService(a.store, a.cfg.PythonURL, a.cfg.StorageDir).Run(context.Background(), job, a.hub.Publish)
	if err != nil {
		updated, _ := a.store.GetJob(job.ID)
		// Run() sets Canceled/Succeeded itself; only flip to Failed if still pending/running.
		if updated.Status != model.StatusCanceled && updated.Status != model.StatusSucceeded {
			_ = a.store.SetJobStatus(job.ID, model.StatusFailed, err.Error(), "")
		}
	}
	updated, _ := a.store.GetJob(job.ID)
	a.hub.Publish(updated)
}

func (a *API) getJob(c *gin.Context) {
	job, err := a.store.GetJob(idParam(c, "jid"))
	respond(c, job, err)
}

func (a *API) jobResult(c *gin.Context) {
	job, err := a.store.GetJob(idParam(c, "jid"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	if job.Type != model.JobExport || job.Status != model.StatusSucceeded || job.ResultPath == "" {
		bad(c, errors.New("job result is not downloadable"))
		return
	}
	// Gin emits RFC 5987 (filename*=UTF-8'') for non-ASCII names, so Chinese
	// zip names download correctly; generated names contain no spaces.
	c.FileAttachment(job.ResultPath, filepath.Base(job.ResultPath))
}

// cancelJob stops a running job. For detect jobs this acts as "pause": the
// frames already processed keep their detections, and a later detect with
// scope=new picks up exactly where this one stopped.
func (a *API) cancelJob(c *gin.Context) {
	job, err := a.store.GetJob(idParam(c, "jid"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	if job.Status != model.StatusPending && job.Status != model.StatusRunning {
		bad(c, errors.New("任务已结束，无需暂停"))
		return
	}
	v, ok := a.cancels.Load(job.ID)
	if !ok {
		bad(c, errors.New("任务不支持暂停"))
		return
	}
	v.(context.CancelFunc)()
	c.JSON(http.StatusOK, gin.H{"canceled": true})
}

// deleteJob removes a finished export job together with its zip so users can
// prune the download history.
func (a *API) deleteJob(c *gin.Context) {
	job, err := a.store.GetJob(idParam(c, "jid"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	if job.Type != model.JobExport {
		bad(c, errors.New("只支持删除导出任务"))
		return
	}
	if job.Status == model.StatusPending || job.Status == model.StatusRunning {
		bad(c, errors.New("任务还在进行中，无法删除"))
		return
	}
	if job.ResultPath != "" {
		if err := os.Remove(job.ResultPath); err != nil && !os.IsNotExist(err) {
			respond(c, nil, err)
			return
		}
	}
	respond(c, gin.H{"deleted": true}, a.store.DeleteJob(job.ID))
}

func (a *API) optimizePrompt(c *gin.Context) {
	var req struct {
		Text string `json:"text"`
	}
	if !bind(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	prompt, err := a.prompts.Optimize(ctx, req.Text)
	if err != nil {
		bad(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"prompt": prompt})
}

func bind(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		bad(c, err)
		return false
	}
	return true
}

func respond(c *gin.Context, v any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, v)
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func bad(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func idParam(c *gin.Context, key string) int64 {
	v, _ := strconv.ParseInt(c.Param(key), 10, 64)
	return v
}

func validateName(v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(v)) > max {
		return "", fmt.Errorf("name exceeds %d characters", max)
	}
	return v, nil
}

func validateUpload(name, mime string, size int64, cfg config.Config) (string, error) {
	clean := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if clean == "" || clean == "." || clean == ".." || len(clean) > 255 {
		return "", errors.New("invalid filename")
	}
	ext := strings.ToLower(filepath.Ext(clean))
	imageExt := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".bmp": true, ".webp": true}
	videoExt := map[string]bool{".mp4": true, ".avi": true, ".mov": true, ".mkv": true}
	if imageExt[ext] {
		if size > cfg.ImageLimit {
			return "", errors.New("image exceeds size limit")
		}
		if !strings.HasPrefix(mime, "image/") && mime != "application/octet-stream" {
			return "", errors.New("unsupported image mime type")
		}
		return model.ImageKindImage, nil
	}
	if videoExt[ext] {
		if size > cfg.VideoLimit {
			return "", errors.New("video exceeds size limit")
		}
		if !strings.HasPrefix(mime, "video/") && mime != "application/octet-stream" {
			return "", errors.New("unsupported video mime type")
		}
		return model.ImageKindVideo, nil
	}
	return "", errors.New("unsupported file type")
}

func imageSize(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
