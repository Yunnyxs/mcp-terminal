package render

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"terminal-mcp/internal/config"
)

const outKeepDays = 7

func safeTool(tool string) string {
	b := []byte(tool)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			b[i] = '_'
		}
	}
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}

func extFor(mime string) string {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0])) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "application/pdf":
		return ".pdf"
	case "application/json":
		return ".json"
	case "text/plain":
		return ".txt"
	}
	return ".bin"
}

func saveOut(p config.Paths, file string, data []byte) (string, error) {
	if err := os.MkdirAll(p.OutDir, 0o700); err != nil {
		return "", err
	}
	pruneOut(p.OutDir)
	path := filepath.Join(p.OutDir, file)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func pruneOut(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-outKeepDays * 24 * time.Hour)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
