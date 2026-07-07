//go:build windows

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"ai-auto-annotator/backend/internal/model"
)

type realEngine struct {
	mu sync.Mutex

	dll        *syscall.LazyDLL
	load       *syscall.LazyProc
	free       *syscall.LazyProc
	locatePath *syscall.LazyProc
	freeString *syscall.LazyProc
	lastError  *syscall.LazyProc
	abiVersion *syscall.LazyProc

	ctx       uintptr
	libPath   string
	modelPath string
	threads   int
	abi       int
}

func newReal(libPath, modelPath string, threads int) (InferenceEngine, error) {
	if libPath == "" {
		return nil, errors.New("LA_LIB is not configured and liblocate_anything was not found")
	}
	if modelPath == "" {
		return nil, errors.New("LA_MODEL is not configured and gguf model was not found")
	}
	libAbs, err := filepath.Abs(libPath)
	if err != nil {
		return nil, err
	}
	modelAbs, err := filepath.Abs(modelPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(libAbs); err != nil {
		return nil, fmt.Errorf("locate-anything library not found: %w", err)
	}
	if _, err := os.Stat(modelAbs); err != nil {
		return nil, fmt.Errorf("locate-anything model not found: %w", err)
	}

	libDir := filepath.Dir(libAbs)
	_ = os.Setenv("PATH", libDir+";"+os.Getenv("PATH"))

	e := &realEngine{
		dll:       syscall.NewLazyDLL(libAbs),
		libPath:   libAbs,
		modelPath: modelAbs,
		threads:   threads,
	}
	e.load = e.dll.NewProc("la_capi_load")
	e.free = e.dll.NewProc("la_capi_free")
	e.locatePath = e.dll.NewProc("la_capi_locate_path")
	e.freeString = e.dll.NewProc("la_capi_free_string")
	e.lastError = e.dll.NewProc("la_capi_last_error")
	e.abiVersion = e.dll.NewProc("la_capi_abi_version")

	if err := e.dll.Load(); err != nil {
		return nil, err
	}
	abi, _, _ := e.abiVersion.Call()
	e.abi = int(abi)

	cModel := newCString(modelAbs)
	ctx, _, callErr := e.load.Call(cModel.ptr(), uintptr(threads))
	cModel.keepAlive()
	if ctx == 0 {
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("la_capi_load returned NULL")
	}
	e.ctx = ctx
	return e, nil
}

func (e *realEngine) Locate(ctx context.Context, imagePath, prompt string, mode int) ([]model.LocateDetection, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ctx == 0 {
		return nil, errors.New("inference engine is closed")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	cImage := newCString(imagePath)
	cPrompt := newCString(prompt)
	ptr, _, callErr := e.locatePath.Call(e.ctx, cImage.ptr(), cPrompt.ptr(), uintptr(mode))
	cImage.keepAlive()
	cPrompt.keepAlive()
	if ptr == 0 {
		if msg := e.lastErrorString(); msg != "" {
			return nil, errors.New(msg)
		}
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("la_capi_locate_path returned NULL")
	}
	defer e.freeString.Call(ptr)

	var out model.LocateResult
	if err := json.Unmarshal([]byte(goString(ptr)), &out); err != nil {
		return nil, err
	}
	return out.Detections, nil
}

func (e *realEngine) Status() model.EngineStatus {
	return model.EngineStatus{
		Loaded:     e.ctx != 0,
		ModelPath:  e.modelPath,
		Threads:    e.threads,
		ABIVersion: e.abi,
	}
}

func (e *realEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx != 0 {
		e.free.Call(e.ctx)
		e.ctx = 0
	}
	return nil
}

func (e *realEngine) lastErrorString() string {
	if e.ctx == 0 {
		return ""
	}
	ptr, _, _ := e.lastError.Call(e.ctx)
	if ptr == 0 {
		return ""
	}
	return goString(ptr)
}

type cString struct {
	bytes []byte
}

func newCString(s string) cString {
	b := append([]byte(s), 0)
	return cString{bytes: b}
}

func (s cString) ptr() uintptr {
	if len(s.bytes) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&s.bytes[0]))
}

func (s cString) keepAlive() {
	runtime.KeepAlive(s.bytes)
}

func goString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	var b []byte
	for p := ptr; ; p++ {
		c := *(*byte)(unsafe.Pointer(p))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}
