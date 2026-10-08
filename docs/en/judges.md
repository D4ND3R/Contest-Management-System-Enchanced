# Judges (workers)

*Español: [../es/jueces.md](../es/jueces.md).*

A **judge** is a `cms worker` process: it takes compilation and evaluation
jobs from the Valkey queue, runs them inside
[isolate](https://github.com/ioi/isolate) and sends the result back. It
never connects to PostgreSQL. A machine can run several (with different box
ranges), and there can be judges on many machines.

## Host requirements

- Linux with **cgroups v2** (Ubuntu 22.04+, Debian 12+). Without cgroups v2
  the memory of a program and all its threads cannot be limited, and the
  "Memory limit exceeded" verdict cannot be given.
- **isolate** 2.0 or later (tested with 2.7) with `isolate.service` running.
- The compilers of the languages in use.
- The worker runs as root (isolate switches to its own box users).

### Preparing a machine (Ubuntu 24.04)

```sh
# isolate, from its authors' repository
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://www.ucw.cz/isolate/debian/signing-key.asc | sudo tee /etc/apt/keyrings/isolate.asc >/dev/null
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/isolate.asc] http://www.ucw.cz/isolate/debian/ noble-isolate main" \
  | sudo tee /etc/apt/sources.list.d/isolate.list
sudo apt-get update
sudo apt-get install -y isolate
sudo systemctl enable --now isolate.service

# compilers of every built-in language
sudo apt-get install -y --no-install-recommends g++ gcc python3 pypy3 default-jdk-headless \
  fp-compiler ghc mono-mcs mono-runtime rustc golang-go php-cli kotlin

# Go: a prebuilt standard library (without it a compilation exceeds the
# 10 s CPU limit). Run it again after every Go upgrade.
sudo env CGO_ENABLED=0 GODEBUG=installgoroot=all /usr/bin/go install -pkgdir=/var/lib/cms/go-std std
```

After upgrading a compiler, restart the workers: the compilation cache
identifies each compiler by the hash of its binary, computed at startup.

### Checking the host

```sh
sudo cms ctl verify-host
```

It checks cgroups v2 and isolate's cgroup, isolate's version and
configuration, that the worker's box range exists and no other process uses
it, seccomp, hard link protection, swap, SMT and CPU frequency, and runs a
real program in a box (also under the seccomp filter).

- **FAIL**: the judge is unreliable or does not work; fix it before the
  contest. The command exits with an error.
- **WARN**: it works, but measured times will be noisier. For a contest:
  turn SMT off (`echo off > /sys/devices/system/cpu/smt/control`), fix the
  frequency (`performance` governor) and turn swap off (`swapoff -a`).
  `isolate-check-environment --execute` applies several of these.

## Configuration

The `worker` section of `cms.yaml` (or `CMS_WORKER_*` variables):

| Setting | What it does |
|---|---|
| `name` | name on the judges page; empty = the host name. One process per name |
| `concurrency` | jobs in parallel: at most one per **physical** core reserved for judging |
| `box_id_base`, `box_count` | isolate boxes `[base, base + count)`; workers on the same machine need disjoint ranges |
| `work_dir` | directory of the boxes (made absolute) |
| `seccomp` | seccomp filter for contestant programs (recommended) |
| `shutdown_timeout` | on shutdown, how long running jobs may finish; the others go back to the queue |
| `job_timeout` | bound for a job stuck because of a fault (the sandbox enforces the real limits) |
| `compilation_cache_ttl` | how long a successful compilation is remembered; 0 disables it |

`sandbox.cgroups: false` is only for development machines without cgroups
v2: memory is limited per process as address space, and exceeding it looks
like a runtime error.

A judge on another machine needs neither `secret_key` nor database access:
set `storage.backend: remote` with the URL of the dispatcher's blob server
and the token printed by `cms ctl blob-token` (see
[Configuration](configuration.md)).

## With Docker

The worker image (`deploy/docker/Dockerfile.worker`) ships isolate, the
compilers and the Go standard library:

```sh
make build
docker build -f deploy/docker/Dockerfile.worker -t cms-worker .
docker run --privileged --cgroupns=private --env-file cms.env cms-worker worker
```

It needs a privileged container with its own cgroup namespace; its entry
point does inside the container what `isolate.service` does on the host.
`docker run … cms-worker ctl verify-host` checks the container.

## Security

- Contestant programs run under a seccomp filter: `ptrace`, `mount`,
  namespaces, `bpf`, `keyctl`, `io_uring`, module loading and other calls
  kill the program with **Security violation**; network sockets fail (the
  box has no network).
- Boxes never get the worker's environment (it may hold secrets), and
  compilers do not see `/etc`: each language declares the host directories
  it needs.
- Compilers, checkers and managers run in boxes, without the seccomp filter.

## Languages

Built in (with CMS's exact names): C11 / gcc, C++11/14/17/20 / g++,
C# / Mono, Haskell / ghc, Java / JDK, Pascal / fpc, PHP, Python 3 / CPython,
Python 3 / PyPy, Rust, plus Go and Kotlin / JDK.

Each language is a YAML file (`config/languages/*.yaml`, embedded in the
binary). To add one or change the flags of an existing one, put files in
the `languages_dir` directory: a file with the same `name` replaces the
built-in one. The format is described in `internal/grading/language/doc.go`;
an example:

```yaml
name: C++17 / g++
source_extensions: [.cpp, .cc, .cxx, .c++, .C]
header_extensions: [.h]
object_extensions: [.o]
compilation:
  commands:
    - [/usr/bin/g++, "{eval:-DEVAL}", -std=gnu++17, -O2, -pipe, -static, -s, -o, "{executable}", "{sources}"]
evaluation:
  commands:
    - ["./{executable}", "{args}"]
```

The name identifies the language in submissions and contests: do not change
it once there are submissions.
