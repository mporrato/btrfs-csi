package btrfs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// RealManager implements Manager using the btrfs CLI.
type RealManager struct{}

// Compile-time check that RealManager implements Manager.
var _ Manager = (*RealManager)(nil)

// runCommand executes a command and returns its stdout. On failure, stderr is
// included in the returned error for debuggability.
//
//nolint:unparam // name parameter allows for testing flexibility and future generalization
func runCommand(name string, args ...string) (string, error) {
	//nolint:gosec // btrfs command with internal args, not user input
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (m *RealManager) CreateSubvolume(path string) error {
	if _, err := runCommand("btrfs", "subvolume", "create", path); err != nil {
		return fmt.Errorf("create subvolume %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) DeleteSubvolume(path string) error {
	if _, err := runCommand("btrfs", "subvolume", "delete", path); err != nil {
		return fmt.Errorf("delete subvolume %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) ClearStaleQgroups(mountpoint string) error {
	if _, err := runCommand("btrfs", "qgroup", "clear-stale", mountpoint); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "quotas not enabled") {
			return nil
		}
		return fmt.Errorf("clear stale qgroups on %s: %w", mountpoint, err)
	}
	return nil
}

func (m *RealManager) SubvolumeExists(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	_, err := runCommand("btrfs", "subvolume", "show", path)
	if err == nil {
		return true, nil
	}
	// "Not a Btrfs subvolume" or "not a btrfs filesystem" means the path exists
	// but is not a subvolume — treat as not found rather than an error.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not a btrfs subvolume") ||
		strings.Contains(msg, "not a btrfs filesystem") {
		return false, nil
	}
	return false, fmt.Errorf("check subvolume %s: %w", path, err)
}

func (m *RealManager) CreateSnapshot(src, dst string, readonly bool) error {
	args := []string{"subvolume", "snapshot"}
	if readonly {
		args = append(args, "-r")
	}
	args = append(args, src, dst)
	if _, err := runCommand("btrfs", args...); err != nil {
		return fmt.Errorf("create snapshot %s -> %s: %w", src, dst, err)
	}
	return nil
}

func (m *RealManager) EnsureQuotaEnabled(mountpoint string) error {
	// Try --simple first (newer btrfs-progs); fall back to traditional qgroups.
	if _, err := runCommand("btrfs", "quota", "enable", "--simple", mountpoint); err == nil {
		return nil
	}
	if _, err := runCommand("btrfs", "quota", "enable", mountpoint); err != nil {
		return fmt.Errorf("enable quota on %s: %w", mountpoint, err)
	}
	return nil
}

func (m *RealManager) SetQgroupLimit(path string, limitBytes uint64) error {
	if _, err := runCommand("btrfs", "qgroup", "limit", strconv.FormatUint(limitBytes, 10), path); err != nil {
		return fmt.Errorf("set qgroup limit on %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) RemoveQgroupLimit(path string) error {
	if _, err := runCommand("btrfs", "qgroup", "limit", "none", path); err != nil {
		return fmt.Errorf("remove qgroup limit on %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) GetQgroupUsage(path string) (*QgroupUsage, error) {
	showOut, err := runCommand("btrfs", "subvolume", "show", path)
	if err != nil {
		return nil, fmt.Errorf("get subvolume info for %s: %w", path, err)
	}
	subvolID, err := parseSubvolumeID(showOut)
	if err != nil {
		return nil, fmt.Errorf("parse subvolume ID for %s: %w", path, err)
	}

	qgroupOut, err := runCommand("btrfs", "qgroup", "show", "-r", "--raw", path)
	if err != nil {
		return nil, fmt.Errorf("get qgroup usage for %s: %w", path, err)
	}

	qgroupID := fmt.Sprintf("0/%d", subvolID)
	usage, err := parseQgroupShow(qgroupOut, qgroupID)
	if err != nil {
		return nil, fmt.Errorf("parse qgroup output for %s: %w", path, err)
	}
	return usage, nil
}

// btrfsSuperMagic is the magic number for btrfs filesystems (from Linux kernel).
const btrfsSuperMagic = 0x9123683E

func (m *RealManager) IsBtrfsFilesystem(path string) (bool, error) {
	// Walk up to the nearest existing ancestor — path may not exist yet.
	cur := path
	for {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(cur, &stat); err == nil {
			return stat.Type == btrfsSuperMagic, nil
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("statfs %s: %w", cur, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false, fmt.Errorf("no existing ancestor found for %s", path)
		}
		cur = parent
	}
}

func (m *RealManager) GetFilesystemUsage(path string) (*FsUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}
	// Frsize is the fundamental block size that Blocks/Bfree/Bavail are denominated in.
	// Bsize is the preferred I/O size and may differ.
	//nolint:gosec // Frsize from statfs is always positive
	frsize := uint64(stat.Frsize)
	total := stat.Blocks * frsize
	free := stat.Bfree * frsize
	return &FsUsage{
		Total:     total,
		Used:      total - free,
		Available: stat.Bavail * frsize,
	}, nil
}

// parseSubvolumeID extracts the numeric subvolume ID from `btrfs subvolume show` output.
func parseSubvolumeID(out string) (uint64, error) {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Subvolume ID:") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return 0, fmt.Errorf("unexpected format: %q", line)
			}
			return strconv.ParseUint(fields[2], 10, 64)
		}
	}
	return 0, fmt.Errorf("subvolume ID not found in output")
}

// parseQgroupShow finds the qgroup entry matching qgroupID in `btrfs qgroup show -r --raw` output.
// Column order: qgroupid rfer excl max_rfer max_excl.
func parseQgroupShow(out, qgroupID string) (*QgroupUsage, error) {
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != qgroupID {
			continue
		}
		rfer, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse rfer: %w", err)
		}
		excl, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse excl: %w", err)
		}
		// In btrfs, max_rfer of 0 means "no limit" (same as "none" in human-readable mode).
		var maxRfer uint64
		if fields[3] != "none" && fields[3] != "0" {
			maxRfer, err = strconv.ParseUint(fields[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse max_rfer: %w", err)
			}
		}
		return &QgroupUsage{
			Referenced: rfer,
			Exclusive:  excl,
			MaxRfer:    maxRfer,
		}, nil
	}
	return nil, fmt.Errorf("qgroup %s not found in output", qgroupID)
}
