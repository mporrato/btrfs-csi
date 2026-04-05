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

	"k8s.io/klog/v2"
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
	same, err := m.sameFilesystem(src, dst)
	if err != nil {
		return err
	}
	if same {
		klog.V(4).InfoS("CreateSnapshot: same filesystem, using btrfs subvolume snapshot",
			"src", src, "dst", dst, "readonly", readonly)
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
	klog.V(4).InfoS("CreateSnapshot: different filesystem, using send/receive",
		"src", src, "dst", dst, "readonly", readonly)
	return m.sendReceive(src, dst, readonly)
}

// sameFilesystem checks if src and dst are on the same btrfs filesystem.
// Since dst may not exist yet, it walks up to the nearest existing ancestor.
func (m *RealManager) sameFilesystem(src, dst string) (bool, error) {
	var srcStat, dstStat syscall.Stat_t
	if err := syscall.Stat(src, &srcStat); err != nil {
		return false, fmt.Errorf("stat %s: %w", src, err)
	}
	dstDir := dst
	for {
		if err := syscall.Stat(dstDir, &dstStat); err == nil {
			break
		}
		parent := filepath.Dir(dstDir)
		if parent == dstDir {
			return false, fmt.Errorf("no existing ancestor for %s", dst)
		}
		dstDir = parent
	}
	return srcStat.Dev == dstStat.Dev, nil
}

// sendReceive performs cross-filesystem snapshot using btrfs send/receive.
func (m *RealManager) sendReceive(src, dst string, readonly bool) error {
	klog.V(4).InfoS("sendReceive: creating cross-filesystem snapshot", "src", src, "dst", dst, "readonly", readonly)

	// 0. Idempotency check: if destination already exists, return success
	exists, err := m.SubvolumeExists(dst)
	if err != nil {
		return fmt.Errorf("check destination: %w", err)
	}
	if exists {
		klog.V(4).InfoS("sendReceive: destination already exists", "dst", dst)
		return nil
	}

	// 1. Create temporary readonly snapshot of src
	// Use direct runCommand to avoid potential recursion if src and tempSnap are on different filesystems
	tempSnap := filepath.Join(filepath.Dir(src), fmt.Sprintf(".btrfs-csi-send-%s-%d", filepath.Base(src), os.Getpid()))
	if _, err := runCommand("btrfs", "subvolume", "snapshot", "-r", src, tempSnap); err != nil {
		return fmt.Errorf("create temp snapshot: %w", err)
	}
	defer func() { _ = m.DeleteSubvolume(tempSnap) }()

	// 2. Ensure destination parent directory exists
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		return fmt.Errorf("create dst directory: %w", err)
	}

	// 3. btrfs send <temp> | btrfs receive <dst_dir>
	//nolint:gosec // tempSnap is generated internally, not user input
	sendCmd := exec.Command("btrfs", "send", tempSnap)
	//nolint:gosec // dstDir is derived from dst parameter, controlled by CSI driver
	receiveCmd := exec.Command("btrfs", "receive", dstDir)
	sendStdout, err := sendCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create send stdout pipe: %w", err)
	}
	receiveCmd.Stdin = sendStdout
	var sendStderr, recvStderr bytes.Buffer
	sendCmd.Stderr = &sendStderr
	receiveCmd.Stderr = &recvStderr
	if err := receiveCmd.Start(); err != nil {
		return fmt.Errorf("start receive: %w", err)
	}
	if err := sendCmd.Run(); err != nil {
		_ = receiveCmd.Process.Kill()
		return fmt.Errorf("btrfs send %s: %w: %s", tempSnap, err, sendStderr.String())
	}
	if err := receiveCmd.Wait(); err != nil {
		// Clean up partial received snapshot
		receivedPath := filepath.Join(dstDir, filepath.Base(tempSnap))
		_ = m.DeleteSubvolume(receivedPath)
		return fmt.Errorf("btrfs receive %s: %w: %s", dstDir, err, recvStderr.String())
	}

	// 4. Rename received snapshot to dst
	receivedName := filepath.Base(tempSnap)
	receivedPath := filepath.Join(dstDir, receivedName)
	if receivedPath != dst {
		if err := os.Rename(receivedPath, dst); err != nil {
			_ = m.DeleteSubvolume(receivedPath)
			return fmt.Errorf("rename %s -> %s: %w", receivedPath, dst, err)
		}
	}

	// 5. Make writable if needed
	if !readonly {
		if _, err := runCommand("btrfs", "property", "set", dst, "ro", "false"); err != nil {
			return fmt.Errorf("make writable %s: %w", dst, err)
		}
	}

	klog.V(4).InfoS("sendReceive: cross-filesystem snapshot created", "src", src, "dst", dst)
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
