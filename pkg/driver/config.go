package driver

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ParsePoolConfig reads a directory (typically a mounted ConfigMap) where each
// regular file represents a storage pool: the filename is the pool name and the
// file content (trimmed) is the absolute path. Hidden files (starting with '.')
// and directories are skipped. Returns an error if any path is not absolute.
func ParsePoolConfig(dir string) (map[string]string, error) {
	return parsePoolConfig(dir)
}

func parsePoolConfig(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read pool config dir: %w", err)
	}
	pools := make(map[string]string)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			continue
		}
		//nolint:gosec // reading from mounted ConfigMap, not user input
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read pool %q: %w", name, err)
		}
		p := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("pool %q contains non-absolute path: %q", name, p)
		}
		pools[name] = p
	}
	return pools, nil
}

// WatchPoolConfig is the exported wrapper around watchPoolConfig for use by main.
func WatchPoolConfig(dir string, intervalMs int, reload func(map[string]string)) chan<- struct{} {
	return watchPoolConfig(dir, intervalMs, reload)
}

// watchPoolConfig polls dir every intervalMs milliseconds. On each poll it
// parses the pool config directory and compares the result to the last-seen
// pool map; if it changed (or on first call), it calls reload with the new map.
func watchPoolConfig(dir string, intervalMs int, reload func(map[string]string)) chan<- struct{} {
	stop := make(chan struct{})
	go func() {
		var lastPools map[string]string
		tick := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer tick.Stop()
		for {
			if pools, err := parsePoolConfig(dir); err == nil {
				if !maps.Equal(pools, lastPools) {
					lastPools = pools
					reload(pools)
				}
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	return stop
}
