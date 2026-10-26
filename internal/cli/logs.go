package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/config"
)

func (c *cmd) logs(args []string) error {
	source := "server"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		source, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(c.err)
	n := fs.Int("n", 50, "number of last lines")
	follow := fs.Bool("f", false, "follow the log (Ctrl+C to stop)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return clierr.Reported
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown argument: %s", strings.Join(fs.Args(), " "))
	}
	if *n < 1 {
		return errors.New("-n must be >= 1")
	}

	argv, err := logCommand(source, *n, *follow)
	if err != nil {
		return err
	}
	proc := exec.Command(argv[0], argv[1:]...)
	proc.Stdout, proc.Stderr = c.out, c.err
	if err := proc.Run(); err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}

func logCommand(source string, n int, follow bool) ([]string, error) {
	switch source {
	case "server", "tunnel":
		unit := serverUnit
		if source == "tunnel" {
			unit = tunnelUnit
		}
		argv := []string{"journalctl", "--user", "-u", unit, "-n", strconv.Itoa(n), "--no-pager"}
		if follow {
			argv = append(argv, "-f")
		}
		return argv, nil
	case "activity":
		s, err := config.FromEnvFile()
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(s.ActivityLog); err != nil {
			return nil, fmt.Errorf("no activity log yet: %s", s.ActivityLog)
		}
		argv := []string{"tail", "-n", strconv.Itoa(n)}
		if follow {
			argv = append(argv, "-f")
		}
		return append(argv, s.ActivityLog), nil
	}
	return nil, fmt.Errorf("unknown log source %q (choose: server, tunnel, activity)", source)
}
