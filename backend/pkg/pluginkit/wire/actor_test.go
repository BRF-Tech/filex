package wire

import (
	"encoding/json"
	"testing"
)

// Actor.Permissions (filex 0.49.0): the ids of the app's own
// user_permissions the person holds. Can asks it; a filex that does not say
// (older) and a call with no person (a public page) hold nothing.
func TestActor_Can(t *testing.T) {
	var nobody *Actor
	if nobody.Can("request") {
		t.Fatal("a call with no actor holds nothing")
	}
	a := &Actor{ID: 7, Permissions: []string{"request", "audit"}}
	if !a.Can("request") || !a.Can("audit") {
		t.Fatalf("%+v should hold request and audit", a)
	}
	if a.Can("app.sign.request") || a.Can("") || a.Can("Request") {
		t.Fatal("Can takes the manifest's own id, exactly")
	}
	if (&Actor{ID: 7}).Can("request") {
		t.Fatal("an actor the host said nothing about holds nothing")
	}
}

// Backward compatible both ways: an actor from a filex before 0.49.0 reads as
// holding nothing, and an actor that holds nothing is written without the
// field, so a module built before it reads the same bytes it always did.
func TestActor_PermissionsOnTheWire(t *testing.T) {
	var in ActionRunInput
	if err := json.Unmarshal([]byte(`{"job_id":"j","action_id":"a","inputs":[],"output":{"mode":"none"},"actor":{"id":7,"email":"ada@test.local","role":"user"}}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Actor.Permissions != nil || in.Actor.Can("request") {
		t.Fatalf("an older host's actor: %+v", in.Actor)
	}
	var ev ViewEventInput
	if err := json.Unmarshal([]byte(`{"view_id":"v","event":"open","context":{"actor":{"id":7,"permissions":["request"]}}}`), &ev); err != nil {
		t.Fatal(err)
	}
	if !ev.Context.Actor.Can("request") {
		t.Fatalf("a view event's actor: %+v", ev.Context.Actor)
	}
	var page ViewEventInput
	if err := json.Unmarshal([]byte(`{"view_id":"p","event":"open","context":{"locale":"en"}}`), &page); err != nil {
		t.Fatal(err)
	}
	if page.Context.Actor.Can("request") {
		t.Fatal("a public page's event has no actor")
	}
	b, err := json.Marshal(Actor{ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"id":7}` {
		t.Fatalf("an actor holding nothing is written as before: %s", b)
	}
	b, _ = json.Marshal(Actor{ID: 7, Permissions: []string{"request"}})
	if string(b) != `{"id":7,"permissions":["request"]}` {
		t.Fatalf("got %s", b)
	}
}
