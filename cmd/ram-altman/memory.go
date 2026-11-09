package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// memStats is the common shape every platform reads into. Values are bytes.
type memStats struct {
	Total     uint64
	Available uint64
	Used      uint64
	Free      uint64
	Cache     uint64
}

func (s memStats) AvailablePct() string {
	if s.Total == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", float64(s.Available)*100/float64(s.Total))
}

func (s memStats) overThreshold(pct int) bool {
	return s.Available*100 > s.Total*uint64(pct)
}

func gib(v uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(v)/(1<<30))
}

func printStatus(s memStats) {
	fmt.Printf("Total:      %s\n", gib(s.Total))
	fmt.Printf("Used:       %s\n", gib(s.Used))
	fmt.Printf("Available:  %s (%s%%)\n", gib(s.Available), s.AvailablePct())
	if s.Free != s.Available {
		fmt.Printf("  Free:     %s\n", gib(s.Free))
	}
	if s.Cache > 0 {
		fmt.Printf("  Cache:    %s\n", gib(s.Cache))
	}
}

// parseMeminfo understands the Linux /proc/meminfo format. It is kept free of
// build tags so the old-kernel fallback can be tested on every platform.
func parseMeminfo(r io.Reader) (memStats, error) {
	vals := make(map[string]uint64)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSpace(key)] = n // KiB
	}
	if err := scanner.Err(); err != nil {
		return memStats{}, err
	}

	totalKiB := vals["MemTotal"]
	if totalKiB == 0 {
		return memStats{}, fmt.Errorf("could not find MemTotal in memory information")
	}

	freeKiB := vals["MemFree"]
	cacheKiB := vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	if cacheKiB < vals["Shmem"] {
		cacheKiB = 0
	} else {
		cacheKiB -= vals["Shmem"]
	}

	availableKiB := vals["MemAvailable"]
	if _, ok := vals["MemAvailable"]; !ok {
		// Kernels older than 3.14 have no MemAvailable, so estimate it.
		availableKiB = freeKiB + cacheKiB
	}

	total := totalKiB * 1024
	available := availableKiB * 1024
	if available > total {
		available = total
	}

	return memStats{
		Total:     total,
		Available: available,
		Used:      total - available,
		Free:      freeKiB * 1024,
		Cache:     cacheKiB * 1024,
	}, nil
}
