package differential

import (
	"reflect"
	"testing"
)

func TestCanonicalRules(t *testing.T) {
	if got := canonical([]byte("x")); got != "x" {
		t.Fatalf("canonical bytes = %#v", got)
	}
	unordered := []any{"b", "a", "c"}
	if got := unorderedCanonical(unordered); !reflect.DeepEqual(got, []any{"a", "b", "c"}) {
		t.Fatalf("unorderedCanonical = %#v", got)
	}
	if got := unorderedCanonical([]any{unordered[1], unordered[0], unordered[2]}); !reflect.DeepEqual(got, unorderedCanonical(unordered)) {
		t.Fatalf("unorderedCanonical is order-dependent: %#v vs %#v", got, unorderedCanonical(unordered))
	}

	hash := []any{"b", "2", "a", "1"}
	wantHash := []any{"a", "1", "b", "2"}
	if got := hashCanonical(hash); !reflect.DeepEqual(got, wantHash) {
		t.Fatalf("hashCanonical = %#v, want %#v", got, wantHash)
	}
	if got := hashCanonical([]any{"a", "1", "odd"}); !reflect.DeepEqual(got, []any{"a", "1", "odd"}) {
		t.Fatalf("hashCanonical should leave odd arrays alone: %#v", got)
	}

	if got := ttlCanonical(int64(999)); got != int64(1) {
		t.Fatalf("ttlCanonical positive = %#v", got)
	}
	for _, sentinel := range []int64{-1, -2, 0} {
		if got := ttlCanonical(sentinel); got != sentinel {
			t.Fatalf("ttlCanonical(%d) = %#v", sentinel, got)
		}
	}
	if got := ttlCanonical("error: ERR no"); got != "error: ERR no" {
		t.Fatalf("ttlCanonical error = %#v", got)
	}
}
