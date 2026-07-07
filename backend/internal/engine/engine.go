package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"ai-auto-annotator/backend/internal/model"
)

type InferenceEngine interface {
	Locate(ctx context.Context, imagePath, prompt string, mode int) ([]model.LocateDetection, error)
	Status() model.EngineStatus
	Close() error
}

// NewConfigured picks an inference engine from config. "python" routes to the
// LocateAnything-3B HTTP sidecar (cross-platform); "real"/"cabi"/"c-api" use the
// Windows-only C ABI DLL; "stub" returns deterministic fake detections. In
// "auto" mode, non-Windows hosts prefer the Python sidecar when LA_PYTHON_URL is
// set (since the C ABI engine is Windows-only), then fall back to the C ABI,
// then to the stub.
func NewConfigured(mode, libPath, modelPath string, threads int, pythonURL string) InferenceEngine {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "stub":
		return NewStub(modelPath, threads)
	case "python":
		if strings.TrimSpace(pythonURL) == "" {
			return NewDisabled(modelPath, threads, errPythonURLRequired)
		}
		return NewPythonEngine(pythonURL, modelPath)
	case "real", "cabi", "c-api":
		eng, err := newReal(libPath, modelPath, threads)
		if err != nil {
			return NewDisabled(modelPath, threads, err)
		}
		return eng
	default: // auto
		if runtime.GOOS != "windows" && strings.TrimSpace(pythonURL) != "" {
			return NewPythonEngine(pythonURL, modelPath)
		}
		if libPath != "" && modelPath != "" {
			eng, err := newReal(libPath, modelPath, threads)
			if err == nil {
				return eng
			}
			return NewDisabled(modelPath, threads, fmt.Errorf("real engine auto-load failed: %w", err))
		}
		return NewStub(modelPath, threads)
	}
}

type Stub struct {
	mu        sync.Mutex
	modelPath string
	threads   int
}

func NewStub(modelPath string, threads int) *Stub {
	return &Stub{modelPath: modelPath, threads: threads}
}

func (s *Stub) Locate(ctx context.Context, imagePath, prompt string, mode int) ([]model.LocateDetection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	label := "object"
	parts := strings.Split(prompt, "</c>")
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimSuffix(p, "."))
		p = strings.TrimPrefix(p, "Locate all the instances that matches the following description:")
		if p != "" {
			label = strings.TrimSpace(p)
			break
		}
	}
	return []model.LocateDetection{
		{Label: label, Box: [4]float64{80, 60, 320, 260}, Score: 0.91},
	}, nil
}

func (s *Stub) Status() model.EngineStatus {
	return model.EngineStatus{
		Loaded:     true,
		ModelPath:  s.modelPath,
		Threads:    s.threads,
		ABIVersion: 0,
	}
}

func (s *Stub) Close() error { return nil }

type Disabled struct {
	modelPath string
	threads   int
	err       error
}

func NewDisabled(modelPath string, threads int, err error) *Disabled {
	if err == nil {
		err = errors.New("inference engine is not configured")
	}
	return &Disabled{modelPath: modelPath, threads: threads, err: err}
}

func (d *Disabled) Locate(ctx context.Context, imagePath, prompt string, mode int) ([]model.LocateDetection, error) {
	return nil, d.err
}

func (d *Disabled) Status() model.EngineStatus {
	return model.EngineStatus{
		Loaded:    false,
		ModelPath: d.modelPath,
		Threads:   d.threads,
		Error:     d.err.Error(),
	}
}

func (d *Disabled) Close() error { return nil }
