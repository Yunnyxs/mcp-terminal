package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/command"
)

const usage = `terminal-mcp: MCP server for the local terminal

Usage: terminal-mcp <command> [options]        (short alias: tmcp)

  serve [--stdio]                run the server (called by systemd; --stdio for Claude Desktop)
  status                         service status, local HTTP probe, URL, sudo
  url                            print the connector URL for claude.ai
  restart [server|tunnel|all]    restart; default is server (URL stays), tunnel/all changes the URL
  start | stop                   start / stop the server and tunnel (the URL changes on start)
  logs [server|tunnel|activity]  show logs; -n COUNT (default 50), -f to follow
  rotate                         replace the token (old URL stops working), then restart the server
  load|unload|loaded|tools|call|mcp   separate MCP servers through the terminal (see: tmcp mcp-help)
  version | help
`

type cmd struct{ out, err io.Writer }

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	c := &cmd{out: stdout, err: stderr}
	name, rest := args[0], args[1:]

	var err error
	switch {
	case name == "help" || name == "-h" || name == "--help":
		fmt.Fprint(stdout, usage)
	case name == "version" || name == "--version":
		fmt.Fprintln(stdout, "terminal-mcp", config.Version)
	case command.Handles(name):
		err = command.Run(name, rest, stdout, stderr)
	default:
		var known bool
		if known, err = c.service(name, rest); !known {
			fmt.Fprintf(stderr, "unknown command: %q\n\n%s", name, usage)
			return 2
		}
	}
	return exitCode(err, stderr)
}

func (c *cmd) service(name string, rest []string) (known bool, err error) {
	switch name {
	case "status":
		return true, noArgs("status", rest, c.status)
	case "url":
		return true, noArgs("url", rest, c.url)
	case "start":
		return true, noArgs("start", rest, c.start)
	case "stop":
		return true, noArgs("stop", rest, c.stop)
	case "rotate":
		return true, noArgs("rotate", rest, c.rotate)
	case "restart":
		return true, c.restart(rest)
	case "logs":
		return true, c.logs(rest)
	}
	return false, nil
}

func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	var ue clierr.Usage
	if errors.As(err, &ue) {
		fmt.Fprintln(stderr, "Error:", ue)
		return 2
	}
	if !errors.Is(err, clierr.Reported) {
		fmt.Fprintln(stderr, "Error:", err)
	}
	return 1
}

func noArgs(name string, rest []string, f func() error) error {
	if len(rest) > 0 {
		return fmt.Errorf("%s takes no arguments: %s", name, strings.Join(rest, " "))
	}
	return f()
}
