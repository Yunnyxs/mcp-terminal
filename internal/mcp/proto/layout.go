package proto

import (
	"fmt"
	"path/filepath"

	"terminal-mcp/internal/config"
)

const maxSocketPath = 100

func Socket(p config.Paths, name string) (string, error) {
	s := filepath.Join(p.RunDir, name+".sock")
	if len(s) > maxSocketPath {
		return "", fmt.Errorf("socket path too long (%d bytes, max %d): move %s to a shorter directory", len(s), maxSocketPath, config.EnvDir)
	}
	return s, nil
}

func PIDFile(p config.Paths, name string) string { return filepath.Join(p.RunDir, name+".pid") }

func LockFile(p config.Paths, name string) string { return filepath.Join(p.RunDir, name+".lock") }

func LogFile(p config.Paths, name string) string { return filepath.Join(p.LogDir, name+".log") }
