package btrfs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"k8s.io/klog/v2"
)

// RealManager implements Manager using the btrfs CLI.
type RealManager struct{}

// Compile-time check that RealManager implements Manager.
var _ Manager = (*RealManager)(nil)

// runCommand executes a command with context and returns its stdout. On failure,
// stderr is included in the returned error for debuggability.
//
//nolint:unparam // name parameter allows for testing flexibility and future generalization
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	//nolint:gosec // btrfs command with internal args, not user input
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (m *RealManager) CreateSubvolume(ctx context.Context, path string) error {
	if _, err := runCommand(ctx, "btrfs", "subvolume", "create", path); err != nil {
		return fmt.Errorf("create subvolume %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) DeleteSubvolume(ctx context.Context, path string) error {
	if _, err := runCommand(ctx, "btrfs", "subvolume", "delete", path); err != nil {
		return fmt.Errorf("delete subvolume %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) ClearStaleQgroups(ctx context.Context, mountpoint string) (int, error) {
	output, err := runCommand(ctx, "btrfs", "qgroup", "clear-stale", mountpoint)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "quotas not enabled") {
			return 0, nil
		}
		// "Device or resource busy" means some qgroups could not be
		// removed yet (kernel still cleaning up).  This is a best-effort
		// operation, so treat it as a partial success — the next
		// scheduled cleanup will pick up the remainder.
		if strings.Contains(msg, "device or resource busy") {
			return 0, nil
		}
		return 0, fmt.Errorf("clear stale qgroups on %s: %w", mountpoint, err)
	}
	// Count the number of removed qgroups by counting output lines.
	// Each removed qgroup produces one line of output.
	count := 0
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line != "" {
			count++
		}
	}
	return count, nil
}

func (m *RealManager) SubvolumeExists(ctx context.Context, path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	_, err := runCommand(ctx, "btrfs", "subvolume", "show", path)
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

func (m *RealManager) CreateSnapshot(ctx context.Context, src, dst string, readonly bool) error {
	// Try direct snapshot first (works when src and dst are on the same btrfs filesystem).
	// Fall back to send/receive for the cross-filesystem case.
	// We avoid filesystem detection heuristics because btrfs subvolumes can report
	// different device IDs and filesystem IDs depending on mount configuration.
	args := []string{"subvolume", "snapshot"}
	if readonly {
		args = append(args, "-r")
	}
	args = append(args, src, dst)
	if _, err := runCommand(ctx, "btrfs", args...); err == nil {
		klog.V(4).InfoS("CreateSnapshot: direct snapshot succeeded",
			"src", src, "dst", dst, "readonly", readonly)
		return nil
	}
	klog.V(4).InfoS("CreateSnapshot: direct snapshot failed, trying send/receive",
		"src", src, "dst", dst, "readonly", readonly)
	return m.sendReceive(ctx, src, dst, readonly)
}

// cleanupStaleSnapshots deletes stale temp snapshots from the given directories.
func (m *RealManager) cleanupStaleSnapshots(ctx context.Context, srcDir, tempDir, srcBase string) {
	tempSnapBase := fmt.Sprintf(".btrfs-csi-send-%s-*", srcBase)
	for _, dir := range []string{srcDir, tempDir} {
		matches, _ := filepath.Glob(filepath.Join(dir, tempSnapBase))
		for _, match := range matches {
			klog.V(4).InfoS("cleanupStaleSnapshots: removing", "path", match)
			if err := m.DeleteSubvolume(ctx, match); err != nil {
				klog.V(2).InfoS("cleanupStaleSnapshots: failed to remove", "path", match, "err", err)
			}
		}
	}
}

// doSendReceive performs btrfs send/receive and renames the result.
func (m *RealManager) doSendReceive(ctx context.Context, tempSnap, dstDir, dst string, readonly bool) error {
	//nolint:gosec // tempSnap is generated internally, not user input
	sendCmd := exec.CommandContext(ctx, "btrfs", "send", "--compressed-data", tempSnap)
	//nolint:gosec // dstDir is derived from dst parameter, controlled by CSI driver
	receiveCmd := exec.CommandContext(ctx, "btrfs", "receive", dstDir)
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
		_ = receiveCmd.Wait() // reap the killed child
		return fmt.Errorf("btrfs send %s: %w: %s", tempSnap, err, sendStderr.String())
	}
	if err := receiveCmd.Wait(); err != nil {
		// Clean up partial received snapshot
		receivedPath := filepath.Join(dstDir, filepath.Base(tempSnap))
		_ = m.DeleteSubvolume(ctx, receivedPath)
		return fmt.Errorf("btrfs receive %s: %w: %s", dstDir, err, recvStderr.String())
	}

	// Rename received snapshot to dst
	receivedName := filepath.Base(tempSnap)
	receivedPath := filepath.Join(dstDir, receivedName)
	if receivedPath != dst {
		if err := os.Rename(receivedPath, dst); err != nil {
			_ = m.DeleteSubvolume(ctx, receivedPath)
			return fmt.Errorf("rename %s -> %s: %w", receivedPath, dst, err)
		}
	}

	// Make writable if needed
	if !readonly {
		if _, err := runCommand(ctx, "btrfs", "property", "set", "-f", dst, "ro", "false"); err != nil {
			return fmt.Errorf("make writable %s: %w", dst, err)
		}
	}
	return nil
}

// sendReceive performs cross-filesystem snapshot using btrfs send/receive.
func (m *RealManager) sendReceive(ctx context.Context, src, dst string, readonly bool) error {
	klog.V(4).InfoS("sendReceive: creating cross-filesystem snapshot", "src", src, "dst", dst, "readonly", readonly)

	// 0. Idempotency check: if destination already exists, return success
	exists, err := m.SubvolumeExists(ctx, dst)
	if err != nil {
		return fmt.Errorf("check destination: %w", err)
	}
	if exists {
		klog.V(4).InfoS("sendReceive: destination already exists", "dst", dst)
		return nil
	}

	// 1. Create a dedicated temp subdirectory for temp snapshots.
	// This prevents collision when srcDir == dstDir: btrfs receive would
	// try to create a subvolume with the same name as the temp snapshot.
	srcDir := filepath.Dir(src)
	tempDir := filepath.Join(srcDir, ".btrfs-csi-tmp")
	if err := os.MkdirAll(tempDir, 0o750); err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer func() { _ = os.Remove(tempDir) }() // clean up empty dir

	// 2. Clean up stale temp snapshots from previous runs (both old and new locations)
	m.cleanupStaleSnapshots(ctx, srcDir, tempDir, filepath.Base(src))

	// 3. Create temporary readonly snapshot of src in the temp subdirectory
	snapName := fmt.Sprintf(".btrfs-csi-send-%s-%s", filepath.Base(src), uuid.New().String())
	tempSnap := filepath.Join(tempDir, snapName)
	if _, err := runCommand(ctx, "btrfs", "subvolume", "snapshot", "-r", src, tempSnap); err != nil {
		return fmt.Errorf("create temp snapshot: %w", err)
	}
	defer func() {
		if err := m.DeleteSubvolume(ctx, tempSnap); err != nil {
			klog.V(2).InfoS("sendReceive: failed to delete temp snapshot", "path", tempSnap, "err", err)
		}
	}()

	// 4. Ensure destination parent directory exists
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		return fmt.Errorf("create dst directory: %w", err)
	}

	// Also clean up stale received snapshots in dstDir from previous failed runs
	tempSnapBase := fmt.Sprintf(".btrfs-csi-send-%s-*", filepath.Base(src))
	dstMatches, _ := filepath.Glob(filepath.Join(dstDir, tempSnapBase))
	for _, match := range dstMatches {
		klog.V(4).InfoS("sendReceive: cleaning up stale received snapshot", "path", match)
		if err := m.DeleteSubvolume(ctx, match); err != nil {
			klog.V(2).InfoS("sendReceive: failed to clean up stale received snapshot", "path", match, "err", err)
		}
	}

	// 5. btrfs send <temp> | btrfs receive <dst_dir>
	if err := m.doSendReceive(ctx, tempSnap, dstDir, dst, readonly); err != nil {
		return err
	}

	klog.V(4).InfoS("sendReceive: cross-filesystem snapshot created", "src", src, "dst", dst)
	return nil
}

func (m *RealManager) EnsureQuotaEnabled(ctx context.Context, mountpoint string) error {
	// Try --simple first (newer btrfs-progs); fall back to traditional qgroups.
	if _, err := runCommand(ctx, "btrfs", "quota", "enable", "--simple", mountpoint); err == nil {
		return nil
	}
	if _, err := runCommand(ctx, "btrfs", "quota", "enable", mountpoint); err != nil {
		return fmt.Errorf("enable quota on %s: %w", mountpoint, err)
	}
	return nil
}

func (m *RealManager) SetQgroupLimit(ctx context.Context, path string, limitBytes uint64) error {
	if _, err := runCommand(ctx, "btrfs", "qgroup", "limit", strconv.FormatUint(limitBytes, 10), path); err != nil {
		return fmt.Errorf("set qgroup limit on %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) RemoveQgroupLimit(ctx context.Context, path string) error {
	if _, err := runCommand(ctx, "btrfs", "qgroup", "limit", "none", path); err != nil {
		return fmt.Errorf("remove qgroup limit on %s: %w", path, err)
	}
	return nil
}

func (m *RealManager) GetQgroupUsage(ctx context.Context, path string) (*QgroupUsage, error) {
	showOut, err := runCommand(ctx, "btrfs", "subvolume", "show", path)
	if err != nil {
		return nil, fmt.Errorf("get subvolume info for %s: %w", path, err)
	}
	subvolID, err := parseSubvolumeID(showOut)
	if err != nil {
		return nil, fmt.Errorf("parse subvolume ID for %s: %w", path, err)
	}

	qgroupOut, err := runCommand(ctx, "btrfs", "qgroup", "show", "-r", "--raw", path)
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

func (m *RealManager) IsBtrfsFilesystem(ctx context.Context, path string) (bool, error) {
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

func (m *RealManager) IsMountpoint(ctx context.Context, path string) (bool, error) {
	var pathStat, parentStat syscall.Stat_t
	if err := syscall.Stat(path, &pathStat); err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	parent := filepath.Dir(path)
	if err := syscall.Stat(parent, &parentStat); err != nil {
		return false, fmt.Errorf("stat %s: %w", parent, err)
	}
	return pathStat.Dev != parentStat.Dev, nil
}

func (m *RealManager) GetFilesystemUsage(ctx context.Context, path string) (*FsUsage, error) {
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
