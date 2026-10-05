package core

import (
	"fmt"
	"testing"
	"time"
)

func TestPendingBounded(t *testing.T) {
	var p pending[int]
	exp := time.Now().Add(time.Minute)
	for i := range maxPending * 3 {
		p.put(fmt.Sprint(i), i, exp.Add(time.Duration(i)))
	}
	if len(p.m) > maxPending {
		t.Fatalf("pending grew to %d", len(p.m))
	}
	last := fmt.Sprint(maxPending*3 - 1)
	if v, _, ok := p.take(last); !ok || v != maxPending*3-1 {
		t.Fatal("newest entry evicted")
	}
}
