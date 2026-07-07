package service

import (
	"errors"
	"math"
)

type YOLOBox struct {
	ClassIndex int     `json:"class_index"`
	CX         float64 `json:"cx"`
	CY         float64 `json:"cy"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
}

func ToYOLO(box [4]float64, imgW, imgH int) (YOLOBox, bool, error) {
	if imgW <= 0 || imgH <= 0 {
		return YOLOBox{}, false, errors.New("image width and height must be positive")
	}
	x0 := math.Max(0, math.Min(box[0], box[2]))
	x1 := math.Min(float64(imgW), math.Max(box[0], box[2]))
	y0 := math.Max(0, math.Min(box[1], box[3]))
	y1 := math.Min(float64(imgH), math.Max(box[1], box[3]))
	if x1 <= x0 || y1 <= y0 {
		return YOLOBox{}, false, nil
	}
	return YOLOBox{
		CX: clamp01((x0 + x1) / 2 / float64(imgW)),
		CY: clamp01((y0 + y1) / 2 / float64(imgH)),
		W:  clamp01((x1 - x0) / float64(imgW)),
		H:  clamp01((y1 - y0) / float64(imgH)),
	}, true, nil
}

func ClipBox(box [4]float64, imgW, imgH int) ([4]float64, bool) {
	x0 := math.Max(0, math.Min(box[0], box[2]))
	x1 := math.Min(float64(imgW), math.Max(box[0], box[2]))
	y0 := math.Max(0, math.Min(box[1], box[3]))
	y1 := math.Min(float64(imgH), math.Max(box[1], box[3]))
	return [4]float64{x0, y0, x1, y1}, x1 > x0 && y1 > y0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// IsOversizedBox reports whether a box covers more than maxRatio of the image
// area. On aerial/dense scenes the model sometimes emits a single "blanket"
// box over a whole cluster of tiny objects instead of individual ones; those
// blanket boxes are far larger than any real target and are dropped. maxRatio
// <= 0 disables the check.
func IsOversizedBox(box [4]float64, imgW, imgH int, maxRatio float64) bool {
	if maxRatio <= 0 || imgW <= 0 || imgH <= 0 {
		return false
	}
	w := math.Abs(box[2] - box[0])
	h := math.Abs(box[3] - box[1])
	return (w*h)/(float64(imgW)*float64(imgH)) > maxRatio
}
