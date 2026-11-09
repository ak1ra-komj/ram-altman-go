//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

type memoryStatusEx struct {
	cbSize                  uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

func readMemory() (memStats, error) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return memStats{}, fmt.Errorf("32-bit Windows is not supported")
	}

	var m memoryStatusEx
	m.cbSize = uint32(unsafe.Sizeof(m))

	ok, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if ok == 0 {
		return memStats{}, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	if m.ullTotalPhys == 0 {
		return memStats{}, fmt.Errorf("GlobalMemoryStatusEx returned zero total memory")
	}
	if m.ullAvailPhys > m.ullTotalPhys {
		m.ullAvailPhys = m.ullTotalPhys
	}

	return memStats{
		Total:     m.ullTotalPhys,
		Available: m.ullAvailPhys,
		Used:      m.ullTotalPhys - m.ullAvailPhys,
		Free:      m.ullAvailPhys,
	}, nil
}
