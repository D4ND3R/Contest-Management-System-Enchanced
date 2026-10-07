// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/language.py of CMS:
// Copyright © 2016-2017 Stefano Maggiolo <s.maggiolo@gmail.com>

package language

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// MaxNameLen bounds language names (the judge protocol's limit).
const MaxNameLen = 255

// Language is a programming language (upstream cms.grading.Language). It
// is immutable once loaded: share it freely.
type Language struct {
	// Name identifies the language ("C++17 / g++").
	Name string `yaml:"name" json:"name"`
	// SourceExtensions are the extensions of sources (with the dot); the
	// first is the canonical one, used for ".%l" in file names.
	SourceExtensions []string `yaml:"source_extensions" json:"source_extensions"`
	// HeaderExtensions and ObjectExtensions mark the other files a
	// compilation uses (headers of graders, precompiled objects).
	HeaderExtensions []string `yaml:"header_extensions" json:"header_extensions"`
	ObjectExtensions []string `yaml:"object_extensions" json:"object_extensions"`
	// ExecutableExtension is appended to the names of executables.
	ExecutableExtension string `yaml:"executable_extension" json:"executable_extension"`
	// RequiresMultithreading: when a contest accepts this language, every
	// program of the contest may start many threads and processes
	// (upstream requires_multithreading).
	RequiresMultithreading bool `yaml:"requires_multithreading" json:"requires_multithreading"`
	// LargeAddressSpace: the runtime reserves far more address space than
	// it uses (JVM, Mono, Go). Only matters without cgroups (development):
	// memory is then limited as address space, so these compilers and
	// programs run without a memory limit.
	LargeAddressSpace bool `yaml:"large_address_space" json:"large_address_space"`
	// Interpreted languages have no compilation commands: the executable
	// is a copy of the main source.
	Interpreted bool `yaml:"interpreted" json:"interpreted"`

	Compilation Compilation `yaml:"compilation" json:"compilation"`
	Evaluation  Evaluation  `yaml:"evaluation" json:"evaluation"`

	// File is where the language was loaded from (for messages).
	File string `yaml:"-" json:"-"`

	compile []command
	eval    []command
	digest  string
}

// Compilation describes how sources become an executable.
type Compilation struct {
	// Commands run in order in the same box.
	Commands []Command `yaml:"commands" json:"commands"`
	// Dirs are host directories the compilers need, mounted read-only at
	// the same path (glob patterns allowed; missing ones are skipped).
	Dirs []string `yaml:"dirs" json:"dirs"`
	// Env holds NAME=VALUE pairs for the compilers.
	Env []string `yaml:"env" json:"env"`
	// Files are host files copied into the home of the box before
	// compiling: box file name -> host path (skipped when missing).
	Files map[string]string `yaml:"files" json:"files"`
	// FileLocks lets the compilers lock files (isolate forbids it by
	// default).
	FileLocks bool `yaml:"file_locks" json:"file_locks"`
}

// Evaluation describes how an executable runs.
type Evaluation struct {
	Commands []Command `yaml:"commands" json:"commands"`
	Dirs     []string  `yaml:"dirs" json:"dirs"`
	Env      []string  `yaml:"env" json:"env"`
	// Processes is the number of threads the runtime needs at least
	// (0 means one; the GHC runtime needs two).
	Processes int `yaml:"processes" json:"processes"`
}

// Command is a command template: an argument list, run once or (ForEach)
// once per item of a list.
type Command struct {
	// ForEach is "" or "other_sources".
	ForEach string   `yaml:"for_each" json:"for_each,omitempty"`
	Args    []string `yaml:"command" json:"command"`
}

// UnmarshalYAML accepts a plain argument list or a {for_each, command}
// mapping.
func (c *Command) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		*c = Command{}
		return n.Decode(&c.Args)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i].Value; k != "for_each" && k != "command" {
				return fmt.Errorf("line %d: unknown key %q in a command (for_each, command)", n.Content[i].Line, k)
			}
		}
		type plain Command
		var p plain
		if err := n.Decode(&p); err != nil {
			return err
		}
		*c = Command(p)
		return nil
	}
	return fmt.Errorf("line %d: a command is a list of arguments or a {for_each, command} mapping", n.Line)
}

// command is a parsed Command.
type command struct {
	forEach string
	args    []argTemplate
}

// Parse reads a language definition. file names it in errors.
func Parse(data []byte, file string) (*Language, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	l := &Language{}
	if err := dec.Decode(l); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	l.File = file
	if err := l.init(); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return l, nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// init validates the definition, parses the command templates and
// computes the digest.
func (l *Language) init() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if l.Name == "" || len(l.Name) > MaxNameLen || !utf8.ValidString(l.Name) ||
		strings.TrimSpace(l.Name) != l.Name || strings.IndexFunc(l.Name, unicode.IsControl) >= 0 {
		bad("invalid name %q (non-empty, at most %d bytes, no control characters or surrounding spaces)", l.Name, MaxNameLen)
	}
	if len(l.SourceExtensions) == 0 {
		bad("source_extensions is empty")
	}
	for _, list := range []struct {
		key  string
		exts []string
	}{{"source_extensions", l.SourceExtensions}, {"header_extensions", l.HeaderExtensions}, {"object_extensions", l.ObjectExtensions}} {
		for _, e := range list.exts {
			if !validExtension(e) {
				bad("%s: invalid extension %q", list.key, e)
			}
		}
	}
	if l.ExecutableExtension != "" && !validExtension(l.ExecutableExtension) {
		bad("invalid executable_extension %q", l.ExecutableExtension)
	}

	c := &l.Compilation
	switch {
	case l.Interpreted && len(c.Commands) > 0:
		bad("an interpreted language has no compilation commands (the main source is copied)")
	case !l.Interpreted && len(c.Commands) == 0:
		bad("compilation.commands is empty")
	}
	l.compile = l.compile[:0]
	for i, cmd := range c.Commands {
		sc := scopeCompile
		switch cmd.ForEach {
		case "":
		case phOtherSources:
			sc = scopeEachSource
		default:
			bad("compilation command %d: for_each must be %q", i+1, phOtherSources)
			continue
		}
		pc, err := parseCommand(cmd.Args, sc)
		if err != nil {
			bad("compilation command %d: %w", i+1, err)
			continue
		}
		pc.forEach = cmd.ForEach
		l.compile = append(l.compile, pc)
	}
	if len(l.Evaluation.Commands) == 0 {
		bad("evaluation.commands is empty")
	}
	l.eval = l.eval[:0]
	for i, cmd := range l.Evaluation.Commands {
		if cmd.ForEach != "" {
			bad("evaluation command %d: for_each is only for compilation commands", i+1)
			continue
		}
		pc, err := parseCommand(cmd.Args, scopeEvaluate)
		if err != nil {
			bad("evaluation command %d: %w", i+1, err)
			continue
		}
		l.eval = append(l.eval, pc)
	}
	if l.Evaluation.Processes < 0 {
		bad("evaluation.processes must not be negative")
	}

	for _, d := range []struct {
		key  string
		dirs []string
	}{{"compilation.dirs", c.Dirs}, {"evaluation.dirs", l.Evaluation.Dirs}} {
		for _, dir := range d.dirs {
			if err := checkDir(dir); err != nil {
				bad("%s: %w", d.key, err)
			}
		}
	}
	for _, e := range []struct {
		key string
		env []string
	}{{"compilation.env", c.Env}, {"evaluation.env", l.Evaluation.Env}} {
		for _, kv := range e.env {
			name, _, ok := strings.Cut(kv, "=")
			if !ok || !envName.MatchString(name) || strings.ContainsRune(kv, 0) {
				bad("%s: %q is not NAME=VALUE", e.key, kv)
			}
		}
	}
	for name, host := range c.Files {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
			bad("compilation.files: %q must be a plain file name", name)
		}
		if !filepath.IsAbs(host) || filepath.Clean(host) != host || strings.ContainsRune(host, 0) {
			bad("compilation.files: %q must be an absolute, clean host path", host)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	// The digest covers everything that changes what a compilation or
	// an evaluation does (compilation caches key on it).
	canon, err := json.Marshal(l)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canon)
	l.digest = hex.EncodeToString(sum[:])
	return nil
}

func parseCommand(args []string, sc scope) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New("empty command")
	}
	// The box does not search PATH for the program, and the program must
	// not depend on what contestants name their files.
	if prog := args[0]; prog != "./{executable}" && (!strings.HasPrefix(prog, "/") || strings.ContainsAny(prog, "{}")) {
		return command{}, fmt.Errorf("the program %q must be an absolute path without placeholders, or \"./{executable}\"", prog)
	}
	pc := command{args: make([]argTemplate, 0, len(args))}
	for _, a := range args {
		t, err := parseArg(a, sc)
		if err != nil {
			return command{}, err
		}
		pc.args = append(pc.args, t)
	}
	return pc, nil
}

func validExtension(e string) bool {
	return e != "" && utf8.ValidString(e) && !strings.ContainsAny(e, "/\x00%{}") && strings.TrimSpace(e) == e
}

// checkDir validates a directory (or glob pattern) to mount in a box.
func checkDir(d string) error {
	switch {
	case !path.IsAbs(d) || path.Clean(d) != d:
		return fmt.Errorf("%q must be an absolute, clean path", d)
	case strings.ContainsAny(d, ":=\x00"):
		return fmt.Errorf("%q must not contain ':' or '=' (isolate separators)", d)
	case d == "/":
		return errors.New(`"/" cannot be mounted`)
	case d == "/etc":
		return errors.New(`mounting all of "/etc" would show the configuration of CMS (secret key, database ` +
			`password) to compilers: mount the subdirectories the compiler needs`)
	}
	if _, err := filepath.Match(d, ""); err != nil {
		return fmt.Errorf("%q: %w", d, err)
	}
	return nil
}

// String returns the name.
func (l *Language) String() string { return l.Name }

// Digest is a SHA-256 of the definition (hex): two languages with the same
// digest compile and run programs the same way.
func (l *Language) Digest() string { return l.digest }

// SourceExtension is the canonical source extension (the first).
func (l *Language) SourceExtension() string { return l.SourceExtensions[0] }

// HeaderExtension is the canonical header extension, or "".
func (l *Language) HeaderExtension() string { return first(l.HeaderExtensions) }

// ObjectExtension is the canonical object extension, or "".
func (l *Language) ObjectExtension() string { return first(l.ObjectExtensions) }

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// IsCompilationFile reports whether a manager with this name takes part
// in compilations in this language: a source, header or object of the
// language (upstream is_manager_for_compilation).
func (l *Language) IsCompilationFile(filename string) bool {
	for _, list := range [][]string{l.SourceExtensions, l.HeaderExtensions, l.ObjectExtensions} {
		for _, e := range list {
			if strings.HasSuffix(filename, e) {
				return true
			}
		}
	}
	return false
}

// CompilationCommands returns the commands that compile sources (the one
// with the entry point first) into executable (upstream
// get_compilation_commands). forEvaluation is false only to show the
// commands to contestants: it leaves out {eval:...} arguments (-DEVAL).
func (l *Language) CompilationCommands(sources []string, executable string, forEvaluation bool) ([][]string, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("language %s: nothing to compile", l.Name)
	}
	if l.Interpreted {
		if sources[0] != executable {
			return [][]string{{"/bin/cp", sources[0], executable}}, nil
		}
		// At least one command, to have execution statistics.
		return [][]string{{"/bin/true"}}, nil
	}
	v := values{
		scalars: map[string]string{
			phExecutable: executable,
			phMainSource: sources[0],
		},
		lists: map[string][]string{
			phSources:      sources,
			phOtherSources: sources[1:],
		},
		forEvaluation: forEvaluation,
	}
	var out [][]string
	for i := range l.compile {
		c := &l.compile[i]
		if c.forEach == "" {
			out = append(out, c.expand(&v))
			continue
		}
		for _, item := range v.lists[c.forEach] {
			v.scalars[phSource] = item
			out = append(out, c.expand(&v))
		}
		delete(v.scalars, phSource)
	}
	return out, nil
}

// EvaluationCommands returns the commands that run executable with args
// (upstream get_evaluation_commands). main names the entry point (the
// Java class); "" means the executable name without its extension, what
// Batch uses without a grader.
func (l *Language) EvaluationCommands(executable, main string, args []string) [][]string {
	if main == "" {
		main = stem(executable)
	}
	v := values{
		scalars: map[string]string{phExecutable: executable, phMain: main},
		lists:   map[string][]string{phArgs: args},
	}
	out := make([][]string, 0, len(l.eval))
	for i := range l.eval {
		out = append(out, l.eval[i].expand(&v))
	}
	return out
}

func (c *command) expand(v *values) []string {
	argv := make([]string, 0, len(c.args)+len(v.lists[phSources]))
	for i := range c.args {
		argv = c.args[i].expand(argv, v)
	}
	return argv
}
