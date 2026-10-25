package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func ownedPulseFile(fi os.FileInfo, kind os.FileMode) bool {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && stat.Nlink == 1 && fi.Mode().Type() == kind && fi.Mode().Perm() == 0600
}

// A persistent empty lock serializes cooperating owners through stale recovery and cleanup.
func lockPulseDirectory(dir string) (*os.File, error) {
	path := filepath.Join(dir, "live.lock")
	if fi, err := os.Lstat(path); err == nil {
		if !ownedPulseFile(fi, 0) || fi.Size() != 0 {
			return nil, errors.New("unexpected communication lock file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	valid := false
	defer func() {
		if !valid {
			f.Close()
		}
	}()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !ownedPulseFile(fi, 0) || fi.Size() != 0 || !os.SameFile(fi, current) {
		return nil, errors.New("communication lock changed or is not private")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("communication bridge is already owned by another instance")
	}
	current, err = os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(fi, current) || !ownedPulseFile(current, 0) || current.Size() != 0 {
		return nil, errors.New("communication lock changed during acquisition")
	}
	valid = true
	return f, nil
}

// Production callers hold live.lock until identity-checked removal completes.
func removePulseSocket(path string, inode os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if inode == nil || !ownedPulseFile(current, os.ModeSocket) || !os.SameFile(inode, current) {
		return errors.New("communication socket was replaced; leaving it untouched")
	}
	return os.Remove(path)
}

// Caller holds live.lock across stale probing, removal, and binding.
func listenPulseSocket(path string) (*net.UnixListener, os.FileInfo, error) {
	if fi, err := os.Lstat(path); err == nil {
		if !ownedPulseFile(fi, os.ModeSocket) {
			return nil, nil, errors.New("unexpected communication socket path")
		}
		c, probeErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if probeErr == nil {
			c.Close()
			return nil, nil, errors.New("communication socket has a live listener")
		}
		if !errors.Is(probeErr, syscall.ECONNREFUSED) {
			return nil, nil, probeErr
		}
		if err = removePulseSocket(path, fi); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	// Go's default unconditional unlink-on-close could remove a replacement owner's path.
	ln.SetUnlinkOnClose(false)
	inode, err := os.Lstat(path)
	if err == nil {
		stat, ok := inode.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 || inode.Mode().Type() != os.ModeSocket {
			err = errors.New("communication socket changed during creation")
		}
	}
	if err == nil {
		err = os.Chmod(path, 0600)
	}
	if err == nil {
		current, e := os.Lstat(path)
		if e != nil {
			err = e
		} else if !ownedPulseFile(current, os.ModeSocket) || !os.SameFile(inode, current) {
			err = errors.New("communication socket changed during setup")
		}
	}
	if err != nil {
		ln.Close()
		// A failed chmod may leave the initial socket mode; do not touch a replacement.
		if current, e := os.Lstat(path); e == nil && inode != nil && current.Mode().Type() == os.ModeSocket && os.SameFile(inode, current) {
			if stat, ok := current.Sys().(*syscall.Stat_t); ok && stat.Uid == uint32(os.Getuid()) && stat.Nlink == 1 {
				_ = os.Remove(path)
			}
		}
		return nil, nil, err
	}
	return ln, inode, nil
}
