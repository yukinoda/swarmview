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
	// XNU: level SOL_LOCAL (0), option LOCAL_PEERPID (2).
	err = raw.Control(func(fd uintptr) { pid, opErr = syscall.GetsockoptInt(int(fd), 0, 2) })
	if err == nil {
		err = opErr
	}
	return pid, err
}
