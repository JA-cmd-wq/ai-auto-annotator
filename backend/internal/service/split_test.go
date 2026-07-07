package service

import (
	"testing"

	"ai-auto-annotator/backend/internal/model"
)

func frameImage(id int64, video string, frameIdx int) model.Image {
	fi := frameIdx
	return model.Image{ID: id, DatasetID: 1, Kind: model.ImageKindFrame, SourceVideo: video, FrameIndex: &fi}
}

func plainImage(id int64) model.Image {
	return model.Image{ID: id, DatasetID: 1, Kind: model.ImageKindImage}
}

func countVal(assignments []string) int {
	n := 0
	for _, a := range assignments {
		if a == SplitVal {
			n++
		}
	}
	return n
}

func TestAssignSplitsRatio(t *testing.T) {
	var images []model.Image
	for i := 0; i < 578; i++ {
		images = append(images, frameImage(int64(i+1), "a.mp4", i))
	}
	got := AssignSplits(images, 0.8)
	val := countVal(got)
	// 578 frames = 12 chunks of 50; expect 2-3 val chunks (~100-150 frames).
	if val < 78 || val > 178 {
		t.Fatalf("val count %d out of expected range for 8:2 split of 578 frames", val)
	}
}

func TestAssignSplitsStableWhenAppending(t *testing.T) {
	var batch1 []model.Image
	for i := 0; i < 300; i++ {
		batch1 = append(batch1, frameImage(int64(i+1), "a.mp4", i))
	}
	for i := 0; i < 40; i++ {
		batch1 = append(batch1, plainImage(int64(1000+i)))
	}
	first := AssignSplits(batch1, 0.8)

	// Second batch: a new video plus more standalone images appended.
	batch2 := append([]model.Image{}, batch1...)
	for i := 0; i < 400; i++ {
		batch2 = append(batch2, frameImage(int64(2000+i), "b.mp4", i))
	}
	for i := 0; i < 25; i++ {
		batch2 = append(batch2, plainImage(int64(3000+i)))
	}
	second := AssignSplits(batch2, 0.8)

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("image %d flipped from %s to %s after appending new material", batch1[i].ID, first[i], second[i])
		}
	}
}

func TestAssignSplitsChunkCoherence(t *testing.T) {
	var images []model.Image
	for i := 0; i < 500; i++ {
		images = append(images, frameImage(int64(i+1), "a.mp4", i))
	}
	got := AssignSplits(images, 0.8)
	for i, img := range images {
		chunk := *img.FrameIndex / splitChunkFrames
		if got[i] != got[chunk*splitChunkFrames] {
			t.Fatalf("frame %d disagrees with its chunk %d", i, chunk)
		}
	}
}

func TestAssignSplitsNoSplit(t *testing.T) {
	images := []model.Image{plainImage(1), plainImage(2), plainImage(3)}
	for _, ratio := range []float64{0, 1} {
		got := AssignSplits(images, ratio)
		if countVal(got) != 0 {
			t.Fatalf("ratio %v should disable the split", ratio)
		}
	}
}

func TestAssignSplitsTinyDatasetHasBothSides(t *testing.T) {
	images := []model.Image{plainImage(1), plainImage(2), plainImage(3), plainImage(4)}
	got := AssignSplits(images, 0.8)
	val := countVal(got)
	if val == 0 || val == len(images) {
		t.Fatalf("tiny dataset must still produce both sides, got %v", got)
	}
}
