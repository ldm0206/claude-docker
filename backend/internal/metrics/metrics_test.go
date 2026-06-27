package metrics

import "testing"

func fakeRead(files map[string]string) func(string) string {
	return func(p string) string { return files[p] }
}

func TestCPU(t *testing.T) {
	r := fakeRead(map[string]string{"/sys/fs/cgroup/cpu.stat": "usage_usec 12345\nthreads 2\n"})
	if ReadCgroupCPU(r) != 12345 {
		t.Fatal("cpu parse failed")
	}
}

func TestMemory(t *testing.T) {
	r := fakeRead(map[string]string{
		"/sys/fs/cgroup/memory.current": "1048576",
		"/sys/fs/cgroup/memory.max":     "2097152",
	})
	cur, max, set := ReadCgroupMemory(r)
	if cur != 1048576 || max != 2097152 || !set {
		t.Fatalf("got cur=%d max=%d set=%v", cur, max, set)
	}
	r2 := fakeRead(map[string]string{
		"/sys/fs/cgroup/memory.current": "10",
		"/sys/fs/cgroup/memory.max":     "max",
	})
	if _, _, set := ReadCgroupMemory(r2); set {
		t.Fatal("max=max should be unset")
	}
}

func TestNetDev(t *testing.T) {
	r := fakeRead(map[string]string{"/proc/net/dev": "header\n  col\neth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\nlo: 5 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0\n"})
	rx, tx := ReadNetDev(r)
	if rx != 100 || tx != 200 {
		t.Fatalf("got rx=%d tx=%d", rx, tx)
	}
}
