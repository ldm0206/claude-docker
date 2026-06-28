package capture

import (
	"sync"
	"testing"
)

func TestAddAndList(t *testing.T) {
	s := NewStore()
	r1 := Record{SessionID: "s1", Method: "GET", Path: "/a", Ts: 1}
	r2 := Record{SessionID: "s2", Method: "POST", Path: "/b", Ts: 2}
	r3 := Record{SessionID: "s1", Method: "DELETE", Path: "/c", Ts: 3}

	s.Add(r1)
	s.Add(r2)
	s.Add(r3)

	all := s.List("")
	if len(all) != 3 {
		t.Fatalf("List('') = %d records, want 3", len(all))
	}

	s1 := s.List("s1")
	if len(s1) != 2 {
		t.Fatalf("List('s1') = %d records, want 2", len(s1))
	}
	if s1[0].Method != "GET" || s1[1].Method != "DELETE" {
		t.Errorf("List('s1') methods = %q, %q; want GET, DELETE", s1[0].Method, s1[1].Method)
	}

	s2 := s.List("s2")
	if len(s2) != 1 {
		t.Fatalf("List('s2') = %d records, want 1", len(s2))
	}
	if s2[0].Path != "/b" {
		t.Errorf("List('s2')[0].Path = %q, want /b", s2[0].Path)
	}

	none := s.List("nonexistent")
	if len(none) != 0 {
		t.Fatalf("List('nonexistent') = %d records, want 0", len(none))
	}
}

func TestListReturnsCopy(t *testing.T) {
	s := NewStore()
	s.Add(Record{SessionID: "s1", Method: "GET"})

	records := s.List("")
	records[0].Method = "MUTATED"

	again := s.List("")
	if again[0].Method == "MUTATED" {
		t.Error("List returned a reference; mutation leaked into store")
	}
}

func TestClearSession(t *testing.T) {
	s := NewStore()
	s.Add(Record{SessionID: "s1", Method: "GET"})
	s.Add(Record{SessionID: "s2", Method: "POST"})
	s.Add(Record{SessionID: "s1", Method: "DELETE"})

	s.ClearSession("s1")

	if len(s.List("s1")) != 0 {
		t.Error("ClearSession did not remove s1 records")
	}
	if len(s.List("s2")) != 1 {
		t.Error("ClearSession removed s2 records")
	}
	if len(s.List("")) != 1 {
		t.Error("ClearSession left wrong total count")
	}
}

func TestClear(t *testing.T) {
	s := NewStore()
	s.Add(Record{SessionID: "s1"})
	s.Add(Record{SessionID: "s2"})

	s.Clear()

	if len(s.List("")) != 0 {
		t.Error("Clear did not remove all records")
	}
}

func TestSubscribeReceivesRecords(t *testing.T) {
	s := NewStore()

	var mu sync.Mutex
	var received []Record
	unsub := s.Subscribe(func(r Record) {
		mu.Lock()
		received = append(received, r)
		mu.Unlock()
	})

	r1 := Record{SessionID: "s1", Method: "GET"}
	r2 := Record{SessionID: "s2", Method: "POST"}
	s.Add(r1)
	s.Add(r2)

	unsub()

	s.Add(Record{SessionID: "s3", Method: "PATCH"})

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("subscriber received %d records, want 2", len(received))
	}
	if received[0].Method != "GET" || received[1].Method != "POST" {
		t.Errorf("subscriber got methods %q, %q; want GET, POST", received[0].Method, received[1].Method)
	}
}

func TestSubscribeMultiple(t *testing.T) {
	s := NewStore()

	var mu sync.Mutex
	var count1, count2 int
	unsub1 := s.Subscribe(func(r Record) {
		mu.Lock()
		count1++
		mu.Unlock()
	})
	_ = s.Subscribe(func(r Record) {
		mu.Lock()
		count2++
		mu.Unlock()
	})

	s.Add(Record{SessionID: "s1"})
	unsub1()
	s.Add(Record{SessionID: "s2"})

	mu.Lock()
	defer mu.Unlock()
	if count1 != 1 {
		t.Errorf("subscriber1 called %d times, want 1", count1)
	}
	if count2 != 2 {
		t.Errorf("subscriber2 called %d times, want 2", count2)
	}
}
