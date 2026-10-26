package command

import (
	"fmt"
	"text/tabwriter"
	"time"

	"terminal-mcp/internal/clierr"
)

func (r *Runner) load(args []string) error {
	pos, _, _, err := splitArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return clierr.Usage("usage: load <name>...")
	}
	var failed bool
	for _, name := range pos {
		res, err := r.mgr.Load(name)
		if err != nil {
			fmt.Fprintf(r.err, "%s: FAILED: %v\n", name, err)
			failed = true
			continue
		}
		r.note(res)
		if res.Started {
			fmt.Fprintf(r.out, "%s: loaded (%s, via %s)\n", name, describe(res.Info), res.Method)
		} else {
			fmt.Fprintf(r.out, "%s: already loaded (%s)\n", name, describe(res.Info))
		}
	}
	if failed {
		return clierr.Reported
	}
	return nil
}

func (r *Runner) unload(args []string) error {
	pos, flags, _, err := splitArgs(args, nil, map[string]bool{"all": true})
	if err != nil {
		return err
	}
	if _, all := flagVal(flags, "all"); all {
		if len(pos) > 0 {
			return clierr.Usage("unload: use names or --all, not both")
		}
		stopped, err := r.mgr.UnloadAll()
		if len(stopped) == 0 && err == nil {
			fmt.Fprintln(r.out, "no MCP servers loaded")
		}
		for _, n := range stopped {
			fmt.Fprintf(r.out, "%s: stopped\n", n)
		}
		return err
	}
	if len(pos) == 0 {
		return clierr.Usage("usage: unload <name>... | --all")
	}
	var failed bool
	for _, name := range pos {
		was, err := r.mgr.Unload(name)
		switch {
		case err != nil:
			fmt.Fprintf(r.err, "%s: FAILED: %v\n", name, err)
			failed = true
		case was:
			fmt.Fprintf(r.out, "%s: stopped\n", name)
		default:
			fmt.Fprintf(r.out, "%s: was not loaded\n", name)
		}
	}
	if failed {
		return clierr.Reported
	}
	return nil
}

func (r *Runner) loaded(args []string) error {
	if len(args) > 0 {
		return clierr.Usage("loaded takes no arguments")
	}
	list := r.mgr.Loaded()
	if len(list) == 0 {
		fmt.Fprintln(r.out, "no MCP servers loaded")
		return nil
	}
	tw := tabwriter.NewWriter(r.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tPID\tUPTIME\tIDLE\tLEFT\tTOOLS\tSERVER")
	for _, i := range list {
		idle, left := "off", "-"
		if i.IdleMS > 0 {
			idle = (time.Duration(i.IdleMS) * time.Millisecond).String()
			if i.Active == 0 {
				left = max(0, time.Duration(i.IdleMS)*time.Millisecond-time.Since(i.LastUsed)).Round(time.Second).String()
			} else {
				left = "busy"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s %s\n", i.Name, i.Kind, i.PID, time.Since(i.Started).Round(time.Second), idle, left, i.Tools, i.Server, i.Version)
	}
	return tw.Flush()
}
