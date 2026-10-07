// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package cli is the small command tree of the cms binary: one subcommand
// per service plus `ctl` (administration) and `migrate`.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Env is what commands receive besides their arguments.
type Env struct {
	Stdout, Stderr io.Writer
	// ConfigPath is the --config flag (empty: $CMS_CONFIG or default paths).
	ConfigPath string
	// Prog is the name the user typed ("cms" or "cmsctl").
	Prog string
}

// Command is a node of the command tree.
type Command struct {
	Name  string
	Args  string // synopsis of the arguments, for the help
	Short string
	Long  string
	// Flags, if set, parses the arguments before Run.
	Flags func(fs *flag.FlagSet)
	Run   func(ctx context.Context, env *Env, args []string) error
	Sub   []*Command
}

// ErrUsage makes Execute print the help of the command and exit with 2.
var ErrUsage = errors.New("usage error")

// UsageError wraps a message as a usage error.
func UsageError(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUsage, fmt.Sprintf(format, a...))
}

// Execute runs the command addressed by args. path is the chain of command
// names used for help messages.
func (c *Command) Execute(ctx context.Context, env *Env, path string, args []string) error {
	// A command with subcommands and its own Run runs it when called alone.
	if len(c.Sub) > 0 && !(len(args) == 0 && c.Run != nil) {
		if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
			if len(args) > 1 && args[0] == "help" {
				if sub := c.find(args[1]); sub != nil {
					sub.help(env.Stdout, path+" "+sub.Name)
					return nil
				}
			}
			c.help(env.Stdout, path)
			if len(args) == 0 {
				return ErrUsage
			}
			return nil
		}
		sub := c.find(args[0])
		if sub == nil {
			fmt.Fprintf(env.Stderr, "%s: unknown command %q\n\n", path, args[0])
			c.help(env.Stderr, path)
			return ErrUsage
		}
		return sub.Execute(ctx, env, path+" "+sub.Name, args[1:])
	}
	if c.Flags != nil {
		fs := flag.NewFlagSet(path, flag.ContinueOnError)
		fs.SetOutput(env.Stderr)
		c.Flags(fs)
		fs.Usage = func() { c.help(env.Stderr, path) }
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return ErrUsage
		}
		args = fs.Args()
	} else if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		c.help(env.Stdout, path)
		return nil
	}
	err := c.Run(ctx, env, args)
	if errors.Is(err, ErrUsage) {
		fmt.Fprintf(env.Stderr, "%s: %v\n\n", path, err)
		c.help(env.Stderr, path)
	}
	return err
}

func (c *Command) find(name string) *Command {
	for _, s := range c.Sub {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (c *Command) help(w io.Writer, path string) {
	fmt.Fprintf(w, "Usage: %s", path)
	if len(c.Sub) > 0 {
		fmt.Fprint(w, " <command> [arguments]")
	} else if c.Args != "" {
		fmt.Fprint(w, " "+c.Args)
	}
	fmt.Fprintln(w)
	if c.Long != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimSpace(c.Long))
	} else if c.Short != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, c.Short)
	}
	if c.Flags != nil {
		fs := flag.NewFlagSet(path, flag.ContinueOnError)
		c.Flags(fs)
		fmt.Fprintln(w, "\nFlags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
	if len(c.Sub) > 0 {
		fmt.Fprintln(w, "\nCommands:")
		width := 0
		for _, s := range c.Sub {
			if len(s.Name) > width {
				width = len(s.Name)
			}
		}
		for _, s := range c.Sub {
			fmt.Fprintf(w, "  %-*s  %s\n", width, s.Name, s.Short)
		}
		fmt.Fprintf(w, "\nRun '%s help <command>' for details.\n", path)
	}
}
