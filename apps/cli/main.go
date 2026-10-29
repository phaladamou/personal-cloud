// Command pc is the Personal Cloud command-line interface.
//
// It is stdlib-only and thin by design: workspace commands map 1:1 to
// the control-plane REST API, and exec streams command stdio over the
// WebSocket endpoint. Run "pc help" for usage.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// usage documents the CLI. Global flags must precede the subcommand.
const usage = `Usage: pc [--api URL] <command> [args]

Commands:
  login [--token TOKEN]          save an auth token (Phase 1 stub)
  workspace create <name> [--user ID]
  workspace list
  workspace get <id>
  workspace start <id>
  workspace stop <id>
  workspace delete <id>
  help                           show this help

Options:
  --api URL   API base URL (default http://localhost:8080, env PC_API)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Stdin, os.Getenv))
}

// run executes the CLI with the given args and streams; it returns the
// process exit code so tests can drive it without a subprocess.
func run(args []string, stdout, stderr io.Writer, stdin io.Reader, getenv func(string) string) int {
	// Everything after "--" belongs to the exec command, never to flag
	// parsing.
	cliArgs := args
	var execCmd []string
	if i := indexOf(args, "--"); i >= 0 {
		cliArgs = args[:i]
		execCmd = args[i+1:]
	}
	_ = execCmd // wired by the exec command (next commit)

	var apiFlag string
	var rest []string
	for i := 0; i < len(cliArgs); i++ {
		a := cliArgs[i]
		switch {
		case a == "--api" && i+1 < len(cliArgs):
			i++
			apiFlag = cliArgs[i]
		case strings.HasPrefix(a, "--api="):
			apiFlag = strings.TrimPrefix(a, "--api=")
		default:
			rest = append(rest, a)
		}
	}

	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		code := 0
		out := stdout
		if len(rest) == 0 {
			code = 2
			out = stderr
		}
		fmt.Fprint(out, usage)
		return code
	}

	base := apiBase(getenv, apiFlag)
	client := NewClient(base, loadToken(configDir(getenv)))

	var err error
	switch rest[0] {
	case "login":
		err = cmdLogin(rest[1:], stdout, getenv)
	case "workspace":
		err = cmdWorkspace(client, rest[1:], stdout)
	default:
		err = fmt.Errorf("unknown command %q (run \"pc help\")", rest[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "pc: %v\n", err)
		return 1
	}
	return 0
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}
