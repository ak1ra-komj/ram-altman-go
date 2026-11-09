//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func readMemory() (memStats, error) {
	total, err := sysctlUint64("hw.memsize")
	if err != nil {
		return memStats{}, err
	}
	if total == 0 {
		return memStats{}, fmt.Errorf("hw.memsize returned zero total memory")
	}
	pageSize, err := sysctlUint64("hw.pagesize")
	if err != nil {
		return memStats{}, err
	}
	freePages, err := sysctlUint64("vm.page_free_count")
	if err != nil {
		return memStats{}, err
	}
	inactivePages, err := sysctlUint64("vm.page_inactive_count")
	if err != nil {
		return memStats{}, err
	}

	free := freePages * pageSize
	available := free + inactivePages*pageSize
	if available > total {
		available = total
	}

	return memStats{
		Total:     total,
		Available: available,
		Used:      total - available,
		Free:      free,
	}, nil
}

func sysctlUint64(name string) (uint64, error) {
	out, err := exec.Command("/usr/sbin/sysctl", "-n", name).Output()
	if err != nil {
		return 0, fmt.Errorf("sysctl %s: %w", name, err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("sysctl %s: %w", name, err)
	}
	return v, nil
}
