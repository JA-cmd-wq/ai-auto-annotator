package repository

import (
	"path/filepath"
	"testing"

	"ai-auto-annotator/backend/internal/model"
)

func newTestStore(t *testing.T) (*Store, int64) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject("p", "")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := store.CreateDataset(p.ID, "d", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return store, ds.ID
}

func addImage(t *testing.T, store *Store, datasetID int64, name string) model.Image {
	t.Helper()
	img, err := store.CreateImage(model.Image{
		DatasetID: datasetID, Kind: model.ImageKindImage,
		FilePath: "/tmp/" + name, FileName: name, Width: 100, Height: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestListImagesForDetect(t *testing.T) {
	store, dsID := newTestStore(t)

	fresh := addImage(t, store, dsID, "fresh.jpg")
	detected := addImage(t, store, dsID, "detected.jpg")
	detectedEmpty := addImage(t, store, dsID, "detected-empty.jpg")
	manual := addImage(t, store, dsID, "manual.jpg")

	// detected: AI results present + status detected.
	if err := store.ReplaceDetections(detected.ID, []model.Detection{
		{RawLabel: "bike", Box: [4]float64{1, 1, 50, 50}, Origin: model.OriginAI},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetImageStatus(detected.ID, model.ImageStatusDetected); err != nil {
		t.Fatal(err)
	}
	// detectedEmpty: ran through AI but found nothing (status only).
	if err := store.SetImageStatus(detectedEmpty.ID, model.ImageStatusDetected); err != nil {
		t.Fatal(err)
	}
	// manual: human-corrected annotations.
	if err := store.ReplaceDetections(manual.ID, []model.Detection{
		{RawLabel: "person", Box: [4]float64{1, 1, 20, 20}, Origin: model.OriginManual, Reviewed: true},
	}); err != nil {
		t.Fatal(err)
	}

	ids := func(imgs []model.Image) map[int64]bool {
		m := map[int64]bool{}
		for _, im := range imgs {
			m[im.ID] = true
		}
		return m
	}

	newScope, err := store.ListImagesForDetect(dsID, model.DetectScopeNew)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(newScope)
	if !got[fresh.ID] || len(got) != 1 {
		t.Fatalf("scope=new should only return the fresh image, got %v", got)
	}

	allScope, err := store.ListImagesForDetect(dsID, model.DetectScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	got = ids(allScope)
	if got[manual.ID] {
		t.Fatal("scope=all must never include manually corrected images")
	}
	if !got[fresh.ID] || !got[detected.ID] || !got[detectedEmpty.ID] || len(got) != 3 {
		t.Fatalf("scope=all should return everything except manual, got %v", got)
	}
}
