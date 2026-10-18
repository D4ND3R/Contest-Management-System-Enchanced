// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/base_loader.py and __init__.py of CMS:
// Copyright © 2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2014-2015 William Di Luigi <williamdiluigi@gmail.com>

package importer

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
)

// Kind is what a loader reads from a directory.
type Kind string

// The kinds of packages.
const (
	KindContest Kind = "contest"
	KindTask    Kind = "task"
	KindUsers   Kind = "users"
	KindTeams   Kind = "teams"
)

// Loader reads one package format (upstream BaseLoader). A loader
// implements one or more of ContestLoader, TaskLoader, UserLoader and
// TeamLoader; it must be stateless (safe for concurrent use): everything
// about one load travels in the arguments, and every load returns a new
// model (the importer changes it: a task renamed with -n, the files hashed
// by the writer).
type Loader interface {
	// Name is the short name given with -L ("italy_yaml").
	Name() string
	// Description is shown in the help ("Italian YAML-based format").
	Description() string
	// Detect reports whether dir looks like a package of this format of
	// that kind (upstream detect). Autodetection asks every loader that
	// implements the kind and needs exactly one to answer true; a loader
	// that does not want to be autodetected answers false.
	Detect(dir string, kind Kind) bool
}

// Expecter is implemented by the loaders that can say what Detect looks
// for, for the message of a failed autodetection.
type Expecter interface {
	// Expects describes the files that make a directory a package of
	// that kind ("contest.yaml"), or "" for a kind the loader does not
	// detect.
	Expects(kind Kind) string
}

// ContestLoader reads contests (upstream ContestLoader).
type ContestLoader interface {
	Loader
	// LoadContest reads the contest at dir: its columns and groups, the
	// names of its tasks in order and its participations (upstream
	// get_contest), plus the users and teams the package describes.
	LoadContest(dir string, opts *Options) (*Contest, error)
	// TaskLoader returns the loader and the directory of the task called
	// name of the contest at dir (upstream get_task_loader).
	TaskLoader(dir, name string) (TaskLoader, string)
}

// TaskLoader reads tasks (upstream TaskLoader).
type TaskLoader interface {
	Loader
	// LoadTask reads the task at dir (upstream get_task), with its
	// statements unless opts.SkipStatements.
	LoadTask(dir string, opts *Options) (*Task, error)
}

// UserLoader reads users (upstream UserLoader; the commands read every
// user of the package, as cmsImportUser --all).
type UserLoader interface {
	Loader
	LoadUsers(dir string, opts *Options) ([]User, error)
}

// TeamLoader reads teams (upstream TeamLoader, as cmsImportTeam --all).
type TeamLoader interface {
	Loader
	LoadTeams(dir string, opts *Options) ([]Team, error)
}

// Options are what a load needs besides the directory.
type Options struct {
	// Languages are the programming languages of the installation (the
	// extensions of graders and headers). Never nil when the importer
	// calls a loader.
	Languages *language.Registry
	// SkipStatements: do not read the statements (upstream
	// get_statement=False, the -S flag).
	SkipStatements bool
	// Report receives what the loader finds wrong but can live with
	// (upstream logger.warning), and errors that do not stop the load.
	// Never nil when the importer calls a loader. A loader returns an
	// error for what it cannot read at all (a missing task.yaml).
	Report *Report
}

// Supports reports whether l reads packages of that kind.
func Supports(l Loader, kind Kind) bool {
	switch kind {
	case KindContest:
		_, ok := l.(ContestLoader)
		return ok
	case KindTask:
		_, ok := l.(TaskLoader)
		return ok
	case KindUsers:
		_, ok := l.(UserLoader)
		return ok
	case KindTeams:
		_, ok := l.(TeamLoader)
		return ok
	}
	return false
}

// Errors of the registry.
var (
	// ErrUnknownLoader: no loader has the name given with -L.
	ErrUnknownLoader = errors.New("the specified loader does not exist")
	// ErrDetect: autodetection found no loader, or more than one.
	ErrDetect = errors.New("couldn't autodetect the loader, please specify it with -L")
)

// Registry is a set of loaders by name (upstream LOADERS). The format
// packages register theirs in Default from an init function.
type Registry struct {
	mu      sync.RWMutex
	loaders map[string]Loader
	aliases map[string]string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{loaders: map[string]Loader{}, aliases: map[string]string{}}
}

// Default is the registry of the commands and the administration.
var Default = NewRegistry()

// Register adds a loader to Default; it panics on a name already taken
// (call it from init).
func Register(l Loader, aliases ...string) {
	if err := Default.Register(l, aliases...); err != nil {
		panic(err)
	}
}

// Register adds a loader, also known by aliases (e.g. the upstream names
// polygon_task and polygon_contest of one Go loader).
func (r *Registry) Register(l Loader, aliases ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range append([]string{l.Name()}, aliases...) {
		if n == "" {
			return errors.New("importer: a loader needs a name")
		}
		if _, ok := r.loaders[n]; ok {
			return fmt.Errorf("importer: loader %q registered twice", n)
		}
		if _, ok := r.aliases[n]; ok {
			return fmt.Errorf("importer: loader %q registered twice", n)
		}
	}
	r.loaders[l.Name()] = l
	for _, a := range aliases {
		r.aliases[a] = l.Name()
	}
	return nil
}

// Lookup returns the loader with that name or alias.
func (r *Registry) Lookup(name string) (Loader, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if canon, ok := r.aliases[name]; ok {
		name = canon
	}
	l, ok := r.loaders[name]
	return l, ok
}

// Loaders returns the loaders sorted by name.
func (r *Registry) Loaders() []Loader {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Loader, 0, len(r.loaders))
	for _, l := range r.loaders {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Aliases returns the other names of a loader, sorted.
func (r *Registry) Aliases(name string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for a, canon := range r.aliases {
		if canon == name {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

// Choose returns the loader to read dir as kind (upstream choose_loader):
// the loader called name, which must support the kind, or with an empty
// name the only loader supporting the kind that detects dir.
func (r *Registry) Choose(kind Kind, name, dir string) (Loader, error) {
	if name != "" {
		l, ok := r.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("%w: %q (known: %s)", ErrUnknownLoader, name, r.names(""))
		}
		if !Supports(l, kind) {
			return nil, fmt.Errorf("loader %q cannot read a %s (loaders of %ss: %s)", name, kind, kind, r.names(kind))
		}
		return l, nil
	}
	var found []Loader
	for _, l := range r.Loaders() {
		if Supports(l, kind) && l.Detect(dir, kind) {
			found = append(found, l)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("%w: no loader of %ss accepted %s (%s)", ErrDetect, kind, dir, r.expected(kind))
	}
	names := make([]string, len(found))
	for i, l := range found {
		names[i] = l.Name()
	}
	return nil, fmt.Errorf("%w: more than one loader accepted %s (%s)", ErrDetect, dir, strings.Join(names, ", "))
}

// expected says what each loader of kind looks for: "loaders of
// contests: italy_yaml wants contest.yaml; polygon wants contest.xml and
// problems/".
func (r *Registry) expected(kind Kind) string {
	var out []string
	for _, l := range r.Loaders() {
		if !Supports(l, kind) {
			continue
		}
		what := ""
		if e, ok := l.(Expecter); ok {
			what = e.Expects(kind)
		}
		if what == "" {
			out = append(out, l.Name())
		} else {
			out = append(out, l.Name()+" wants "+what)
		}
	}
	if len(out) == 0 {
		return fmt.Sprintf("loaders of %ss: none", kind)
	}
	return fmt.Sprintf("loaders of %ss: %s", kind, strings.Join(out, "; "))
}

// names lists the loaders supporting kind ("" = all).
func (r *Registry) names(kind Kind) string {
	var out []string
	for _, l := range r.Loaders() {
		if kind == "" || Supports(l, kind) {
			out = append(out, l.Name())
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// ChooseContest, ChooseTask, ChooseUsers and ChooseTeams are Choose with
// the loader typed for the kind.
func (r *Registry) ChooseContest(name, dir string) (ContestLoader, error) {
	l, err := r.Choose(KindContest, name, dir)
	if err != nil {
		return nil, err
	}
	return l.(ContestLoader), nil
}

func (r *Registry) ChooseTask(name, dir string) (TaskLoader, error) {
	l, err := r.Choose(KindTask, name, dir)
	if err != nil {
		return nil, err
	}
	return l.(TaskLoader), nil
}

func (r *Registry) ChooseUsers(name, dir string) (UserLoader, error) {
	l, err := r.Choose(KindUsers, name, dir)
	if err != nil {
		return nil, err
	}
	return l.(UserLoader), nil
}

func (r *Registry) ChooseTeams(name, dir string) (TeamLoader, error) {
	l, err := r.Choose(KindTeams, name, dir)
	if err != nil {
		return nil, err
	}
	return l.(TeamLoader), nil
}

// Help lists the loaders for the help of the commands (upstream
// build_epilog).
func (r *Registry) Help() string {
	var b strings.Builder
	b.WriteString("The following loaders are supported:\n")
	ls := r.Loaders()
	if len(ls) == 0 {
		b.WriteString(" (none)\n")
	}
	for _, l := range ls {
		var kinds []string
		for _, k := range []Kind{KindContest, KindTask, KindUsers, KindTeams} {
			if Supports(l, k) {
				kinds = append(kinds, string(k))
			}
		}
		fmt.Fprintf(&b, " * %s (%s; %s)", l.Name(), l.Description(), strings.Join(kinds, ", "))
		if a := r.Aliases(l.Name()); len(a) > 0 {
			fmt.Fprintf(&b, ", also -L %s", strings.Join(a, ", -L "))
		}
		b.WriteString("\n")
	}
	return b.String()
}
