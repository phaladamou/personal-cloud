package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// cmdPs implements "pc ps <workspace-id>".
func cmdPs(c *Client, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("ps: missing workspace id")
	}
	if len(args) > 1 {
		return fmt.Errorf("ps: unexpected argument %q", args[1])
	}
	list, err := c.ListProcesses(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "ID\tCOMMAND\tSTATE\tPID")
	for _, p := range list {
		printProcess(stdout, p)
	}
	return nil
}

// cmdLogs implements "pc logs <workspace-id> <process-id> [--follow]".
func cmdLogs(c *Client, args []string, stdout io.Writer) error {
	var follow bool
	var rest []string
	for _, a := range args {
		switch {
		case a == "--follow":
			follow = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("logs: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 {
		return fmt.Errorf("logs: missing workspace id or process id")
	}
	if len(rest) > 2 {
		return fmt.Errorf("logs: unexpected argument %q", rest[2])
	}
	return c.ProcessLogs(context.Background(), rest[0], rest[1], follow, stdout)
}

// printProcess prints one process as a tab-separated row.
func printProcess(w io.Writer, p types.Process) {
	cmd := p.Command
	if len(p.Args) > 0 {
		cmd += " " + strings.Join(p.Args, " ")
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", p.ID, cmd, p.State, p.PID)
}
