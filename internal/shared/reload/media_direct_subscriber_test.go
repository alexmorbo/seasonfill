package reload

import (
	"context"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestMediaDirectSubscriber_Apply(t *testing.T) {
	var got []bool
	s := NewMediaDirectSubscriber(func(v bool) { got = append(got, v) }, nil)
	_ = s.apply(context.Background(), runtime.Snapshot{MediaDirect: true})
	_ = s.apply(context.Background(), runtime.Snapshot{MediaDirect: false})
	if len(got) != 2 || got[0] != true || got[1] != false {
		t.Fatalf("apply forwarding = %v", got)
	}
	// nil set must not panic.
	_ = NewMediaDirectSubscriber(nil, nil).apply(context.Background(), runtime.Snapshot{MediaDirect: true})
}
