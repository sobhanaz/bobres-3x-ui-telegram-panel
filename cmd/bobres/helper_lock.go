package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// installLockName is the lock file in the install directory (root's, mode 0600).
const installLockName = ".update.lock"

// errInstallBusy says another update, rollback or restore holds the install's lock.
var errInstallBusy = errors.New("another bobres update, rollback or restore is running on this install")

// lockInstall takes the lock that one update, rollback or restore at a time holds on the install in
// dir, for as long as the returned function has not been called (or the process lives: the system
// drops it when the process ends, however it ends). It does not wait: errInstallBusy says someone
// else holds it. The dashboard's helper takes the same lock as `bobres update`, `bobres rollback` and
// `bobres restore`, so none of them runs under another. Whoever holds the lock knows no update is
// running, so a status that says "running" is one that was never finished (clearStaleStatus).
func lockInstall(dir string, now time.Time) (release func(), err error) {
	path := filepath.Join(dir, installLockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // the lock file of this install, root's
	if errors.Is(err, os.ErrPermission) {
		return nil, fmt.Errorf("cannot open the lock file %s: %w (run bobres as root: sudo bobres ...)", path, err)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot open the lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errInstallBusy
		}
		return nil, fmt.Errorf("cannot lock %s: %w", path, err)
	}
	clearStaleStatus(dir, now)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// lock is lockInstall at this ops's time.
func (o *ops) lock(dir string) (func(), error) { return lockInstall(dir, o.clock()) }
