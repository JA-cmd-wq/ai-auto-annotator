package service

import (
	"fmt"
	"hash/fnv"
	"sort"

	"ai-auto-annotator/backend/internal/model"
)

// splitChunkFrames groups this many consecutive frames of one video into a
// single train/val unit. Neighboring frames are near-duplicates; splitting
// them individually would leak training data into the validation set. 10 keeps
// chunks time-coherent while giving short (or sparsely sampled) videos enough
// units for the ratio to hold — at 50, a 132-frame video collapses into 3
// units and an 8:2 split degenerates to 131:1.
const splitChunkFrames = 10

const (
	SplitTrain = "train"
	SplitVal   = "val"
)

// AssignSplits deterministically assigns each image to train or val.
//
// Assignments must stay stable across exports: users upload material in
// batches, and an image that once sat in val must never drift into train (it
// would silently inflate later validation metrics). To achieve this without
// storing state, each image belongs to a sequence whose order can only grow
// at the end (standalone images ordered by ID, video frames ordered by chunk
// index), and val units are spread evenly through each sequence with a
// per-sequence phase derived from a hash. Appending new units never changes
// the assignment of earlier ones.
func AssignSplits(images []model.Image, trainRatio float64) []string {
	out := make([]string, len(images))
	for i := range out {
		out[i] = SplitTrain
	}
	if trainRatio <= 0 || trainRatio >= 1 || len(images) == 0 {
		return out
	}
	valShare := 1 - trainRatio

	var standalone []int
	chunksByVideo := map[string]map[int][]int{}
	for i, img := range images {
		if img.Kind == model.ImageKindFrame && img.SourceVideo != "" && img.FrameIndex != nil {
			key := fmt.Sprintf("v:%d:%s", img.DatasetID, img.SourceVideo)
			if chunksByVideo[key] == nil {
				chunksByVideo[key] = map[int][]int{}
			}
			chunk := *img.FrameIndex / splitChunkFrames
			chunksByVideo[key][chunk] = append(chunksByVideo[key][chunk], i)
		} else {
			standalone = append(standalone, i)
		}
	}

	if len(standalone) > 0 {
		sort.Slice(standalone, func(a, b int) bool {
			return images[standalone[a]].ID < images[standalone[b]].ID
		})
		phase := phaseOf(fmt.Sprintf("i:%d", images[standalone[0]].DatasetID))
		for rank, idx := range standalone {
			if isValUnit(rank, valShare, phase) {
				out[idx] = SplitVal
			}
		}
	}

	for key, chunks := range chunksByVideo {
		chunkIDs := make([]int, 0, len(chunks))
		for c := range chunks {
			chunkIDs = append(chunkIDs, c)
		}
		sort.Ints(chunkIDs)
		phase := phaseOf(key)
		for rank, c := range chunkIDs {
			if isValUnit(rank, valShare, phase) {
				for _, idx := range chunks[c] {
					out[idx] = SplitVal
				}
			}
		}
	}

	ensureBothSides(out)
	return out
}

// isValUnit marks unit `rank` as val whenever the running val quota crosses an
// integer boundary (Bresenham-style spreading), so every sequence hits the
// requested ratio within one unit while earlier assignments stay fixed as the
// sequence grows.
func isValUnit(rank int, valShare, phase float64) bool {
	return int(float64(rank+1)*valShare+phase) > int(float64(rank)*valShare+phase)
}

func phaseOf(key string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return float64(h.Sum32()%1000) / 1000
}

// ensureBothSides keeps tiny datasets usable: an empty val (or train) folder
// would make YOLO training fail outright. Only kicks in for datasets so small
// that stability hardly matters yet.
func ensureBothSides(out []string) {
	if len(out) < 2 {
		return
	}
	train, val := 0, 0
	for _, v := range out {
		if v == SplitVal {
			val++
		} else {
			train++
		}
	}
	if val == 0 {
		out[len(out)-1] = SplitVal
	}
	if train == 0 {
		out[0] = SplitTrain
	}
}
