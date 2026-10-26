package command

import (
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/mcp/registry"
)

func (r *Runner) mcp(args []string) error {
	if len(args) == 0 {
		return clierr.Usage("usage: mcp ls | mcp add <name> ... | mcp rm <name>")
	}
	switch args[0] {
	case "ls":
		return r.mcpLs()
	case "add":
		return r.mcpAdd(args[1:])
	case "rm":
		return r.mcpRm(args[1:])
	}
	return clierr.Usagef("unknown mcp subcommand: %q (ls, add, rm)", args[0])
}

func (r *Runner) mcpLs() error {
	cfg, err := registry.Load(r.paths.MCPs)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.out, "config: %s\n", r.paths.MCPs)
	if len(cfg) == 0 {
		fmt.Fprintln(r.out, "(empty; add with: mcp add <name> -- <command>...)")
		return nil
	}
	live := map[string]bool{}
	for _, i := range r.mgr.Loaded() {
		live[i.Name] = true
	}
	tw := tabwriter.NewWriter(r.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tSTATUS\tIDLE\tTARGET\tENV/HEADER")
	for _, name := range cfg.Names() {
		e := cfg[name]
		status := "-"
		if live[name] {
			status = "loaded"
		}
		idle := e.IdleTimeout
		if idle == "" {
			idle = registry.DefaultIdle.String() + " (default)"
		}
		target := strings.TrimSpace(e.Command + " " + strings.Join(e.Args, " "))
		if e.URL != "" {
			target = redactURL(e.URL)
		}

		var keys []string
		for k := range e.Env {
			keys = append(keys, k)
		}
		for k := range e.Headers {
			keys = append(keys, k)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", name, e.Kind(), status, idle, target, strings.Join(keys, ","))
	}
	return tw.Flush()
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(url)"
	}
	u.User = nil
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	return u.String()
}

func (r *Runner) mcpAdd(args []string) error {
	pos, flags, rest, err := splitArgs(args,
		map[string]bool{"env": true, "cwd": true, "idle": true, "url": true, "header": true},
		map[string]bool{"force": true})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return clierr.Usage("usage: mcp add <name> [options] -- <command> [args...]  |  mcp add <name> --url URL [options]")
	}
	name := pos[0]
	if err := registry.ValidName(name); err != nil {
		return clierr.Usage(err.Error())
	}
	var e registry.Entry
	e.Cwd, _ = flagVal(flags, "cwd")
	e.IdleTimeout, _ = flagVal(flags, "idle")
	e.URL, _ = flagVal(flags, "url")
	if len(rest) > 0 {
		e.Command, e.Args = rest[0], rest[1:]
	}
	if e.Env, err = parseKV(flagAll(flags, "env"), "env"); err != nil {
		return err
	}
	if e.Headers, err = parseKV(flagAll(flags, "header"), "header"); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return clierr.Usage(err.Error())
	}
	cfg, err := registry.Load(r.paths.MCPs)
	if err != nil {
		return err
	}
	if _, exists := cfg[name]; exists {
		if _, force := flagVal(flags, "force"); !force {
			return fmt.Errorf("%q already exists in %s (overwrite with --force)", name, r.paths.MCPs)
		}
	}
	cfg[name] = e
	if err := registry.Save(r.paths.MCPs, cfg); err != nil {
		return err
	}
	fmt.Fprintf(r.out, "%s: added to %s\n", name, r.paths.MCPs)
	if len(e.Env) > 0 || len(e.Headers) > 0 {
		fmt.Fprintln(r.err, "[tmcp] warning: env/header passed as arguments are recorded in the activity log when this command runs through the run tool; for secrets, edit mcps.json directly")
	}
	return nil
}

func (r *Runner) mcpRm(args []string) error {
	if len(args) != 1 {
		return clierr.Usage("usage: mcp rm <name>")
	}
	name := args[0]
	cfg, err := registry.Load(r.paths.MCPs)
	if err != nil {
		return err
	}
	if _, ok := cfg[name]; !ok {
		return fmt.Errorf("%q is not in %s", name, r.paths.MCPs)
	}
	if was, err := r.mgr.Unload(name); err != nil {
		return fmt.Errorf("cannot stop %q first: %w", name, err)
	} else if was {
		fmt.Fprintf(r.out, "%s: stopped\n", name)
	}
	delete(cfg, name)
	if err := registry.Save(r.paths.MCPs, cfg); err != nil {
		return err
	}
	fmt.Fprintf(r.out, "%s: removed from %s\n", name, r.paths.MCPs)
	return nil
}
