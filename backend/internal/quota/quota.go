// Package quota provides disk-usage checking and cgroup-based resource limits
// for user sessions. Interfaces are seam-driven so unit tests on Windows inject
// fakes; the real implementations (du / /sys/fs/cgroup) are Linux-only at
// runtime but compile cross-platform — they will simply fail against /sys or
// fail to find `du` on Windows, which is acceptable because tests never
// exercise them.
package quota

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DiskUsageProvider reports the bytes consumed by a user's home directory.
//
// Real implementation: DuDiskUsage shells out to `du -sb <homeRoot>/<username>`
// (Linux). Tests inject a fake.
type DiskUsageProvider interface {
	Usage(homeRoot, username string) (int64, error)
}

// CgroupWriter applies and removes cgroup-v2 resource limits for a user's
// session. Real implementation: CgroupFSWriter writes
// /sys/fs/cgroup/cu-<uid>/{cpu.max,memory.max}. Tests inject a fake.
type CgroupWriter interface {
	Apply(uid int, cpuQuota string, memMax int64) error
	Remove(uid int) error
}

// Service wires a DiskUsageProvider and a CgroupWriter together.
// Cgroup operations degrade on error (logged, never fail the session).
type Service struct {
	Disk     DiskUsageProvider
	CG       CgroupWriter
	HomeRoot string // typically "/home"
}

// New constructs a Service backed by the given implementations.
func New(disk DiskUsageProvider, cg CgroupWriter, homeRoot string) *Service {
	return &Service{Disk: disk, CG: cg, HomeRoot: homeRoot}
}

// CheckDisk reports the bytes used by username and whether usage exceeds limit.
// A limit <= 0 means "no limit"; over is always false in that case.
// Disk provider errors propagate (used=0, over=false, err set).
func (s *Service) CheckDisk(username string, limit int64) (used int64, over bool, err error) {
	used, err = s.Disk.Usage(s.HomeRoot, username)
	if err != nil {
		return 0, false, err
	}
	if limit > 0 && used > limit {
		over = true
	}
	return used, over, nil
}

// ApplyCgroup wraps CG.Apply. On cgroup write failure it logs and returns nil
// so a resource-limit problem can never block the session from starting.
func (s *Service) ApplyCgroup(uid int, cpuQuota string, memMax int64) error {
	if err := s.CG.Apply(uid, cpuQuota, memMax); err != nil {
		log.Printf("quota: cgroup apply uid=%d cpu=%q mem=%d failed (degrading): %v",
			uid, cpuQuota, memMax, err)
		return nil
	}
	return nil
}

// RemoveCgroup wraps CG.Remove. On failure it logs and returns nil; a stale
// empty cgroup directory is harmless and not worth failing teardown.
func (s *Service) RemoveCgroup(uid int) error {
	if err := s.CG.Remove(uid); err != nil {
		log.Printf("quota: cgroup remove uid=%d failed (degrading): %v", uid, err)
		return nil
	}
	return nil
}

// DuDiskUsage is the real DiskUsageProvider: it shells out to `du -sb <path>`.
// Linux-only at runtime; compiles on Windows (exec.Command is cross-platform)
// but `du` will not be found there.
type DuDiskUsage struct{}

// Usage runs `du -sb <homeRoot>/<username>` and parses the first numeric field
// (du prints "<bytes>\t<path>").
func (DuDiskUsage) Usage(homeRoot, username string) (int64, error) {
	path := filepath.Join(homeRoot, username)
	out, err := exec.Command("du", "-sb", path).Output()
	if err != nil {
		return 0, err
	}
	// du output: "<bytes>\t<path>"
	field := strings.Fields(string(out))
	if len(field) == 0 {
		return 0, fmt.Errorf("quota: empty du output for %s", path)
	}
	n, err := strconv.ParseInt(field[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("quota: parse du bytes %q for %s: %w", field[0], path, err)
	}
	return n, nil
}

// CgroupFSWriter is the real CgroupWriter: it manages
// /sys/fs/cgroup/cu-<uid>/{cpu.max,memory.max}. Linux-only at runtime; the
// os.* calls compile on Windows but will fail against /sys.
type CgroupFSWriter struct{}

// Apply creates the user's cgroup directory and writes cpu.max / memory.max
// when their parameters are non-empty / positive respectively. Any write
// failure is returned; the Service layer degrades (log + don't fail session).
func (CgroupFSWriter) Apply(uid int, cpuQuota string, memMax int64) error {
	dir := cgroupDir(uid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if cpuQuota != "" {
		if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(cpuQuota+"\n"), 0o644); err != nil {
			return err
		}
	}
	if memMax > 0 {
		if err := os.WriteFile(filepath.Join(dir, "memory.max"),
			[]byte(strconv.FormatInt(memMax, 10)+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Remove recursively deletes the user's cgroup directory.
func (CgroupFSWriter) Remove(uid int) error {
	return os.RemoveAll(cgroupDir(uid))
}

func cgroupDir(uid int) string {
	return filepath.Join("/sys/fs/cgroup", "cu-"+strconv.Itoa(uid))
}
