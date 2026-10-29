package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// cmdPorts implements "pc ports register|list|remove": it manages the
// gateway port forwards exposing workspace HTTP services.
func cmdPorts(c *Client, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("ports: missing action (register|list|remove)")
	}
	switch args[0] {
	case "register":
		return portsRegister(c, args[1:], stdout)
	case "list":
		if len(args) < 2 {
			return fmt.Errorf("ports list: missing workspace id")
		}
		if len(args) > 2 {
			return fmt.Errorf("ports list: unexpected argument %q", args[2])
		}
		list, err := c.ListPorts(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "PORT\tTARGET\tPROTOCOL")
		for _, f := range list {
			fmt.Fprintf(stdout, "%d\t%s\t%s\n", f.Port, f.Target, f.Protocol)
		}
		return nil
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("ports remove: missing workspace id")
		}
		if len(args) < 3 {
			return fmt.Errorf("ports remove: missing port")
		}
		if len(args) > 3 {
			return fmt.Errorf("ports remove: unexpected argument %q", args[3])
		}
		port, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("ports remove: invalid port %q", args[2])
		}
		if err := c.RemovePort(args[1], port); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed port %d\n", port)
		return nil
	default:
		return fmt.Errorf("ports: unknown action %q (register|list|remove)", args[0])
	}
}

// portsRegister implements "ports register <id> <port> <target>
// [--protocol PROTO]".
func portsRegister(c *Client, args []string, stdout io.Writer) error {
	var protocol string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--protocol" && i+1 < len(args):
			i++
			protocol = args[i]
		case strings.HasPrefix(a, "--protocol="):
			protocol = strings.TrimPrefix(a, "--protocol=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("ports register: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) < 3 {
		return fmt.Errorf("ports register: missing argument (usage: pc ports register <id> <port> <target>)")
	}
	if len(rest) > 3 {
		return fmt.Errorf("ports register: unexpected argument %q", rest[3])
	}
	port, err := strconv.Atoi(rest[1])
	if err != nil {
		return fmt.Errorf("ports register: invalid port %q", rest[1])
	}
	fwd, err := c.RegisterPort(rest[0], port, rest[2], protocol)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d\t%s\t%s\n", fwd.Port, fwd.Target, fwd.Protocol)
	return nil
}
