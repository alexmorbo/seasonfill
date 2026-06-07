package regrab

import "testing"

func TestUseCase_ConstructorWiresRuntimeStateStore(t *testing.T) {
	u := NewUseCase(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if u.state == nil {
		t.Fatal("NewUseCase must wire a RuntimeStateStore")
	}
	if u.SnapshotAll() == nil {
		t.Fatal("SnapshotAll should never return nil")
	}
	if _, ok := u.Snapshot("anything"); ok {
		t.Fatal("Snapshot on fresh use case must be (zero, false)")
	}
}

func TestUseCase_ForgetStateRemovesEntry(t *testing.T) {
	u := NewUseCase(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	u.state.Stamp("alpha", RuntimeState{Watched: 7})
	u.ForgetState("alpha")
	if _, ok := u.Snapshot("alpha"); ok {
		t.Fatal("ForgetState did not drop the entry")
	}
}
