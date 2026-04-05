package driver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ParseConfigFile reads a basePaths config file and returns the list of
// absolute paths. Lines starting with '#' and blank lines are ignored.
// Returns an error if any path is not absolute.
func ParseConfigFile(path string) ([]string, error) {
	return parseConfigFile(path)
}

func parseConfigFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var paths []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			return nil, fmt.Errorf("config file contains non-absolute path: %q", line)
		}
		paths = append(paths, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return paths, nil
}

// WatchConfigFile is the exported wrapper around watchConfigFile for use by main.
func WatchConfigFile(path string, intervalMs int, reload func([]string)) chan<- struct{} {
	return watchConfigFile(path, intervalMs, reload)
}

// pathsEqual returns true if both slices contain the same paths in the same order.
func pathsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// watchConfigFile polls path every intervalMs milliseconds. On each poll it
// parses the config file and compares the result to the last-seen path list;
// if it changed (or on first call), it calls reload with the new path list.
// Errors parsing the file are silently skipped so a bad config update doesn't
// break a running driver.
//
// The caller signals shutdown by closing the returned stop channel.
func watchConfigFile(path string, intervalMs int, reload func([]string)) chan<- struct{} {
	stop := make(chan struct{})
	go func() {
		var lastPaths []string
		tick := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer tick.Stop()
		for {
			// Fire immediately on first iteration before waiting for tick.
			if paths, err := parseConfigFile(path); err == nil {
				if !pathsEqual(paths, lastPaths) {
					lastPaths = paths
					reload(paths)
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
