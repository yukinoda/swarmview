package main

import (
	"errors"
	"net"
	"os"
	"syscall"
)

func pulsePeerPID(c net.Conn) (int, error) {
	raw, err := c.(*net.UnixConn).SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var opErr error
	err = raw.Control(func(fd uintptr) {
		cred, opErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err == nil {
		err = opErr
	}
	if err != nil {
		return 0, err
	}
	if cred == nil || cred.Uid != uint32(os.Getuid()) {
		return 0, errors.New("wrong socket owner")
	}
	return int(cred.Pid), nil
}
