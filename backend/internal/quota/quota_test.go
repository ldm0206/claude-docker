package quota

import (
	"errors"
	"testing"
)

// fakeDisk implements DiskUsageProvider for tests.
type fakeDisk struct {
	bytes int64
	err   error
	// record last call
	gotHome string
	gotUser string
}

func (f *fakeDisk) Usage(homeRoot, username string) (int64, error) {
	f.gotHome = homeRoot
	f.gotUser = username
	if f.err != nil {
		return 0, f.err
	}
	return f.bytes, nil
}

// fakeCG implements CgroupWriter for tests.
type fakeCG struct {
	applyErr  error
	removeErr error
	// records of last calls
	appliedUID   int
	appliedCPU   string
	appliedMem   int64
	appliedCalls int
	removedUID   int
	removedCalls int
}

func (f *fakeCG) Apply(uid int, cpuQuota string, memMax int64) error {
	f.appliedCalls++
	f.appliedUID = uid
	f.appliedCPU = cpuQuota
	f.appliedMem = memMax
	return f.applyErr
}

func (f *fakeCG) Remove(uid int) error {
	f.removedCalls++
	f.removedUID = uid
	return f.removeErr
}

const gb = int64(1024 * 1024 * 1024)

func TestCheckDisk(t *testing.T) {
	t.Run("under limit", func(t *testing.T) {
		d := &fakeDisk{bytes: 7 * gb}
		s := New(d, &fakeCG{}, "/home")
		used, over, err := s.CheckDisk("alice", 10*gb)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if used != 7*gb {
			t.Fatalf("used=%d want 7GB", used)
		}
		if over {
			t.Fatal("over=true, want false (under limit)")
		}
		if d.gotUser != "alice" || d.gotHome != "/home" {
			t.Fatalf("Usage called with home=%q user=%q", d.gotHome, d.gotUser)
		}
	})

	t.Run("over limit", func(t *testing.T) {
		d := &fakeDisk{bytes: 7 * gb}
		s := New(d, &fakeCG{}, "/home")
		used, over, err := s.CheckDisk("alice", 5*gb)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if used != 7*gb {
			t.Fatalf("used=%d want 7GB", used)
		}
		if !over {
			t.Fatal("over=false, want true (used > limit)")
		}
	})

	t.Run("zero/negative limit never over", func(t *testing.T) {
		d := &fakeDisk{bytes: 999 * gb}
		s := New(d, &fakeCG{}, "/home")
		for _, lim := range []int64{0, -1, -100} {
			_, over, err := s.CheckDisk("alice", lim)
			if err != nil {
				t.Fatalf("limit=%d unexpected err: %v", lim, err)
			}
			if over {
				t.Fatalf("limit=%d over=true, want false (no limit)", lim)
			}
		}
	})

	t.Run("disk error propagates", func(t *testing.T) {
		wantErr := errors.New("du: command not found")
		d := &fakeDisk{err: wantErr}
		s := New(d, &fakeCG{}, "/home")
		used, over, err := s.CheckDisk("alice", 10*gb)
		if err == nil {
			t.Fatal("err=nil, want propagation")
		}
		if used != 0 {
			t.Fatalf("used=%d want 0 on error", used)
		}
		if over {
			t.Fatal("over=true on error, want false")
		}
	})
}

func TestApplyCgroup(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cg := &fakeCG{}
		s := New(&fakeDisk{}, cg, "/home")
		if err := s.ApplyCgroup(1001, "200000 100000", 2*gb); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if cg.appliedCalls != 1 {
			t.Fatalf("Apply called %d times, want 1", cg.appliedCalls)
		}
		if cg.appliedUID != 1001 {
			t.Fatalf("uid=%d want 1001", cg.appliedUID)
		}
		if cg.appliedCPU != "200000 100000" {
			t.Fatalf("cpu=%q want 200000 100000", cg.appliedCPU)
		}
		if cg.appliedMem != 2*gb {
			t.Fatalf("mem=%d want 2GB", cg.appliedMem)
		}
	})

	t.Run("degrade on error", func(t *testing.T) {
		cg := &fakeCG{applyErr: errors.New("EACCES /sys/fs/cgroup")}
		s := New(&fakeDisk{}, cg, "/home")
		if err := s.ApplyCgroup(1001, "200000 100000", 2*gb); err != nil {
			t.Fatalf("ApplyCgroup must degrade (return nil), got %v", err)
		}
		if cg.appliedCalls != 1 {
			t.Fatalf("Apply should still be called once, got %d", cg.appliedCalls)
		}
	})
}

func TestRemoveCgroup(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cg := &fakeCG{}
		s := New(&fakeDisk{}, cg, "/home")
		if err := s.RemoveCgroup(1001); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if cg.removedCalls != 1 {
			t.Fatalf("Remove called %d times, want 1", cg.removedCalls)
		}
		if cg.removedUID != 1001 {
			t.Fatalf("uid=%d want 1001", cg.removedUID)
		}
	})

	t.Run("degrade on error", func(t *testing.T) {
		cg := &fakeCG{removeErr: errors.New("device or resource busy")}
		s := New(&fakeDisk{}, cg, "/home")
		if err := s.RemoveCgroup(1001); err != nil {
			t.Fatalf("RemoveCgroup must degrade (return nil), got %v", err)
		}
		if cg.removedCalls != 1 {
			t.Fatalf("Remove should still be called once, got %d", cg.removedCalls)
		}
	})
}
