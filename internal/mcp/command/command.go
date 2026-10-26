package command

import (
	"fmt"
	"io"
	"os"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/client"
	"terminal-mcp/internal/mcp/lifecycle"
	"terminal-mcp/internal/mcp/proto"
)

const EnvAutoload = "TMCP_AUTOLOAD"

const Usage = `Separate MCP servers (loaded and called through the terminal):

  load <name>...                     start an MCP server as a separate process (already running is fine)
  loaded                             list running MCP servers
  tools <name> [tool]                short tool list; with <tool>: full description and schema
  call <name> <tool> [json|-]        call a tool; json = argument object, "-" = read from stdin
                                     options: --timeout SECONDS, --no-autoload
  unload <name>... | --all           stop MCP servers
  mcp ls                             list MCP servers in mcps.json
  mcp add <name> [options] -- <cmd>...  add a stdio MCP server (options: --env K=V --cwd DIR --idle 30m --force)
  mcp add <name> --url URL [options]    add an HTTP MCP server (options: --header K=V --idle 30m --force)
  mcp rm <name>                      remove from mcps.json (and stop it if running)

call/tools automatically load an MCP server that is not running (disable with TMCP_AUTOLOAD=0 or --no-autoload).
Write secrets (tokens, API keys) directly in mcps.json (mode 600), not in command arguments:
commands run through the run tool are recorded in full in the activity log.
`

func Handles(name string) bool {
	switch name {
	case "load", "unload", "loaded", "tools", "call", "mcp", "mcp-help":
		return true
	}
	return false
}

func Run(name string, args []string, out, errw io.Writer) error {
	if name == "mcp-help" {
		_, err := fmt.Fprint(out, Usage)
		return err
	}
	p, err := config.PathsFromEnv()
	if err != nil {
		return err
	}
	r := newRunner(out, errw, os.Stdin, p, config.LenientLimits(os.Getenv), os.Getenv(EnvAutoload) != "0")
	return r.dispatch(name, args)
}

type Runner struct {
	out, err io.Writer
	in       io.Reader
	paths    config.Paths
	limits   config.Limits
	autoload bool
	mgr      *lifecycle.Manager
	cl       *client.Client
}

func newRunner(out, errw io.Writer, in io.Reader, p config.Paths, lim config.Limits, autoload bool) *Runner {
	return &Runner{out: out, err: errw, in: in, paths: p, limits: lim, autoload: autoload, mgr: lifecycle.New(p), cl: client.New(p)}
}

func (r *Runner) dispatch(name string, args []string) error {
	switch name {
	case "load":
		return r.load(args)
	case "unload":
		return r.unload(args)
	case "loaded":
		return r.loaded(args)
	case "tools":
		return r.tools(args)
	case "call":
		return r.call(args)
	case "mcp":
		return r.mcp(args)
	}
	return clierr.Usagef("unknown MCP command: %q", name)
}

func (r *Runner) note(res *lifecycle.LoadResult) {
	if res.Warn != "" {
		fmt.Fprintln(r.err, "[tmcp] warning:", res.Warn)
	}
}

func (r *Runner) ensure(name string, noAutoload bool) error {
	res, err := r.mgr.Ensure(name, r.autoload && !noAutoload)
	if err != nil {
		return err
	}
	r.note(res)
	if res.Started {
		fmt.Fprintf(r.err, "[tmcp] %s loaded automatically (%s %s, %d tools)\n", name, res.Info.Server, res.Info.Version, res.Info.Tools)
	}
	return nil
}

func describe(i *proto.Info) string {
	return fmt.Sprintf("%s %s, %d tools, pid %d", i.Server, i.Version, i.Tools, i.PID)
}
