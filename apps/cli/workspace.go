package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// defaultUser is the user_id sent by "workspace create" without --user.
const defaultUser = "local-user"

// cmdWorkspace implements "pc workspace create|list|get|start|stop|delete".
func cmdWorkspace(c *Client, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("workspace: missing action (create|list|get|start|stop|delete)")
	}
	switch args[0] {
	case "create":
		return workspaceCreate(c, args[1:], stdout)
	case "start":
		return workspaceStart(c, args[1:], stdout)
	case "list":
		if len(args[1:]) > 0 {
			return fmt.Errorf("workspace list: unexpected argument %q", args[1])
		}
		list, err := c.ListWorkspaces()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "ID\tNAME\tSTATUS\tNODE")
		for _, ws := range list {
			printWorkspace(stdout, ws)
		}
		return nil
	case "get", "stop", "delete":
		if len(args) < 2 {
			return fmt.Errorf("workspace %s: missing workspace id", args[0])
		}
		if len(args) > 2 {
			return fmt.Errorf("workspace %s: unexpected argument %q", args[0], args[2])
		}
		return workspaceOne(c, args[0], args[1], stdout)
	default:
		return fmt.Errorf("workspace: unknown action %q (create|list|get|start|stop|delete)", args[0])
	}
}

// parsePlacementFlags extracts the --cpu/--mem/--disk/--gpus/--selector
// requirements from args, in "--flag value" and "--flag=value" forms,
// returning the remaining args and the requirements.
func parsePlacementFlags(action string, args []string) ([]string, WorkspaceResources, error) {
	var res WorkspaceResources
	var cpu, mem, disk, gpus *int64
	sel := map[string]string{}
	var rest []string
	setInt := func(flag, val string, dst **int64) error {
		v, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("workspace %s: invalid value %q for %s", action, val, flag)
		}
		*dst = &v
		return nil
	}
	setFlag := func(flag, val string) error {
		switch flag {
		case "--cpu":
			return setInt(flag, val, &cpu)
		case "--mem":
			return setInt(flag, val, &mem)
		case "--disk":
			return setInt(flag, val, &disk)
		case "--gpus":
			return setInt(flag, val, &gpus)
		default:
			return fmt.Errorf("workspace %s: unknown flag %q", action, flag)
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--cpu" || a == "--mem" || a == "--disk" || a == "--gpus":
			if i+1 >= len(args) {
				return nil, res, fmt.Errorf("workspace %s: missing value for %s", action, a)
			}
			i++
			if err := setFlag(a, args[i]); err != nil {
				return nil, res, err
			}
		case strings.HasPrefix(a, "--cpu=") || strings.HasPrefix(a, "--mem=") ||
			strings.HasPrefix(a, "--disk=") || strings.HasPrefix(a, "--gpus="):
			name, val, _ := strings.Cut(a, "=")
			if err := setFlag(name, val); err != nil {
				return nil, res, err
			}
		case a == "--selector" && i+1 < len(args):
			i++
			if err := parseSelectorKV(action, sel, args[i]); err != nil {
				return nil, res, err
			}
		case strings.HasPrefix(a, "--selector="):
			if err := parseSelectorKV(action, sel, strings.TrimPrefix(a, "--selector=")); err != nil {
				return nil, res, err
			}
		default:
			rest = append(rest, a)
		}
	}
	if cpu != nil {
		res.CPU = int(*cpu)
	}
	if mem != nil {
		res.MemoryMB = *mem
	}
	if disk != nil {
		res.DiskGB = *disk
	}
	if gpus != nil {
		res.GPUs = int(*gpus)
	}
	if len(sel) > 0 {
		res.Selector = sel
	}
	return rest, res, nil
}

// parseSelectorKV adds a k=v node selector entry.
func parseSelectorKV(action string, sel map[string]string, kv string) error {
	k, v, ok := strings.Cut(kv, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("workspace %s: invalid selector %q (want k=v)", action, kv)
	}
	sel[k] = v
	return nil
}

// workspaceCreate implements
// "workspace create <name> [--user ID] [--cpu N] [--mem MB] [--disk GB] [--gpus N] [--selector k=v]".
func workspaceCreate(c *Client, args []string, stdout io.Writer) error {
	rest, res, err := parsePlacementFlags("create", args)
	if err != nil {
		return err
	}
	user := ""
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--user" && i+1 < len(rest):
			i++
			user = rest[i]
		case strings.HasPrefix(a, "--user="):
			user = strings.TrimPrefix(a, "--user=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("workspace create: unknown flag %q", a)
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		return fmt.Errorf("workspace create: missing workspace name")
	}
	if len(positional) > 1 {
		return fmt.Errorf("workspace create: unexpected argument %q", positional[1])
	}
	if user == "" {
		user = defaultUser
	}
	ws, err := c.CreateWorkspaceWithSpec(WorkspaceSpec{
		UserID: user, Name: positional[0], WorkspaceResources: res,
	})
	if err != nil {
		return err
	}
	printWorkspace(stdout, ws)
	return nil
}

// workspaceStart implements
// "workspace start <id> [--cpu N] [--mem MB] [--disk GB] [--gpus N] [--selector k=v]".
func workspaceStart(c *Client, args []string, stdout io.Writer) error {
	rest, res, err := parsePlacementFlags("start", args)
	if err != nil {
		return err
	}
	for _, a := range rest {
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("workspace start: unknown flag %q", a)
		}
	}
	if len(rest) == 0 {
		return fmt.Errorf("workspace start: missing workspace id")
	}
	if len(rest) > 1 {
		return fmt.Errorf("workspace start: unexpected argument %q", rest[1])
	}
	ws, err := c.StartWorkspace(rest[0], res)
	if err != nil {
		return err
	}
	printWorkspace(stdout, ws)
	return nil
}

// workspaceOne implements the single-workspace actions get|stop|delete.
func workspaceOne(c *Client, action, id string, stdout io.Writer) error {
	switch action {
	case "get":
		ws, err := c.GetWorkspace(id)
		if err != nil {
			return err
		}
		printWorkspace(stdout, ws)
		return nil
	case "stop":
		ws, err := c.WorkspaceAction(id, action)
		if err != nil {
			return err
		}
		printWorkspace(stdout, ws)
		return nil
	case "delete":
		if err := c.DeleteWorkspace(id); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "deleted %s\n", id)
		return nil
	default:
		return fmt.Errorf("workspace: unknown action %q", action)
	}
}

// printWorkspace prints one workspace as a tab-separated row; unplaced
// workspaces show "-" in the NODE column.
func printWorkspace(w io.Writer, ws types.Workspace) {
	node := ws.NodeID
	if node == "" {
		node = "-"
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ws.ID, ws.Name, ws.Status, node)
}
