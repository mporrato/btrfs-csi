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
// discovers pool subdirectories and passes the result through validate
// (e.g. checking that each path is a btrfs filesystem mounted as a separate
// mountpoint). If the validated pool map differs from the last-applied one,
// it calls reload with the new map.
//
// Re-validating on every tick — rather than comparing the raw directory
// listing — ensures pools that become valid after startup (e.g. a
// filesystem that is mounted after the driver starts) are picked up, and
// pools that become invalid at runtime (e.g. unmounted) are dropped before
// further volume operations can target the wrong filesystem.
func WatchPools(
	baseDir string,
	intervalMs int,
	validate func(map[string]string) map[string]string,
	reload func(map[string]string),
) chan<- struct{} {
	stop := make(chan struct{})
	go func() {
		// Seed lastPools so the first tick doesn't redundantly fire reload
		// for config that initializeStores already processed at startup.
		var lastPools map[string]string
		if discovered, err := DiscoverPools(baseDir); err == nil {
			lastPools = validate(discovered)
		}
		tick := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			discovered, err := DiscoverPools(baseDir)
			if err != nil {
				continue
			}
			valid := validate(discovered)
			if !maps.Equal(valid, lastPools) {
				lastPools = valid
				reload(valid)
			}
		}
	}()
	return stop
}
