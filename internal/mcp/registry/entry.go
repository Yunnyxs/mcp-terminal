package registry

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const DefaultIdle = 30 * time.Minute

type Entry struct {
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	IdleTimeout string            `json:"idle_timeout,omitempty"`
}

type Config map[string]Entry

func (e Entry) Kind() string {
	if e.URL != "" {
		return "http"
	}
	return "stdio"
}

func (e Entry) IdleDuration() (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(e.IdleTimeout)) {
	case "":
		return DefaultIdle, nil
	case "0", "off", "never":
		return 0, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(e.IdleTimeout))
	if err != nil {
		return 0, fmt.Errorf("idle_timeout %q is invalid (examples: 30m, 90s, off): %w", e.IdleTimeout, err)
	}
	if d < time.Second {
		return 0, fmt.Errorf("idle_timeout %q is too short (minimum 1s, or 0/off)", e.IdleTimeout)
	}
	return d, nil
}

func (e Entry) Validate() error {
	hasCmd, hasURL := e.Command != "", e.URL != ""
	switch {
	case hasCmd && hasURL:
		return errors.New("set exactly one of: command or url, not both")
	case !hasCmd && !hasURL:
		return errors.New("command or url is required")
	}
	if hasURL {
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url %q must be http:// or https://", e.URL)
		}
		if len(e.Args) > 0 || e.Cwd != "" || len(e.Env) > 0 {
			return errors.New("args, cwd and env are only for command (stdio)")
		}
	} else if len(e.Headers) > 0 {
		return errors.New("headers are only for url (HTTP)")
	}
	if e.Cwd != "" && !filepath.IsAbs(e.Cwd) {
		return fmt.Errorf("cwd %q must be an absolute path", e.Cwd)
	}
	for k := range e.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return fmt.Errorf("env name %q is invalid", k)
		}
	}
	for k := range e.Headers {
		if k == "" || strings.ContainsAny(k, ":\r\n\x00") {
			return fmt.Errorf("header name %q is invalid", k)
		}
	}
	if _, err := e.IdleDuration(); err != nil {
		return err
	}
	return nil
}

func (c Config) Names() []string {
	names := make([]string, 0, len(c))
	for n := range c {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var passEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

func PassEnv() []string { return slices.Clone(passEnv) }

func (e Entry) ChildEnv() []string {
	env := make([]string, 0, len(passEnv)+len(e.Env))
	for _, k := range passEnv {
		if _, overridden := e.Env[k]; overridden {
			continue
		}
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	keys := make([]string, 0, len(e.Env))
	for k := range e.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+e.Env[k])
	}
	return env
}
