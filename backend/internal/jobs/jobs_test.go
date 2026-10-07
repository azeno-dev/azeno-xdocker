package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/azeno/xdocker/internal/store"
)

func TestRunnerLifecycle(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	rn := New(ctx, st, 1)

	done := make(chan store.Job, 2)
	id, err := rn.Start(store.Job{Type: "test", Title: "ok"}, func(ctx context.Context, r *Run) (string, error) {
		r.SetTotal(2)
		r.Item(store.JobItem{Target: "local", Ref: "a:1", Action: "delete", Result: "deleted", Bytes: 10})
		r.Item(store.JobItem{Target: "local", Ref: "a:2", Action: "delete", Result: "skipped"})
		r.Logf("hello %d", 1)
		return "1 deleted", nil
	}, func(j store.Job) { done <- j })
	if err != nil {
		t.Fatal(err)
	}
	j := <-done
	if j.Status != "done" || j.Done != 2 || j.Bytes != 10 || j.Summary != "1 deleted" {
		t.Fatalf("job = %+v", j)
	}
	saved, _ := st.Job(ctx, id)
	items, _ := st.JobItems(ctx, id)
	if saved.Status != "done" || len(items) != 2 {
		t.Fatalf("saved = %+v items = %d", saved, len(items))
	}

	rn.Start(store.Job{Type: "test", Title: "fail"}, func(ctx context.Context, r *Run) (string, error) {
		return "", errors.New("boom")
	}, func(j store.Job) { done <- j })
	if j := <-done; j.Status != "failed" || j.Summary != "boom" {
		t.Fatalf("failed job = %+v", j)
	}

	rn.Start(store.Job{Type: "test", Title: "panic"}, func(ctx context.Context, r *Run) (string, error) {
		panic("oops")
	}, func(j store.Job) { done <- j })
	if j := <-done; j.Status != "failed" {
		t.Fatalf("panic job = %+v", j)
	}
	rn.Wait()
}
