package store

import (
	"context"
	"errors"
	"testing"
)

func TestFlows_CreateGet(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	doc := []byte("{\n  \"id\": \"order-api\"\n}\n")

	created, err := s.CreateFlow(ctx, "order-api", "Order API", doc)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	if created.Revision != 1 || created.Enabled {
		t.Errorf("created = %+v, want revision 1, not enabled", created)
	}

	got, err := s.GetFlow(ctx, "order-api")
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if string(got.Document) != string(doc) {
		t.Errorf("document = %q, want the saved bytes verbatim", got.Document)
	}
	if got.Name != "Order API" || got.Revision != 1 {
		t.Errorf("got = %+v", got)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("timestamps = %v / %v, want both %v", got.CreatedAt, got.UpdatedAt, created.CreatedAt)
	}
}

func TestFlows_CreateExisting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFlow(ctx, "f", "First", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFlow(ctx, "f", "Second", []byte(`2`)); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	got, _ := s.GetFlow(ctx, "f")
	if got.Name != "First" {
		t.Errorf("existing flow was changed: %+v", got)
	}
}

func TestFlows_GetUnknown(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetFlow(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFlows_SaveBumpsRevision(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	created, _ := s.CreateFlow(ctx, "f", "F", []byte(`1`))

	saved, err := s.SaveFlow(ctx, "f", "F2", []byte(`2`), 1)
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}
	if saved.Revision != 2 || saved.Name != "F2" || string(saved.Document) != "2" {
		t.Errorf("saved = %+v", saved)
	}
	if !saved.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updatedAt not advanced: %v", saved.UpdatedAt)
	}
	if !saved.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("createdAt changed: %v", saved.CreatedAt)
	}
}

func TestFlows_SaveConflict(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`1`))
	if _, err := s.SaveFlow(ctx, "f", "F", []byte(`2`), 1); err != nil {
		t.Fatal(err)
	}

	// A second editor still holds revision 1.
	if _, err := s.SaveFlow(ctx, "f", "F", []byte(`stale`), 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	got, _ := s.GetFlow(ctx, "f")
	if string(got.Document) != "2" || got.Revision != 2 {
		t.Errorf("stale save was written: %+v", got)
	}
}

func TestFlows_SaveUnknown(t *testing.T) {
	s := openTest(t)
	if _, err := s.SaveFlow(context.Background(), "nope", "", []byte(`1`), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFlows_List(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if got, err := s.ListFlows(ctx); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty list = %v, %v; want [], nil", got, err)
	}
	s.CreateFlow(ctx, "b", "Beta", []byte(`{}`))
	s.CreateFlow(ctx, "a2", "Alpha", []byte(`{}`))
	s.CreateFlow(ctx, "a1", "Alpha", []byte(`{}`))

	got, err := s.ListFlows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
		if f.Document != nil {
			t.Errorf("%s: list includes the document", f.ID)
		}
	}
	if want := "a1 a2 b"; joinIDs(ids) != want {
		t.Errorf("order = %v, want %s (by name, then ID)", ids, want)
	}
}

func TestFlows_SetEnabled(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))

	if err := s.SetEnabled(ctx, "f", true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFlow(ctx, "f")
	if !got.Enabled || got.Revision != 1 {
		t.Errorf("got = %+v, want enabled at revision 1", got)
	}
	s.SetEnabled(ctx, "f", false)
	if got, _ := s.GetFlow(ctx, "f"); got.Enabled {
		t.Error("still enabled")
	}
	if err := s.SetEnabled(ctx, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown flow err = %v, want ErrNotFound", err)
	}
}

func TestFlows_DeleteCascades(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))
	writeRun(t, s, "f", "e1", StatusCompleted)

	if err := s.DeleteFlow(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFlow(ctx, "f"); !errors.Is(err, ErrNotFound) {
		t.Errorf("flow still there: %v", err)
	}
	if _, _, err := s.GetExecution(ctx, "e1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("execution survived its flow: %v", err)
	}
	var events int
	s.db.QueryRow("SELECT count(*) FROM trace_events").Scan(&events)
	if events != 0 {
		t.Errorf("%d trace events survived their flow", events)
	}
	if err := s.DeleteFlow(ctx, "f"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += id
	}
	return out
}
