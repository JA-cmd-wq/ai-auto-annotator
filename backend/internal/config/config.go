package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

type Config struct {
	Addr           string
	DBPath         string
	StorageDir     string
	ExportDir      string
	EngineMode     string
	LibPath        string
	ModelPath      string
	Threads        int
	PythonURL      string
	ImageLimit     int64
	VideoLimit     int64
	LLMEndpoint    string
	LLMModel       string
	LLMKey         string
	FrontendOrigin string
}

func Load() Config {
	storage := getenv("APP_STORAGE_DIR", filepath.Join("data", "storage"))
	libPath := getenv("LA_LIB", defaultLocateAnythingLib())
	modelPath := getenv("LA_MODEL", defaultLocateAnythingModel())
	return Config{
		Addr:           getenv("APP_ADDR", ":8080"),
		DBPath:         getenv("APP_DB", filepath.Join("data", "annotator.db")),
		StorageDir:     storage,
		ExportDir:      getenv("APP_EXPORT_DIR", filepath.Join("data", "exports")),
		EngineMode:     getenv("LA_ENGINE", "auto"),
		LibPath:        libPath,
		ModelPath:      modelPath,
		Threads:        getenvInt("LA_THREADS", 8),
		PythonURL:      getenv("LA_PYTHON_URL", "http://127.0.0.1:8001"),
		ImageLimit:     getenvInt64("APP_IMAGE_LIMIT", 50*1024*1024),
		VideoLimit:     getenvInt64("APP_VIDEO_LIMIT", 2*1024*1024*1024),
		LLMEndpoint:    getenv("MATPOOL_ENDPOINT", "https://token.matpool.com/v1/chat/completions"),
		LLMModel:       getenv("MATPOOL_LLM", "DeepSeek-V4-Pro"),
		LLMKey:         os.Getenv("MATPOOL_KEY"),
		FrontendOrigin: getenv("APP_FRONTEND_ORIGIN", "http://localhost:5173"),
	}
}

func defaultLocateAnythingLib() string {
	name := "liblocate_anything.so"
	if runtime.GOOS == "windows" {
		name = "liblocate_anything.dll"
	}
	return firstExistingLocatePath(filepath.Join("build", name))
}

func defaultLocateAnythingModel() string {
	return firstExistingLocatePath("locate-anything-f16.gguf")
}

func firstExistingLocatePath(suffix string) string {
	wd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(wd, "..", "..", "locate-anything.cpp-master", suffix),
		filepath.Join(wd, "..", "locate-anything.cpp-master", suffix),
		filepath.Join(wd, "locate-anything.cpp-master", suffix),
	}
	for _, p := range candidates {
		if abs, err := filepath.Abs(p); err == nil {
			if _, statErr := os.Stat(abs); statErr == nil {
				return abs
			}
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func getenvInt64(key string, fallback int64) int64 {
	v, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
