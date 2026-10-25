//go:build !darwin && !linux

package main

import (
	"errors"
	"net"
)

func pulsePeerPID(c net.Conn) (int, error) {
	return 0, errors.New("communication bridge requires macOS or Linux")
}
