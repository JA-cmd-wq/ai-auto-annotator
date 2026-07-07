package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-auto-annotator/backend/internal/model"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// _pragma in the DSN applies to every pooled connection (a plain
	// `PRAGMA foreign_keys=ON` exec would only affect one connection).
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS project (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dataset (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  default_prompt TEXT NOT NULL DEFAULT '',
  default_mode INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS label_class (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  dataset_id INTEGER NOT NULL REFERENCES dataset(id) ON DELETE CASCADE,
  class_index INTEGER NOT NULL,
  name TEXT NOT NULL,
  prompt_fragment TEXT NOT NULL,
  color TEXT NOT NULL,
  match_keywords TEXT NOT NULL,
  UNIQUE(dataset_id, class_index),
  UNIQUE(dataset_id, name)
);
CREATE TABLE IF NOT EXISTS image (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  dataset_id INTEGER NOT NULL REFERENCES dataset(id) ON DELETE CASCADE,
  job_id INTEGER REFERENCES job(id) ON DELETE SET NULL,
  kind TEXT NOT NULL,
  source_video TEXT NOT NULL DEFAULT '',
  frame_index INTEGER,
  file_path TEXT NOT NULL,
  file_name TEXT NOT NULL,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'ready',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS detection (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  image_id INTEGER NOT NULL REFERENCES image(id) ON DELETE CASCADE,
  label_class_id INTEGER REFERENCES label_class(id) ON DELETE RESTRICT,
  raw_label TEXT NOT NULL,
  x0 REAL NOT NULL,
  y0 REAL NOT NULL,
  x1 REAL NOT NULL,
  y1 REAL NOT NULL,
  score REAL NOT NULL DEFAULT 0,
  origin TEXT NOT NULL,
  reviewed INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS job (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  dataset_id INTEGER NOT NULL REFERENCES dataset(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  status TEXT NOT NULL,
  prompt TEXT NOT NULL DEFAULT '',
  mode INTEGER NOT NULL DEFAULT 0,
  threads INTEGER NOT NULL DEFAULT 0,
  total INTEGER NOT NULL DEFAULT 0,
  processed INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  params_json TEXT NOT NULL DEFAULT '',
  result_path TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`)
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func (s *Store) CreateProject(name, description string) (model.Project, error) {
	ts := now()
	res, err := s.db.Exec(`INSERT INTO project(name, description, created_at, updated_at) VALUES(?,?,?,?)`, name, description, ts, ts)
	if err != nil {
		return model.Project{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetProject(id)
}

func (s *Store) ListProjects() ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT id,name,description,created_at,updated_at FROM project ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		var p model.Project
		var ca, ua string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &ca, &ua); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = parseTime(ca), parseTime(ua)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(id int64) (model.Project, error) {
	var p model.Project
	var ca, ua string
	err := s.db.QueryRow(`SELECT id,name,description,created_at,updated_at FROM project WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Description, &ca, &ua)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.CreatedAt, p.UpdatedAt = parseTime(ca), parseTime(ua)
	return p, nil
}

func (s *Store) UpdateProject(id int64, name, description string) (model.Project, error) {
	res, err := s.db.Exec(`UPDATE project SET name=?, description=?, updated_at=? WHERE id=?`, name, description, now(), id)
	if err != nil {
		return model.Project{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Project{}, ErrNotFound
	}
	return s.GetProject(id)
}

func (s *Store) DeleteProject(id int64) error {
	res, err := s.db.Exec(`DELETE FROM project WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateDataset(projectID int64, name, description, defaultPrompt string, mode int) (model.Dataset, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return model.Dataset{}, err
	}
	ts := now()
	res, err := s.db.Exec(`INSERT INTO dataset(project_id,name,description,default_prompt,default_mode,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		projectID, name, description, defaultPrompt, mode, ts, ts)
	if err != nil {
		return model.Dataset{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetDataset(id)
}

func (s *Store) ListDatasets(projectID int64) ([]model.Dataset, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,project_id,name,description,default_prompt,default_mode,created_at,updated_at FROM dataset WHERE project_id=? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Dataset{}
	for rows.Next() {
		d, err := scanDataset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDataset(id int64) (model.Dataset, error) {
	row := s.db.QueryRow(`SELECT id,project_id,name,description,default_prompt,default_mode,created_at,updated_at FROM dataset WHERE id=?`, id)
	d, err := scanDataset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDataset(row scanner) (model.Dataset, error) {
	var d model.Dataset
	var ca, ua string
	if err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.DefaultPrompt, &d.DefaultMode, &ca, &ua); err != nil {
		return d, err
	}
	d.CreatedAt, d.UpdatedAt = parseTime(ca), parseTime(ua)
	return d, nil
}

// SaveLabelClasses upserts classes by (case-insensitive) name instead of
// delete-and-recreate: existing detections reference label_class rows with ON
// DELETE RESTRICT, so wholesale deletion fails with a FOREIGN KEY error once a
// dataset has been detected. Classes that disappear from the new list are
// removed together with their detections.
func (s *Store) SaveLabelClasses(datasetID int64, classes []model.LabelClass) ([]model.LabelClass, error) {
	existing, err := s.ListLabelClasses(datasetID)
	if err != nil {
		return nil, err
	}
	byName := map[string]model.LabelClass{}
	for _, c := range existing {
		byName[strings.ToLower(c.Name)] = c
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Park existing rows on temporary indices so re-ordering can't trip the
	// UNIQUE(dataset_id, class_index) constraint mid-transaction.
	if _, err := tx.Exec(`UPDATE label_class SET class_index = class_index - 1000000 WHERE dataset_id=?`, datasetID); err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for i, cls := range classes {
		name := strings.TrimSpace(cls.Name)
		key := strings.ToLower(name)
		keywords, _ := json.Marshal(cls.MatchKeywords)
		color := cls.Color
		if color == "" {
			color = defaultColor(i)
		}
		if old, ok := byName[key]; ok {
			kept[key] = true
			if _, err := tx.Exec(`UPDATE label_class SET class_index=?, name=?, prompt_fragment=?, color=?, match_keywords=? WHERE id=?`,
				i, name, strings.TrimSpace(cls.PromptFragment), color, string(keywords), old.ID); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.Exec(`INSERT INTO label_class(dataset_id,class_index,name,prompt_fragment,color,match_keywords) VALUES(?,?,?,?,?,?)`,
			datasetID, i, name, strings.TrimSpace(cls.PromptFragment), color, string(keywords)); err != nil {
			return nil, err
		}
	}
	for key, old := range byName {
		if kept[key] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM detection WHERE label_class_id=?`, old.ID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM label_class WHERE id=?`, old.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListLabelClasses(datasetID)
}

func (s *Store) ListLabelClasses(datasetID int64) ([]model.LabelClass, error) {
	if _, err := s.GetDataset(datasetID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,dataset_id,class_index,name,prompt_fragment,color,match_keywords FROM label_class WHERE dataset_id=? ORDER BY class_index ASC`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.LabelClass{}
	for rows.Next() {
		var c model.LabelClass
		var raw string
		if err := rows.Scan(&c.ID, &c.DatasetID, &c.ClassIndex, &c.Name, &c.PromptFragment, &c.Color, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &c.MatchKeywords)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateImage(img model.Image) (model.Image, error) {
	if _, err := s.GetDataset(img.DatasetID); err != nil {
		return model.Image{}, err
	}
	ts := now()
	res, err := s.db.Exec(`INSERT INTO image(dataset_id,job_id,kind,source_video,frame_index,file_path,file_name,width,height,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		img.DatasetID, img.JobID, img.Kind, img.SourceVideo, img.FrameIndex, img.FilePath, img.FileName, img.Width, img.Height, coalesce(img.Status, "ready"), ts)
	if err != nil {
		return model.Image{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetImage(id)
}

func (s *Store) ListImages(datasetID int64, page, pageSize int) ([]model.Image, error) {
	if _, err := s.GetDataset(datasetID); err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 2000 {
		pageSize = 2000
	}
	rows, err := s.db.Query(`SELECT id,dataset_id,job_id,kind,source_video,frame_index,file_path,file_name,width,height,status,created_at FROM image WHERE dataset_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		datasetID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Image{}
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

func (s *Store) ListDetectableImages(datasetID int64) ([]model.Image, error) {
	rows, err := s.db.Query(`SELECT id,dataset_id,job_id,kind,source_video,frame_index,file_path,file_name,width,height,status,created_at FROM image WHERE dataset_id=? AND kind IN ('image','frame') ORDER BY id ASC`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Image{}
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

// ListImagesForDetect returns the detectable images an AI detect run should
// process. Images carrying manual/reviewed annotations are always excluded so
// a re-run can never wipe human corrections. Scope "new" additionally narrows
// to images that have never been successfully detected (no detection rows and
// not marked 'detected'), enabling cheap incremental runs after new uploads.
func (s *Store) ListImagesForDetect(datasetID int64, scope string) ([]model.Image, error) {
	query := `SELECT id,dataset_id,job_id,kind,source_video,frame_index,file_path,file_name,width,height,status,created_at
FROM image
WHERE dataset_id=? AND kind IN ('image','frame')
  AND NOT EXISTS (SELECT 1 FROM detection d WHERE d.image_id=image.id AND (d.origin=? OR d.reviewed=1))`
	args := []any{datasetID, model.OriginManual}
	if scope == model.DetectScopeNew {
		query += `
  AND status<>?
  AND NOT EXISTS (SELECT 1 FROM detection d2 WHERE d2.image_id=image.id)`
		args = append(args, model.ImageStatusDetected)
	}
	query += ` ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Image{}
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

func (s *Store) GetImage(id int64) (model.Image, error) {
	row := s.db.QueryRow(`SELECT id,dataset_id,job_id,kind,source_video,frame_index,file_path,file_name,width,height,status,created_at FROM image WHERE id=?`, id)
	img, err := scanImage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return img, ErrNotFound
	}
	return img, err
}

func scanImage(row scanner) (model.Image, error) {
	var img model.Image
	var jobID sql.NullInt64
	var frame sql.NullInt64
	var ca string
	if err := row.Scan(&img.ID, &img.DatasetID, &jobID, &img.Kind, &img.SourceVideo, &frame, &img.FilePath, &img.FileName, &img.Width, &img.Height, &img.Status, &ca); err != nil {
		return img, err
	}
	if jobID.Valid {
		v := jobID.Int64
		img.JobID = &v
	}
	if frame.Valid {
		v := int(frame.Int64)
		img.FrameIndex = &v
	}
	img.CreatedAt = parseTime(ca)
	return img, nil
}

func (s *Store) SetImageStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE image SET status=? WHERE id=?`, status, id)
	return err
}

// DeleteFramesBySourceVideo removes all frame images extracted from the given
// video (detections cascade via FK) and returns their file paths so the caller
// can clean up files on disk.
func (s *Store) DeleteFramesBySourceVideo(datasetID int64, sourceVideo string) ([]string, error) {
	rows, err := s.db.Query(`SELECT file_path FROM image WHERE dataset_id=? AND kind=? AND source_video=?`,
		datasetID, model.ImageKindFrame, sourceVideo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`DELETE FROM image WHERE dataset_id=? AND kind=? AND source_video=?`,
		datasetID, model.ImageKindFrame, sourceVideo); err != nil {
		return nil, err
	}
	return paths, nil
}

// HasActiveJob reports whether the dataset has a pending/running job of any of
// the given types (all types when none are given).
func (s *Store) HasActiveJob(datasetID int64, types ...string) (bool, error) {
	query := `SELECT COUNT(*) FROM job WHERE dataset_id=? AND status IN (?,?)`
	args := []any{datasetID, model.StatusPending, model.StatusRunning}
	if len(types) > 0 {
		query += ` AND type IN (?` + strings.Repeat(",?", len(types)-1) + `)`
		for _, t := range types {
			args = append(args, t)
		}
	}
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) ReplaceDetections(imageID int64, dets []model.Detection) error {
	if _, err := s.GetImage(imageID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM detection WHERE image_id=?`, imageID); err != nil {
		return err
	}
	ts := now()
	for _, d := range dets {
		reviewed := 0
		if d.Reviewed {
			reviewed = 1
		}
		_, err := tx.Exec(`INSERT INTO detection(image_id,label_class_id,raw_label,x0,y0,x1,y1,score,origin,reviewed,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			imageID, d.LabelClassID, d.RawLabel, d.Box[0], d.Box[1], d.Box[2], d.Box[3], d.Score, d.Origin, reviewed, ts)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteOversizedAIDetections removes AI-origin detections whose box area
// exceeds maxRatio of the image, across a whole dataset. Manual/reviewed boxes
// are never touched. Returns the number of rows removed. Used to clean up the
// "blanket boxes" the model emits over dense clusters in aerial scenes.
func (s *Store) DeleteOversizedAIDetections(datasetID int64, maxRatio float64) (int64, error) {
	if maxRatio <= 0 {
		return 0, nil
	}
	res, err := s.db.Exec(`
DELETE FROM detection
WHERE origin=? AND reviewed=0 AND id IN (
  SELECT d.id FROM detection d JOIN image i ON i.id=d.image_id
  WHERE i.dataset_id=? AND i.width>0 AND i.height>0
    AND (ABS(d.x1-d.x0)*ABS(d.y1-d.y0)) > ? * i.width * i.height
)`, model.OriginAI, datasetID, maxRatio)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) GetDetections(imageID int64) ([]model.Detection, error) {
	if _, err := s.GetImage(imageID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,image_id,label_class_id,raw_label,x0,y0,x1,y1,score,origin,reviewed,created_at FROM detection WHERE image_id=? ORDER BY id ASC`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Detection{}
	for rows.Next() {
		var d model.Detection
		var labelID sql.NullInt64
		var reviewed int
		var ca string
		if err := rows.Scan(&d.ID, &d.ImageID, &labelID, &d.RawLabel, &d.Box[0], &d.Box[1], &d.Box[2], &d.Box[3], &d.Score, &d.Origin, &reviewed, &ca); err != nil {
			return nil, err
		}
		if labelID.Valid {
			v := labelID.Int64
			d.LabelClassID = &v
		}
		d.Reviewed = reviewed == 1
		d.CreatedAt = parseTime(ca)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateJob(datasetID int64, typ, prompt string, mode, threads int, params string) (model.Job, error) {
	if _, err := s.GetDataset(datasetID); err != nil {
		return model.Job{}, err
	}
	ts := now()
	res, err := s.db.Exec(`INSERT INTO job(dataset_id,type,status,prompt,mode,threads,params_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		datasetID, typ, model.StatusPending, prompt, mode, threads, params, ts, ts)
	if err != nil {
		return model.Job{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetJob(id)
}

func (s *Store) GetJob(id int64) (model.Job, error) {
	var j model.Job
	var ca, ua string
	err := s.db.QueryRow(`SELECT id,dataset_id,type,status,prompt,mode,threads,total,processed,error,params_json,result_path,created_at,updated_at FROM job WHERE id=?`, id).
		Scan(&j.ID, &j.DatasetID, &j.Type, &j.Status, &j.Prompt, &j.Mode, &j.Threads, &j.Total, &j.Processed, &j.Error, &j.ParamsJSON, &j.ResultPath, &ca, &ua)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	j.CreatedAt, j.UpdatedAt = parseTime(ca), parseTime(ua)
	return j, nil
}

func (s *Store) ListJobs(datasetID int64) ([]model.Job, error) {
	rows, err := s.db.Query(`SELECT id,dataset_id,type,status,prompt,mode,threads,total,processed,error,params_json,result_path,created_at,updated_at FROM job WHERE dataset_id=? ORDER BY created_at DESC`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		var j model.Job
		var ca, ua string
		if err := rows.Scan(&j.ID, &j.DatasetID, &j.Type, &j.Status, &j.Prompt, &j.Mode, &j.Threads, &j.Total, &j.Processed, &j.Error, &j.ParamsJSON, &j.ResultPath, &ca, &ua); err != nil {
			return nil, err
		}
		j.CreatedAt, j.UpdatedAt = parseTime(ca), parseTime(ua)
		out = append(out, j)
	}
	return out, rows.Err()
}

// DeleteJob removes a job row (the caller is responsible for cleaning up any
// files referenced by result_path).
func (s *Store) DeleteJob(id int64) error {
	res, err := s.db.Exec(`DELETE FROM job WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FailInterruptedJobs marks jobs left pending/running by a previous process as
// failed so they don't block the active-job guards forever after a restart.
func (s *Store) FailInterruptedJobs() error {
	_, err := s.db.Exec(`UPDATE job SET status=?, error=?, updated_at=? WHERE status IN (?,?)`,
		model.StatusFailed, "服务重启，任务已中断，请重新发起", now(), model.StatusPending, model.StatusRunning)
	return err
}

func (s *Store) UpdateJobProgress(id int64, processed, total int) error {
	_, err := s.db.Exec(`UPDATE job SET processed=?, total=?, updated_at=? WHERE id=?`, processed, total, now(), id)
	return err
}

func (s *Store) SetJobStatus(id int64, status, message, resultPath string) error {
	_, err := s.db.Exec(`UPDATE job SET status=?, error=?, result_path=?, updated_at=? WHERE id=?`, status, message, resultPath, now(), id)
	return err
}

func defaultColor(i int) string {
	colors := []string{"#0066cc", "#34c759", "#ff9500", "#af52de", "#ff2d55", "#5ac8fa"}
	return colors[i%len(colors)]
}

func coalesce(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
