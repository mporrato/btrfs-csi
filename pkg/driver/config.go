package driver

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DiscoverPools scans baseDir for immediate subdirectories. Each subdirectory
// represents a storage pool: the directory name is the pool name and its path
// is the pool base path. Hidden entries (starting with '.') and non-directories
// are skipped.
func DiscoverPools(baseDir string) (map[string]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("read pools dir: %w", err)
	}
	pools := make(map[string]string)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			continue
		}
		pools[name] = filepath.Join(baseDir, name)
	}
	return pools, nil
}

// WatchPools polls baseDir every intervalMs milliseconds. On each poll it
// discovers pools and compares the result to the last-seen pool map; if it
// changed, it calls reload with the new map.
func WatchPools(baseDir string, intervalMs int, reload func(map[string]string)) chan<- struct{} {
	stop := make(chan struct{})
	go func() {
		// Seed lastPools so the first tick doesn't redundantly fire reload
		// for config that initializeStores already processed at startup.
		lastPools, _ := DiscoverPools(baseDir)
		tick := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			if pools, err := DiscoverPools(baseDir); err == nil {
				if !maps.Equal(pools, lastPools) {
					lastPools = pools
					reload(pools)
				}
			}
		}
	}()
	return stop
}
