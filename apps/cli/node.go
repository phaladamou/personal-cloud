package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// cmdNode implements "pc node list|get|register|deregister".
func cmdNode(c *Client, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("node: missing action (list|get|register|deregister)")
	}
	switch args[0] {
	case "list":
		if len(args[1:]) > 0 {
			return fmt.Errorf("node list: unexpected argument %q", args[1])
		}
		list, err := c.ListNodes()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "ID\tNAME\tSTATUS\tCPU\tMEMORY_MB\tDISK_GB\tGPUS")
		for _, n := range list {
			printNode(stdout, n)
		}
		return nil
	case "get", "deregister":
		if len(args) < 2 {
			return fmt.Errorf("node %s: missing node id", args[0])
		}
		if len(args) > 2 {
			return fmt.Errorf("node %s: unexpected argument %q", args[0], args[2])
		}
		if args[0] == "get" {
			n, err := c.GetNode(args[1])
			if err != nil {
				return err
			}
			printNode(stdout, n)
			return nil
		}
		if err := c.DeregisterNode(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "deregistered %s\n", args[1])
		return nil
	case "register":
		return nodeRegister(c, args[1:], stdout)
	default:
		return fmt.Errorf("node: unknown action %q (list|get|register|deregister)", args[0])
	}
}

// nodeRegister implements
// "node register <name> --cpu N --mem MB --disk GB [--gpus N] [--label k=v]...".
func nodeRegister(c *Client, args []string, stdout io.Writer) error {
	var spec NodeSpec
	var cpu, mem, disk, gpus *int64
	labels := map[string]string{}
	var rest []string
	setInt := func(dst **int64, a string, val string) error {
		v, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("node register: invalid value %q for %s", val, a)
		}
		*dst = &v
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--cpu" || a == "--mem" || a == "--disk" || a == "--gpus":
			if i+1 >= len(args) {
				return fmt.Errorf("node register: missing value for %s", a)
			}
			i++
			var dst **int64
			switch a {
			case "--cpu":
				dst = &cpu
			case "--mem":
				dst = &mem
			case "--disk":
				dst = &disk
			case "--gpus":
				dst = &gpus
			}
			if err := setInt(dst, a, args[i]); err != nil {
				return err
			}
		case strings.HasPrefix(a, "--cpu=") || strings.HasPrefix(a, "--mem=") ||
			strings.HasPrefix(a, "--disk=") || strings.HasPrefix(a, "--gpus="):
			name, val, _ := strings.Cut(a, "=")
			var dst **int64
			switch name {
			case "--cpu":
				dst = &cpu
			case "--mem":
				dst = &mem
			case "--disk":
				dst = &disk
			case "--gpus":
				dst = &gpus
			}
			if err := setInt(dst, name, val); err != nil {
				return err
			}
		case a == "--label" && i+1 < len(args):
			i++
			if err := parseLabel(labels, args[i]); err != nil {
				return err
			}
		case strings.HasPrefix(a, "--label="):
			if err := parseLabel(labels, strings.TrimPrefix(a, "--label=")); err != nil {
				return err
			}
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("node register: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return fmt.Errorf("node register: missing node name")
	}
	if len(rest) > 1 {
		return fmt.Errorf("node register: unexpected argument %q", rest[1])
	}
	if cpu == nil || mem == nil || disk == nil {
		return fmt.Errorf("node register: --cpu, --mem and --disk are required")
	}
	spec.Name = rest[0]
	spec.CPU = int(*cpu)
	spec.MemoryMB = *mem
	spec.DiskGB = *disk
	if gpus != nil {
		spec.GPUs = int(*gpus)
	}
	if len(labels) > 0 {
		spec.Labels = labels
	}
	n, err := c.RegisterNode(spec)
	if err != nil {
		return err
	}
	printNode(stdout, n)
	return nil
}

// parseLabel adds a k=v entry to labels.
func parseLabel(labels map[string]string, kv string) error {
	k, v, ok := strings.Cut(kv, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("node register: invalid label %q (want k=v)", kv)
	}
	labels[k] = v
	return nil
}

// printNode prints one node as a tab-separated row.
func printNode(w io.Writer, n types.Node) {
	fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\n",
		n.ID, n.Name, n.Status, n.CPU, n.MemoryMB, n.DiskGB, n.GPUs)
}
