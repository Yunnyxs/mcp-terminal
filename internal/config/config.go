package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const Version = "0.3.0-lite"

const (
	DefaultHost       = "127.0.0.1"
	DefaultPort       = 8000
	DefaultTimeoutSec = 120
	DefaultMaxOutput  = 30000
	DefaultLLMOutput  = 8000
	DefaultLogMaxMB   = 400
	DefaultMaxEdits   = 10

	DefaultCheckpointKeepAuto = 5
	DefaultCheckpointKeepMax  = 20

	ServerLogMaxBytes = 50 << 20

	noMax             = 1<<31 - 1
	maxOutputCap      = 10_000_000
	logMaxMBCap       = 1 << 20
	maxEditsCap       = 100
	checkpointKeepCap = 1000
)

type Lookup func(key string) string

type Settings struct {
	Host               string
	Port               int
	Token              string
	Root               string
	Shell              string
	Timeout            time.Duration
	MaxOutput          int
	Compact            bool
	LLMOutput          int
	MaxEdits           int
	CheckpointKeepAuto int
	CheckpointKeepMax  int
	LogMaxBytes        int64
	ActivityLog        string
	ServerLog          string
	TrashDir           string
	NotesDir           string
	CheckpointDir      string
	Paths              Paths
}

type Limits struct {
	Timeout   time.Duration
	MaxOutput int
}

func LimitsFrom(get Lookup) (Limits, error) {
	sec, err := intVal(get, "TERMINAL_MCP_TIMEOUT", DefaultTimeoutSec, 1, noMax)
	if err != nil {
		return Limits{}, err
	}
	maxOut, err := intVal(get, "TERMINAL_MCP_MAX_OUTPUT", DefaultMaxOutput, 1000, maxOutputCap)
	if err != nil {
		return Limits{}, err
	}
	return Limits{Timeout: time.Duration(sec) * time.Second, MaxOutput: maxOut}, nil
}

func DefaultLimits() Limits {
	return Limits{Timeout: DefaultTimeoutSec * time.Second, MaxOutput: DefaultMaxOutput}
}

func LenientLimits(get Lookup) Limits {
	l := DefaultLimits()
	if sec, err := intVal(get, "TERMINAL_MCP_TIMEOUT", DefaultTimeoutSec, 1, noMax); err == nil {
		l.Timeout = time.Duration(sec) * time.Second
	}
	if n, err := intVal(get, "TERMINAL_MCP_MAX_OUTPUT", DefaultMaxOutput, 1000, maxOutputCap); err == nil {
		l.MaxOutput = n
	}
	return l
}

func FromEnv() (Settings, error) { return Load(os.Getenv) }

func Load(get Lookup) (Settings, error) {
	paths, err := PathsFrom(get)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{
		Host:  orDefault(get("TERMINAL_MCP_HOST"), DefaultHost),
		Token: get("TERMINAL_MCP_TOKEN"),
		Shell: orDefault(get("TERMINAL_MCP_SHELL"), orDefault(get("SHELL"), "/bin/bash")),
		Paths: paths,
	}
	if s.Port, err = intVal(get, "TERMINAL_MCP_PORT", DefaultPort, 1, 65535); err != nil {
		return Settings{}, err
	}
	lim, err := LimitsFrom(get)
	if err != nil {
		return Settings{}, err
	}
	s.Timeout, s.MaxOutput = lim.Timeout, lim.MaxOutput
	if s.Compact, err = boolVal(get, "TERMINAL_MCP_COMPACT", true); err != nil {
		return Settings{}, err
	}
	if s.LLMOutput, err = intVal(get, "TERMINAL_MCP_LLM_OUTPUT", DefaultLLMOutput, 500, maxOutputCap); err != nil {
		return Settings{}, err
	}
	if s.MaxEdits, err = intVal(get, "TERMINAL_MCP_MAX_EDITS", DefaultMaxEdits, 1, maxEditsCap); err != nil {
		return Settings{}, err
	}
	if s.CheckpointKeepAuto, err = intVal(get, "TERMINAL_MCP_CHECKPOINT_KEEP_AUTO", DefaultCheckpointKeepAuto, 0, checkpointKeepCap); err != nil {
		return Settings{}, err
	}
	if s.CheckpointKeepMax, err = intVal(get, "TERMINAL_MCP_CHECKPOINT_KEEP", DefaultCheckpointKeepMax, 0, checkpointKeepCap); err != nil {
		return Settings{}, err
	}
	logMaxMB, err := intVal(get, "TERMINAL_MCP_LOG_MAX_MB", DefaultLogMaxMB, 1, logMaxMBCap)
	if err != nil {
		return Settings{}, err
	}
	s.LogMaxBytes = int64(logMaxMB) << 20

	for _, f := range []struct {
		dst        *string
		key, label string
	}{
		{&s.ActivityLog, "TERMINAL_MCP_ACTIVITY_LOG", "activity_terminal_mcp.log"},
		{&s.ServerLog, "TERMINAL_MCP_LOG", "terminal_mcp.log"},
		{&s.TrashDir, "TERMINAL_MCP_TRASH", "Trash"},
		{&s.NotesDir, "TERMINAL_MCP_NOTES", "notes"},
		{&s.CheckpointDir, "TERMINAL_MCP_CHECKPOINTS", "Checkpoints"},
	} {
		if *f.dst, err = pathVal(get, f.key, filepath.Join(paths.Dir, f.label)); err != nil {
			return Settings{}, err
		}
	}

	root := get("TERMINAL_MCP_ROOT")
	if root == "" {
		if root, err = os.UserHomeDir(); err != nil {
			return Settings{}, fmt.Errorf("home directory not found, set TERMINAL_MCP_ROOT: %w", err)
		}
	}

	if s.Root, err = filepath.Abs(root); err != nil {
		return Settings{}, err
	}
	if info, err := os.Stat(s.Root); err != nil || !info.IsDir() {
		return Settings{}, fmt.Errorf("TERMINAL_MCP_ROOT is not a directory: %s", s.Root)
	}
	return s, nil
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func pathVal(get Lookup, key, def string) (string, error) {
	if v := get(key); v != "" {
		return filepath.Abs(v)
	}
	return def, nil
}

func boolVal(get Lookup, key string, def bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(get(key))) {
	case "":
		return def, nil
	case "1", "true", "on", "yes":
		return true, nil
	case "0", "false", "off", "no":
		return false, nil
	}
	return false, fmt.Errorf("%s must be 0/1, true/false, on/off or yes/no, got: %q", key, get(key))
}

func intVal(get Lookup, key string, def, min, max int) (int, error) {
	raw := get(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got: %q", key, raw)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%s must be %d-%d, got: %d", key, min, max, n)
	}
	return n, nil
}
