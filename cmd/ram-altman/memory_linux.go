//go:build linux

package main

import (
	"fmt"
	"os"
)

func readMemory() (memStats, error) {
	path := os.Getenv("RAM_ALTMAN_MEMINFO")
	if path == "" {
		path = "/proc/meminfo"
	}

	f, err := os.Open(path)
	if err != nil {
		return memStats{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()

	stats, err := parseMeminfo(f)
	if err != nil {
		return memStats{}, fmt.Errorf("%s: %w", path, err)
	}
	return stats, nil
}
