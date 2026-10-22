package main

import (
	"strconv"
	"strings"
)

type system struct {
	FreePct       *int   `json:"freePct"`
	Pressure      *int   `json:"pressure"` // 1 normal, 2 warn, 4 critical
	SwapUsedBytes *int64 `json:"swapUsedBytes"`
}

// parseSwap reads `used = 3749.75M` from `sysctl -n vm.swapusage` (units K/M/G).
func parseSwap(text string) (int64, bool) {
	_, rest, ok := strings.Cut(text, "used = ")
	f := strings.Fields(rest)
	if !ok || len(f) == 0 || len(f[0]) < 2 {
		return 0, false
	}
	mult := map[byte]float64{'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30}[f[0][len(f[0])-1]]
	v, err := strconv.ParseFloat(f[0][:len(f[0])-1], 64)
	if mult == 0 || err != nil {
		return 0, false
	}
	return int64(v * mult), true
}

// refreshSystem reads the two cheap sysctls every snapshot and the swap exec at most every 10 s. Caller holds s.mu.
func (s *server) refreshSystem(now int64) {
	if s.sysU32 == nil {
		return
	}
	s.sys = system{}
	if v, ok := s.sysU32("kern.memorystatus_level"); ok {
		n := int(v)
		s.sys.FreePct = &n
	}
	if v, ok := s.sysU32("kern.memorystatus_vm_pressure_level"); ok {
		n := int(v)
		s.sys.Pressure = &n
	}
	if s.swapAt == 0 || now-s.swapAt >= 10000 {
		s.swapAt, s.swap = now, nil
		if out, err := s.run("sysctl", "-n", "vm.swapusage"); err == nil {
			if b, ok := parseSwap(out); ok {
				s.swap = &b
			}
		}
	}
	s.sys.SwapUsedBytes = s.swap
}
