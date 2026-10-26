package command

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/mcp/render"
)

const maxStdinArgs = 4 << 20

func (r *Runner) tools(args []string) error {
	pos, flags, _, err := splitArgs(args, nil, map[string]bool{"no-autoload": true})
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return clierr.Usage("usage: tools <name> [tool]")
	}
	name, tool := pos[0], ""
	if len(pos) == 2 {
		tool = pos[1]
	}
	_, noAuto := flagVal(flags, "no-autoload")
	if err := r.ensure(name, noAuto); err != nil {
		return err
	}
	list, err := r.cl.Tools(name, tool)
	if err != nil {
		return err
	}
	if tool != "" {
		fmt.Fprint(r.out, render.ToolDetail(list[0]))
		return nil
	}
	fmt.Fprintf(r.out, "%s: %d tools (* = required parameter; detail: tools %s <tool>)\n", name, len(list), name)
	fmt.Fprint(r.out, render.Tools(list))
	return nil
}

func (r *Runner) call(args []string) error {
	pos, flags, _, err := splitArgs(args, map[string]bool{"timeout": true}, map[string]bool{"no-autoload": true})
	if err != nil {
		return err
	}
	if len(pos) < 2 || len(pos) > 3 {
		return clierr.Usage(`usage: call <name> <tool> [json|-] [--timeout SECONDS] [--no-autoload]`)
	}
	name, tool := pos[0], pos[1]
	var argJSON []byte
	if len(pos) == 3 {
		if pos[2] == "-" {
			if argJSON, err = io.ReadAll(io.LimitReader(r.in, maxStdinArgs+1)); err != nil {
				return err
			}
			if len(argJSON) > maxStdinArgs {
				return clierr.Usage("stdin arguments too large (max 4 MB)")
			}
		} else {
			argJSON = []byte(pos[2])
		}
	}
	if s := strings.TrimSpace(string(argJSON)); s != "" {
		var probe map[string]any
		if err := json.Unmarshal([]byte(s), &probe); err != nil {
			return clierr.Usage("arguments must be a JSON object, e.g. '{\"q\":\"x\"}': " + err.Error())
		}
	}
	timeout := r.limits.Timeout
	if v, ok := flagVal(flags, "timeout"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return clierr.Usage("--timeout must be a number of seconds > 0")
		}
		timeout = time.Duration(n) * time.Second
	}
	_, noAuto := flagVal(flags, "no-autoload")
	if err := r.ensure(name, noAuto); err != nil {
		return err
	}
	res, err := r.cl.Call(name, tool, argJSON, timeout)
	if err != nil {
		return err
	}
	text, err := render.Result(r.paths, name, tool, res, r.limits.MaxOutput)
	if err != nil {
		return err
	}
	if text != "" {
		fmt.Fprintln(r.out, text)
	}
	if res.IsError {
		fmt.Fprintln(r.err, "[tmcp] tool reported an error (isError=true)")
		return clierr.Reported
	}
	return nil
}
