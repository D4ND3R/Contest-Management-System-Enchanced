// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// pool hands out the box ids [base, base+count). Inside the process a
// map says which ids are taken; across processes every taken id holds an
// exclusive flock on LockDir/<id>.lock, so two workers whose ranges
// overlap by mistake skip each other's boxes instead of corrupting them
// (and `cms ctl verify-host` can see which ids are in use). The lock dies
// with the process, so a crashed worker never leaves ids taken.
//
// Ids are assigned round robin, like CMS, so that a box id is not reused
// immediately (its cgroup and files are gone by then, but logs are
// easier to read).
type pool struct {
	base, count int
	lockDir     string

	mu   sync.Mutex
	next int
	held map[int]*os.File
	wake chan struct{} // closed and replaced on every release
}

// retryBusy is how often a full pool looks again for ids released by
// other processes (in-process releases wake the waiters at once).
const retryBusy = 200 * time.Millisecond

func newPool(base, count int, lockDir string) *pool {
	return &pool{base: base, count: count, lockDir: lockDir, held: map[int]*os.File{}, wake: make(chan struct{})}
}

// BoxLockPath is the lock file of a box id in lockDir.
func BoxLockPath(lockDir string, id int) string {
	return filepath.Join(lockDir, strconv.Itoa(id)+".lock")
}

// acquire returns a free box id, waiting until one is released.
func (p *pool) acquire(ctx context.Context) (int, error) {
	for {
		p.mu.Lock()
		for i := 0; i < p.count; i++ {
			id := p.base + (p.next+i)%p.count
			if p.held[id] != nil {
				continue
			}
			f, err := tryLockBox(p.lockDir, id)
			if err != nil {
				p.mu.Unlock()
				return 0, err
			}
			if f == nil {
				continue // used by another process
			}
			p.held[id] = f
			p.next = (p.next + i + 1) % p.count
			p.mu.Unlock()
			return id, nil
		}
		wake := p.wake
		p.mu.Unlock()
		t := time.NewTimer(retryBusy)
		select {
		case <-ctx.Done():
			t.Stop()
			return 0, fmt.Errorf("waiting for a free box: %w", context.Cause(ctx))
		case <-wake:
		case <-t.C:
		}
		t.Stop()
	}
}

// release gives an id back.
func (p *pool) release(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f := p.held[id]; f != nil {
		f.Close() // drops the flock
		delete(p.held, id)
		close(p.wake)
		p.wake = make(chan struct{})
	}
}

// tryLockBox takes the lock of a box id without waiting. It returns nil
// (and no error) when another process holds it.
func tryLockBox(lockDir string, id int) (*os.File, error) {
	f, err := os.OpenFile(BoxLockPath(lockDir, id), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("box %d lock: %w", id, err)
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, fmt.Errorf("box %d lock: %w", id, err)
	}
	return f, nil
}

// BoxInUse reports whether a running process (a worker) holds the lock
// of a box id. It is what `cms ctl verify-host` uses.
func BoxInUse(lockDir string, id int) (bool, error) {
	f, err := os.Open(BoxLockPath(lockDir, id))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil // closing f drops the shared lock
}
