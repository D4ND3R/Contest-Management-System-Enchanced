// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package language holds the programming languages a contest can accept
// (upstream cms/grading/language.py, languagemanager.py and the classes of
// cms/grading/languages). Each language is a YAML file instead of a Python
// class: the built-in ones live in config/languages (embedded in the
// binary), and the files of the directory named by languages_dir in
// cms.yaml are added to them, replacing a built-in language of the same
// name.
//
// The name identifies a language everywhere (submissions, datasets,
// contests store it), so the built-in languages keep the exact names of
// CMS ("C++17 / g++", "Python 3 / PyPy"...).
//
// # File format
//
//	name: C++17 / g++                    # required, unique
//	source_extensions: [.cpp, .cc]       # required; the first is canonical
//	header_extensions: [.h]              # files the compilation may use
//	object_extensions: [.o]
//	executable_extension: ""             # appended to executable names
//	requires_multithreading: false       # every program of a contest with
//	                                     # this language may use 1000
//	                                     # threads and processes
//	large_address_space: false           # the runtime reserves much more
//	                                     # address space than it uses
//	interpreted: false                   # see below
//	compilation:
//	  commands:                          # run in order, in one box
//	    - [/usr/bin/g++, "{eval:-DEVAL}", -o, "{executable}", "{sources}"]
//	    - for_each: other_sources        # once per source but the first
//	      command: [/bin/ln, -s, "{source|base}", "{source|capitalize}"]
//	  dirs: [/var/lib/ghc]               # host directories visible (read-
//	                                     # only, globs allowed, skipped if
//	                                     # missing)
//	  env: [NAME=value]                  # environment of the compilers
//	  files: {.fpc.cfg: /etc/fpc.cfg}    # host files copied into the box
//	  file_locks: false                  # the compiler locks files
//	evaluation:
//	  commands:
//	    - ["./{executable}", "{args}"]
//	  dirs: []
//	  env: []
//	  processes: 0                       # threads the runtime needs at
//	                                     # least (0: one)
//
// Unknown keys are errors. Commands are argument lists, never shell lines
// (use /bin/sh -c explicitly when a shell is needed); the program is an
// absolute path (the box does not search PATH for it) or "./{executable}".
// Every argument may hold placeholders:
//
//   - {executable}: the name of the executable.
//   - Compilation only: {sources}, every source file, the one with the entry
//     point (the grader, if any) first; {main_source}, that first file;
//     {other_sources}, the others; {source}, the current file of a
//     for_each command.
//   - Evaluation only: {main}, the name of the entry point (the Java class:
//     "grader", or the executable name without extension); {args}, the
//     arguments of the program.
//   - {eval:TEXT}: the whole argument TEXT when compiling for an evaluation,
//     no argument when showing the commands to contestants (upstream
//     for_evaluation, used for -DEVAL).
//
// A list ({sources}, {other_sources}, {args}) must be the only placeholder
// of its argument: the argument is repeated for every item, with the text
// around the placeholder, and disappears for an empty list
// ("{other_sources|stem}.pyc"). Modifiers follow a "|": base (the file
// name without directories), stem (base without its extension), capitalize
// (first letter of the base in upper case) and quote (quoted for a POSIX
// shell, like Python's shlex.quote); several apply left to right
// ("{source|base|capitalize}"). "{{" and "}}" are literal braces.
//
// An interpreted language has no compilation commands: the compilation
// copies the main source to the executable name, or runs /bin/true when
// they are the same (upstream PHP).
//
// Directories, files and environment never come from the worker: boxes
// see only what the language asks for, so that a compiler error cannot
// show secrets (mounting all of /etc, as upstream does, would expose
// /etc/cms/cms.yaml to `#include "/etc/cms/cms.yaml"`; "/etc" itself is
// refused).
package language
