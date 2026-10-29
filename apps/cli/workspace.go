package main

import (
	"fmt"
	"io"
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
	case "list":
		if len(args[1:]) > 0 {
			return fmt.Errorf("workspace list: unexpected argument %q", args[1])
		}
		list, err := c.ListWorkspaces()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "ID\tNAME\tSTATUS")
		for _, ws := range list {
			printWorkspace(stdout, ws)
		}
		return nil
	case "get", "start", "stop", "delete":
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

// workspaceCreate implements "workspace create <name> [--user ID]".
func workspaceCreate(c *Client, args []string, stdout io.Writer) error {
	user := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--user" && i+1 < len(args):
			i++
			user = args[i]
		case strings.HasPrefix(a, "--user="):
			user = strings.TrimPrefix(a, "--user=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("workspace create: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return fmt.Errorf("workspace create: missing workspace name")
	}
	if len(rest) > 1 {
		return fmt.Errorf("workspace create: unexpected argument %q", rest[1])
	}
	if user == "" {
		user = defaultUser
	}
	ws, err := c.CreateWorkspace(user, rest[0])
	if err != nil {
		return err
	}
	printWorkspace(stdout, ws)
	return nil
}

// workspaceOne implements the single-workspace actions get|start|stop|delete.
func workspaceOne(c *Client, action, id string, stdout io.Writer) error {
	switch action {
	case "get":
		ws, err := c.GetWorkspace(id)
		if err != nil {
			return err
		}
		printWorkspace(stdout, ws)
		return nil
	case "start", "stop":
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

// printWorkspace prints one workspace as a tab-separated row.
func printWorkspace(w io.Writer, ws types.Workspace) {
	fmt.Fprintf(w, "%s\t%s\t%s\n", ws.ID, ws.Name, ws.Status)
}
