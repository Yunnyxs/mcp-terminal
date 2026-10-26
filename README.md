# terminal-mcp

`terminal-mcp` is an MCP (Model Context Protocol) server written in Go that gives an AI client controlled access to
your local machine's terminal and files. It is also a small CLI (`terminal-mcp`, short alias `tmcp`) for managing the
service it runs as.

- **Module:** `terminal-mcp`
- **Go version:** see `go.mod`
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk`

## Features

- Five MCP tools: `run`, `read`, `write`, `edit`, `delete`.
- Two transports: HTTP (for claude.ai, exposed through a Cloudflare tunnel) and stdio (for Claude Desktop).
- Secret-in-URL authentication: the connector URL contains a token (`/<TOKEN>/mcp`), compared in constant time and
  never written to the logs.
- Output compaction: long command output is summarized for the model, while the raw output is stored on disk so it
  can be read again in chunks without re-running the command.
- Safe deletion: `delete` moves files to a Trash folder unless `permanent=true`, and refuses protected paths.
- Optional root access: file tools accept `sudo=true`; a hidden `fileop` subcommand does the privileged work.
- Separate MCP servers: other MCP servers can be registered, loaded and called through the terminal (`tmcp load`,
  `tmcp call`, ...).
- One-command installer that builds the binary, creates a token, installs `cloudflared` and sets up systemd user
  services.

## Quick start

From a clone of this repository (Linux with systemd user services and Go installed):

```bash
./install.sh     # build, configure, start the services
tmcp url         # print the connector URL to add in claude.ai
tmcp status      # check that the server and tunnel are up
```

See [Installation](#installation) for what the installer does and [Security model](#security-model) before exposing the
server.

## MCP tools

Tool descriptions and parameters are intentionally short, because the schema is sent to the model in every session.

| Tool     | Purpose | Parameters |
|----------|---------|------------|
| `run`    | Run a shell command. Long output is summarized. | `command`, `cwd`, `timeout` (1-3600 s), `raw` |
| `read`   | Read a UTF-8 text file by byte range. The output ends with `next_offset` when truncated. | `path`, `offset`, `limit`, `sudo` |
| `write`  | Write a whole file atomically. Refuses to replace an existing file unless `overwrite=true`. | `path`, `content`, `overwrite`, `sudo` |
| `edit`   | Replace exact text in an existing file. Fails if the text is missing or matches more than once (unless `replace_all`). | `path`, `old`, `new`, `replace_all`, `sudo` |
| `delete` | Delete files or directories by explicit path (no wildcards, max 50). Moves to Trash unless `permanent=true`. | `paths`, `recursive`, `permanent`, `sudo` |

Notes:

- Relative paths are resolved against `TERMINAL_MCP_ROOT`. The root is not a sandbox: `run` can reach anywhere.
- When a file tool fails with a permission error, the message ends with `(retry with sudo=true)`.

## Project layout

```
cmd/
  terminal-mcp/     entry point: main.go (command dispatch), serve.go (run the server), logsink.go (logging)
  fakemcp/          tiny fake MCP server used by the end-to-end tests
internal/
  auth/             token validation and the path-token HTTP middleware
  cli/              service commands: status, url, start, stop, restart, logs, rotate (wraps systemctl/journalctl)
  clierr/           CLI error types and exit-code mapping
  compact/          output compaction: line collapsing, size fitting, per-command filters (git, files, runners, ...)
  config/           settings from environment variables, data paths, config.env parsing
  fsutil/           atomic file writes
  logging/          activity log and size-based log rotation
  mcp/              separate-MCP subsystem: client, command, host, lifecycle, proto, registry, render
  rawstore/         raw output storage (run-*.txt, mode 600, pruned after 7 days or 200 files)
  result/           tool result helpers
  server/           MCP server construction, HTTP handler, compaction glue
  tools/            the five MCP tools and the root helper (fileop)
installer/          one function per file; the order of steps is in installer/main.sh
scripts/e2e.sh      end-to-end test script
units/              systemd unit template
install.sh          installer entry point
uninstall.sh        uninstaller
```

## Installation

Requirements: Linux with systemd (user services), Go, and network access to download `cloudflared` if it is missing.

```bash
./install.sh              # asks y/N before granting root access
./install.sh --yes-root   # grants root access without asking
```

The installer:

1. builds the binary into `~/.local/bin`;
2. creates a token and picks a port (existing token and port are kept on re-runs, so it is safe to upgrade);
3. installs `cloudflared` if needed;
4. installs two systemd user services (server and tunnel) into `~/.config/systemd/user`;
5. prints the connector URL to add in claude.ai.

Relevant locations:

| What | Where |
|------|-------|
| Binary | `~/.local/bin/terminal-mcp` |
| Service configuration | `~/.config/terminal-mcp/config.env` |
| Data folder | `~/.mcp-terminal` (Trash, notes, logs, MCP runtime data) |
| systemd units | `~/.config/systemd/user/` |

To remove everything, run `./uninstall.sh`.

## Usage

```
terminal-mcp <command> [options]        (short alias: tmcp)

  serve [--stdio]                run the server (called by systemd; --stdio for Claude Desktop)
  status                         service status, local HTTP probe, URL, sudo
  url                            print the connector URL for claude.ai
  restart [server|tunnel|all]    restart; default is server (the URL stays), tunnel/all changes the URL
  start | stop                   start / stop the server and tunnel (the URL changes on start)
  logs [server|tunnel|activity]  show logs; -n COUNT (default 50), -f to follow
  rotate                         replace the token (the old URL stops working), then restart the server
  load|unload|loaded|tools|call|mcp   separate MCP servers through the terminal (see: tmcp mcp-help)
  version | help
```

Running `terminal-mcp` with no arguments prints the help text only; it never starts a second server. Exit codes: `0`
success, `1` failure, `2` unknown command or wrong usage.

### Connecting from claude.ai

Run `tmcp url` and add the printed URL as a custom connector. Anyone who has the URL can use the server, so treat it
like a password and run `tmcp rotate` if it leaks.

### Connecting from Claude Desktop

Configure Claude Desktop to start `terminal-mcp --stdio` (or `terminal-mcp serve --stdio`).

### Separate MCP servers

```
tmcp mcp add <name> [--env K=V] [--cwd DIR] [--idle 30m] [--force] -- <cmd>...   add a stdio MCP
tmcp mcp add <name> --url URL [--header K=V] [--idle 30m] [--force]              add an HTTP MCP
tmcp mcp ls                      list registered MCPs (mcps.json)
tmcp load <name>...              start MCPs as separate processes
tmcp loaded                      list running MCPs
tmcp tools <name> [tool]         compact tool list; with <tool>: full description and schema
tmcp call <name> <tool> [json|-] call a tool (json = argument object, "-" = read from stdin)
tmcp unload <name>... | --all    stop MCPs
tmcp mcp rm <name>               remove from mcps.json (and stop it if running)
```

`call` and `tools` load an MCP automatically when it is not running (disable with `TMCP_AUTOLOAD=0` or
`--no-autoload`). Write secrets directly in `mcps.json` (mode 600), not in command arguments: commands run through the
`run` tool are logged in full in the activity log.

## Configuration

Settings come from environment variables. Under systemd they are read from `~/.config/terminal-mcp/config.env`
(`~` is not expanded there, so write full paths). Invalid values make the server fail at startup instead of being
silently ignored.

| Variable | Default | Description |
|----------|---------|-------------|
| `TERMINAL_MCP_TOKEN` | generated by the installer | Secret path token. At least 32 characters; only `A-Z a-z 0-9 - _`. |
| `TERMINAL_MCP_HOST` | `127.0.0.1` | Address the HTTP server listens on. |
| `TERMINAL_MCP_PORT` | `8000` | HTTP port. |
| `TERMINAL_MCP_ROOT` | home directory | Base directory for relative paths. Must exist. |
| `TERMINAL_MCP_TIMEOUT` | `120` | Default command timeout in seconds. |
| `TERMINAL_MCP_MAX_OUTPUT` | `30000` | Capture and log limit for `run` output (1000-10000000). |
| `TERMINAL_MCP_LLM_OUTPUT` | `8000` | Limit for the output body sent to the client (min 500). |
| `TERMINAL_MCP_COMPACT` | on | Boolean (`1/true/on/yes`, `0/false/off/no`) to enable output compaction. |
| `TERMINAL_MCP_SHELL` | `$SHELL`, or `/bin/bash` | Shell used by `run`. |
| `TERMINAL_MCP_LOG_MAX_MB` | `400` | Size limit that triggers log rotation. |
| `TERMINAL_MCP_LOG` | `<data dir>/terminal_mcp.log` | Server log file. |
| `TERMINAL_MCP_ACTIVITY_LOG` | `<data dir>/activity_terminal_mcp.log` | Activity log file. |
| `TERMINAL_MCP_TRASH` | `<data dir>/Trash` | Where `delete` moves files. |
| `TERMINAL_MCP_NOTES` | `<data dir>/notes` | Notes folder. |
| `TMCP_DIR` | `~/.mcp-terminal` | Data folder (run sockets, MCP logs, stored outputs). |
| `TMCP_MCPS` | `<data dir>/mcps.json` | Registry file of separate MCPs. |
| `TMCP_AUTOLOAD` | on | Set to `0` to disable auto-load in `call` and `tools`. |

## Security model

- The token is part of the URL path. Requests with a wrong token get `404`, and the path (which contains the token) is
  never logged.
- DNS-rebinding protection is disabled on purpose, because the `Host` header carries the tunnel domain. The path token
  replaces it.
- The server is deliberately powerful: `run` can execute any command as your user, and `sudo=true` can use passwordless
  `sudo`. Only expose it to clients you trust.
- `delete` refuses protected paths (and their parents) and uses Trash by default.
- Raw outputs are stored with mode `600` in a folder with mode `700`.

## Output compaction and raw storage

When output is longer than the client budget, `compact` collapses repeated lines and applies per-command filters
(for example for `git`, file listings, package managers and test runners) so the important lines survive; on failure
the tail is kept longer, because errors usually appear at the end. If anything was merged, dropped or cut, the raw
output is saved by `rawstore` as `run-<time>-<random>.txt`, so it can be read again in chunks with the `read` tool.
Old files are pruned after seven days or when the file-count limit is exceeded. Pass `raw=true` to `run` to skip
compaction.

## Testing

```bash
go test ./...            # unit and integration tests
scripts/e2e.sh           # end-to-end: real binary, stdio and HTTP servers, a fake separate MCP (fakemcp)
```

`scripts/e2e.sh` builds the binary into a temporary folder and redirects `HOME`, `TMCP_DIR` and `TMCP_MCPS` there, so
your running service, `~/.mcp-terminal` and `~/.config/terminal-mcp` are not touched. Options:

| Variable | Meaning |
|----------|---------|
| `E2E_TMP` | Parent folder for the temporary work folder (keep it short: unix socket paths are limited to about 100 bytes). |
| `E2E_KEEP` | `1` keeps the work folder at the end, for investigating failures. |
| `E2E_SUDO` | `0` skips the sudo section even when passwordless sudo is available. |

## Development tips

- Start with `cmd/terminal-mcp/main.go` (command dispatch) and `internal/server` (MCP server construction). Each package under `internal/` has one responsibility, listed in the layout above.
- The installer keeps one function per file, and the file name is the function name.
- `internal/config/paths.go` and `install.sh` must agree on the data folder name and config location.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
