package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// cmdSnapshot implements "pc snapshot create|list|restore|delete".
func cmdSnapshot(c *Client, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("snapshot: missing action (create|list|restore|delete)")
	}
	switch args[0] {
	case "create":
		return snapshotCreate(c, args[1:], stdout)
	case "list":
		if len(args) < 2 {
			return fmt.Errorf("snapshot list: missing workspace id")
		}
		if len(args) > 2 {
			return fmt.Errorf("snapshot list: unexpected argument %q", args[2])
		}
		list, err := c.ListSnapshots(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "ID\tNAME\tVOLUME")
		for _, snap := range list {
			printSnapshot(stdout, snap)
		}
		return nil
	case "restore", "delete":
		if len(args) < 3 {
			return fmt.Errorf("snapshot %s: missing workspace id or snapshot id", args[0])
		}
		if len(args) > 3 {
			return fmt.Errorf("snapshot %s: unexpected argument %q", args[0], args[3])
		}
		return snapshotOne(c, args[0], args[1], args[2], stdout)
	default:
		return fmt.Errorf("snapshot: unknown action %q (create|list|restore|delete)", args[0])
	}
}

// snapshotCreate implements "snapshot create <workspace-id> [--volume
// ID] [--name NAME]".
func snapshotCreate(c *Client, args []string, stdout io.Writer) error {
	var volumeID, name string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--volume" && i+1 < len(args):
			i++
			volumeID = args[i]
		case strings.HasPrefix(a, "--volume="):
			volumeID = strings.TrimPrefix(a, "--volume=")
		case a == "--name" && i+1 < len(args):
			i++
			name = args[i]
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("snapshot create: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return fmt.Errorf("snapshot create: missing workspace id")
	}
	if len(rest) > 1 {
		return fmt.Errorf("snapshot create: unexpected argument %q", rest[1])
	}
	snap, err := c.CreateSnapshot(rest[0], volumeID, name)
	if err != nil {
		return err
	}
	printSnapshot(stdout, snap)
	return nil
}

// snapshotOne implements "snapshot restore|delete <workspace-id>
// <snapshot-id>".
func snapshotOne(c *Client, action, workspaceID, snapshotID string, stdout io.Writer) error {
	switch action {
	case "restore":
		snap, err := c.RestoreSnapshot(workspaceID, snapshotID)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "restored %s\n", snap.ID)
		return nil
	case "delete":
		if err := c.DeleteSnapshot(workspaceID, snapshotID); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "deleted %s\n", snapshotID)
		return nil
	default:
		return fmt.Errorf("snapshot: unknown action %q", action)
	}
}

// printSnapshot prints one snapshot as a tab-separated row.
func printSnapshot(w io.Writer, snap types.Snapshot) {
	fmt.Fprintf(w, "%s\t%s\t%s\n", snap.ID, snap.Name, snap.VolumeID)
}
