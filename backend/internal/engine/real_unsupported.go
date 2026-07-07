//go:build !windows

package engine

import (
	"errors"
)

func newReal(libPath, modelPath string, threads int) (InferenceEngine, error) {
	return nil, errors.New("real LocateAnything C ABI engine is currently implemented for Windows builds")
}
