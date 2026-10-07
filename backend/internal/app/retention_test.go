package app

import (
	"testing"

	"github.com/azeno/xdocker/internal/retention"
)

func TestCountLocalBytes(t *testing.T) {
	rows := []ImageRow{
		{ID: "a", Ref: "x:1"}, {ID: "a", Ref: "busybox:1"}, // a has a tag outside the policy
		{ID: "b", Ref: "x:2"}, {ID: "b", Ref: "x:old"}, // both tags of b are deleted
		{ID: "c", Ref: "x:3"},
	}
	res := retention.Result{Decisions: []retention.Decision{
		{Item: retention.Item{Digest: "a", Size: 100}, Action: retention.Delete},
		{Item: retention.Item{Digest: "b", Size: 200}, Action: retention.Delete},
		{Item: retention.Item{Digest: "b", Size: 200}, Action: retention.Delete},
		{Item: retention.Item{Digest: "c", Size: 300}, Action: retention.Keep},
	}}
	countLocalBytes(&res, rows)
	if res.DeleteSize != 200 {
		t.Fatalf("delete size = %d, want 200", res.DeleteSize)
	}
	if res.Decisions[0].Size != 0 || res.Decisions[1].Size != 200 || res.Decisions[2].Size != 0 {
		t.Fatalf("sizes = %d %d %d", res.Decisions[0].Size, res.Decisions[1].Size, res.Decisions[2].Size)
	}
}

func TestProtectSharedDigests(t *testing.T) {
	res := retention.Result{
		Decisions: []retention.Decision{
			{Item: retention.Item{Repo: "r", Tag: "stable", Digest: "d1"}, Action: retention.Keep},
			{Item: retention.Item{Repo: "r", Tag: "1.0", Digest: "d1", Size: 10}, Action: retention.Delete},
			{Item: retention.Item{Repo: "r", Tag: "0.9", Digest: "d2", Size: 10}, Action: retention.Delete},
		},
		KeepCount: 1, DeleteCount: 2, DeleteSize: 20,
	}
	protectSharedDigests(&res)
	if res.Decisions[1].Action != retention.Keep || res.DeleteCount != 1 || res.DeleteSize != 10 || res.KeepCount != 2 {
		t.Fatalf("res = %+v", res)
	}
}
