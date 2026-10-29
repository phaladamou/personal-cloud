package main

import (
	"fmt"
	"io"
	"os"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// cmdFiles implements "pc files ls|cat|put|mkdir|rm".
func cmdFiles(c *Client, args []string, stdout io.Writer, stdin io.Reader) error {
	if len(args) == 0 {
		return fmt.Errorf("files: missing action (ls|cat|put|mkdir|rm)")
	}
	switch args[0] {
	case "ls":
		return filesLs(c, args[1:], stdout)
	case "cat":
		return filesCat(c, args[1:], stdout)
	case "put":
		return filesPut(c, args[1:], stdout, stdin)
	case "mkdir":
		return filesMkdir(c, args[1:], stdout)
	case "rm":
		return filesRm(c, args[1:], stdout)
	default:
		return fmt.Errorf("files: unknown action %q (ls|cat|put|mkdir|rm)", args[0])
	}
}

// filesLs implements "files ls <workspace-id> [path]".
func filesLs(c *Client, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("files ls: missing workspace id")
	}
	if len(args) > 2 {
		return fmt.Errorf("files ls: unexpected argument %q", args[2])
	}
	dir := ""
	if len(args) == 2 {
		dir = args[1]
	}
	list, err := c.ListFiles(args[0], dir)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "PATH\tTYPE\tSIZE")
	for _, e := range list {
		printFileEntry(stdout, e)
	}
	return nil
}

// filesCat implements "files cat <workspace-id> <path>".
func filesCat(c *Client, args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("files cat: missing workspace id or path")
	}
	if len(args) > 2 {
		return fmt.Errorf("files cat: unexpected argument %q", args[2])
	}
	data, err := c.ReadFile(args[0], args[1])
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}

// filesPut implements "files put <workspace-id> <path> [local-file]".
// Without a local file, the content is read from stdin.
func filesPut(c *Client, args []string, stdout io.Writer, stdin io.Reader) error {
	if len(args) < 2 {
		return fmt.Errorf("files put: missing workspace id or path")
	}
	if len(args) > 3 {
		return fmt.Errorf("files put: unexpected argument %q", args[3])
	}
	var data []byte
	var err error
	if len(args) == 3 {
		data, err = os.ReadFile(args[2])
		if err != nil {
			return fmt.Errorf("files put: read %s: %w", args[2], err)
		}
	} else {
		data, err = io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("files put: read stdin: %w", err)
		}
	}
	if err := c.WriteFile(args[0], args[1], data); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s\n", args[1])
	return nil
}

// filesMkdir implements "files mkdir <workspace-id> <path>".
func filesMkdir(c *Client, args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("files mkdir: missing workspace id or path")
	}
	if len(args) > 2 {
		return fmt.Errorf("files mkdir: unexpected argument %q", args[2])
	}
	if err := c.Mkdir(args[0], args[1]); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created %s\n", args[1])
	return nil
}

// filesRm implements "files rm <workspace-id> <path>".
func filesRm(c *Client, args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("files rm: missing workspace id or path")
	}
	if len(args) > 2 {
		return fmt.Errorf("files rm: unexpected argument %q", args[2])
	}
	if err := c.DeleteFile(args[0], args[1]); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "deleted %s\n", args[1])
	return nil
}

// printFileEntry prints one file entry as a tab-separated row.
func printFileEntry(w io.Writer, e types.FileEntry) {
	kind := "file"
	if e.IsDir {
		kind = "dir"
	}
	fmt.Fprintf(w, "%s\t%s\t%d\n", e.Path, kind, e.Size)
}
