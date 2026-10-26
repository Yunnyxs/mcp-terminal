package proto

import (
	"encoding/json"
	"time"
)

const (
	OpPing  = "ping"
	OpTools = "tools"
	OpCall  = "call"
	OpStop  = "stop"
)

const (
	MaxRequestBytes  = 8 << 20
	MaxResponseBytes = 128 << 20
)

type Request struct {
	Op        string          `json:"op"`
	Tool      string          `json:"tool,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}

type Info struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	PID      int       `json:"pid"`
	Started  time.Time `json:"started"`
	LastUsed time.Time `json:"last_used"`
	IdleMS   int64     `json:"idle_ms"`
	Active   int       `json:"active"`
	Server   string    `json:"server"`
	Version  string    `json:"version"`
	Tools    int       `json:"tools"`
}

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type Part struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	MIME string `json:"mime,omitempty"`
	URI  string `json:"uri,omitempty"`
	Data []byte `json:"data,omitempty"`
}

type CallResult struct {
	IsError    bool            `json:"is_error,omitempty"`
	Parts      []Part          `json:"parts,omitempty"`
	Structured json.RawMessage `json:"structured,omitempty"`
}

type Response struct {
	OK     bool        `json:"ok"`
	Error  string      `json:"error,omitempty"`
	Info   *Info       `json:"info,omitempty"`
	Tools  []ToolInfo  `json:"tools,omitempty"`
	Result *CallResult `json:"result,omitempty"`
}
