package main

import (
	"log"
	"os"

	"terminal-mcp/internal/cli"
	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/host"
	"terminal-mcp/internal/tools"
)

func main() {

	if len(os.Args) > 1 && os.Args[1] == "fileop" {
		fail(tools.ServeFileOp(os.Stdin, os.Stdout))
		return
	}

	if len(os.Args) == 3 && os.Args[1] == "mcphost" {
		fail(runMCPHost(os.Args[2]))
		return
	}
	if len(os.Args) < 2 {
		os.Exit(cli.Run(nil, os.Stdout, os.Stderr))
	}
	switch os.Args[1] {
	case "serve":
		fail(run(os.Args[2:]))
	case "--stdio":
		fail(run(os.Args[1:]))
	default:
		os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
	}
}

func fail(err error) {
	if err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}
}

func runMCPHost(name string) error {
	paths, err := config.PathsFromEnv()
	if err != nil {
		return err
	}
	return host.Serve(name, paths, host.Options{OpenLog: openLog})
}
