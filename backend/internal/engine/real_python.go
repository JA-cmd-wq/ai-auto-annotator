package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"ai-auto-annotator/backend/internal/model"
)

// PythonEngine talks to the LocateAnything-3B HTTP sidecar (see
// backend/engine_python/server.py). The model stays resident in the Python
// process on the GPU; this engine just forwards Locate requests over HTTP.
// Unlike the C ABI engine, this works on any platform because it only speaks
// HTTP to a local Python process.
type PythonEngine struct {
	mu        sync.Mutex
	baseURL   string
	modelPath string

	statusClient *http.Client // short timeout for /status probes
	locateClient *http.Client // long timeout for inference calls
}

func NewPythonEngine(baseURL, modelPath string) *PythonEngine {
	return &PythonEngine{
		baseURL:      normalizeBaseURL(baseURL),
		modelPath:    modelPath,
		statusClient: &http.Client{Timeout: 3 * time.Second},
		locateClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

func normalizeBaseURL(u string) string {
	for len(u) > 0 && u[len(u)-1] == '/' {
		u = u[:len(u)-1]
	}
	return u
}

type locateRequest struct {
	Image  string `json:"image"`
	Prompt string `json:"prompt"`
	Mode   int    `json:"mode"`
}

func (e *PythonEngine) Locate(ctx context.Context, imagePath, prompt string, mode int) ([]model.LocateDetection, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	absPath, err := filepath.Abs(imagePath)
	if err != nil {
		absPath = imagePath
	}

	body, err := json.Marshal(locateRequest{Image: absPath, Prompt: prompt, Mode: mode})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/locate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.locateClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("python engine locate: %w", err)
	}
	defer resp.Body.Close()

	var out model.LocateResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("python engine decode: %w", err)
	}
	if resp.StatusCode >= 300 {
		if out.Detections == nil {
			return nil, fmt.Errorf("python engine returned HTTP %d", resp.StatusCode)
		}
	}
	return out.Detections, nil
}

type pythonStatusResponse struct {
	Loaded    bool   `json:"loaded"`
	ModelPath string `json:"model_path"`
	Device    string `json:"device"`
	Engine    string `json:"engine"`
	Error     string `json:"error"`
}

func (e *PythonEngine) Status() model.EngineStatus {
	st := model.EngineStatus{
		Loaded:    false,
		ModelPath: e.modelPath,
	}

	req, err := http.NewRequest(http.MethodGet, e.baseURL+"/status", nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	resp, err := e.statusClient.Do(req)
	if err != nil {
		st.Error = fmt.Sprintf("python sidecar not reachable at %s: %v", e.baseURL, err)
		return st
	}
	defer resp.Body.Close()

	var ps pythonStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		st.Error = fmt.Sprintf("python sidecar status decode: %v", err)
		return st
	}
	st.Loaded = ps.Loaded
	if ps.ModelPath != "" {
		st.ModelPath = ps.ModelPath
	}
	if ps.Error != "" {
		st.Error = ps.Error
	}
	if !ps.Loaded && st.Error == "" {
		st.Error = "python sidecar reports model not loaded"
	}
	return st
}

func (e *PythonEngine) Close() error { return nil }

var _ InferenceEngine = (*PythonEngine)(nil)

var errPythonURLRequired = errors.New("LA_PYTHON_URL is not configured")
