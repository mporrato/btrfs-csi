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
func runCommand(name string, args ...string) (string, error) {
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
	// Capture the subvolume ID before deletion so we can destroy its qgroup afterward.
	// Qgroup destruction must happen after subvolume deletion (the qgroup is "in use"
	// while the subvolume exists), using the parent directory as the filesystem path.
	var qgroupID string
	if showOut, err := runCommand("btrfs", "subvolume", "show", path); err == nil {
		if subvolID, err := parseSubvolumeID(showOut); err == nil {
			qgroupID = fmt.Sprintf("0/%d", subvolID)
		}
	}

	if _, err := runCommand("btrfs", "subvolume", "delete", path); err != nil {
		return fmt.Errorf("delete subvolume %s: %w", path, err)
	}

	// Best-effort: destroy the qgroup using the parent directory (still on the filesystem).
	if qgroupID != "" {
		parent := filepath.Dir(path)
		if _, err := runCommand("btrfs", "qgroup", "destroy", qgroupID, parent); err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "quotas not enabled") && !strings.Contains(msg, "no such") {
				return fmt.Errorf("destroy qgroup %s: %w", qgroupID, err)
			}
		}
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

func (m *RealManager) SetQgroupLimit(path string, bytes uint64) error {
	if _, err := runCommand("btrfs", "qgroup", "limit", strconv.FormatUint(bytes, 10), path); err != nil {
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

func (m *RealManager) GetFilesystemUsage(path string) (*FsUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}
	// Frsize is the fundamental block size that Blocks/Bfree/Bavail are denominated in.
	// Bsize is the preferred I/O size and may differ.
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
	for _, line := range strings.Split(out, "\n") {
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
// Column order: qgroupid rfer excl max_rfer max_excl
func parseQgroupShow(out, qgroupID string) (*QgroupUsage, error) {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
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
