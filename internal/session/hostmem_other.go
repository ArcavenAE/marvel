//go:build !darwin && !linux

package session

func readFreeMemoryPercent() (int, bool) { return 0, false }
