package notify

import (
	"os"
	"testing"
)

// taskCreatedFixture is a delivery recorded from a Vikunja v2.7.0 instance
// (see internal/webhook/fixture_test.go). Its doer has id 1.
const taskCreatedFixture = "../webhook/testdata/task_created.json"

func loadFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(taskCreatedFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func TestMapTaskCreated(t *testing.T) {
	n, ok, err := Map(loadFixture(t), 2)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if !ok {
		t.Fatal("event by another user was dropped")
	}
	want := Notification{Event: TaskCreated, TaskTitle: "Example task", ProjectName: "Inbox"}
	if n != want {
		t.Fatalf("Map = %+v, want %+v", n, want)
	}
}

func TestMapDropsSelfCausedProjectEvent(t *testing.T) {
	// The fixture's doer is user 1, so registering user 1 makes the event self-caused.
	if _, ok, err := Map(loadFixture(t), 1); err != nil || ok {
		t.Fatalf("Map = ok %v, err %v; want dropped with no error", ok, err)
	}
}

func TestMapIgnoresUnsupportedEvent(t *testing.T) {
	body := []byte(`{"event_name":"project.deleted","data":{}}`)
	if _, ok, err := Map(body, 1); err != nil || ok {
		t.Fatalf("Map = ok %v, err %v; want dropped with no error", ok, err)
	}
}

func TestMapRejectsMalformedJSON(t *testing.T) {
	if _, _, err := Map([]byte(`{`), 1); err == nil {
		t.Fatal("Map accepted malformed JSON")
	}
}
