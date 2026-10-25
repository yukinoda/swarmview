package main

import (
	"net"
	"syscall"
)

func pulsePeerPID(c net.Conn) (int, error) {
	raw, err := c.(*net.UnixConn).SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var opErr error
	err = raw.Control(func(fd uintptr) { pid, opErr = syscall.GetsockoptInt(int(fd), 0, 2) }) // XNU SOL_LOCAL / LOCAL_PEERPID
	if err == nil {
		err = opErr
	}
	return pid, err
}
