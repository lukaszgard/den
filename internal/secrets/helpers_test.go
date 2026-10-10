package secrets

import (
	"reflect"
	"testing"
)

func key(k string) PathSeg { return PathSeg{Key: k} }
func idx(i int) PathSeg    { return PathSeg{Index: i, IsIndex: true} }

func TestFlattenJSONKeepsPaths(t *testing.T) {
	got, err := FlattenJSON([]byte(`{
		"client.aacn": {"apiKey": "k", "basicauth.clientId": "c"},
		"hosts": ["a", "b"],
		"users": [{"name": "n", "pass": "p"}],
		"": "empty key"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Field{
		// A dotted key stays one segment: the Key alone cannot tell.
		{Key: "client.aacn.apiKey", Value: "k", Path: []PathSeg{key("client.aacn"), key("apiKey")}},
		{Key: "client.aacn.basicauth.clientId", Value: "c", Path: []PathSeg{key("client.aacn"), key("basicauth.clientId")}},
		{Key: "hosts[0]", Value: "a", Path: []PathSeg{key("hosts"), idx(0)}},
		{Key: "hosts[1]", Value: "b", Path: []PathSeg{key("hosts"), idx(1)}},
		{Key: "users[0].name", Value: "n", Path: []PathSeg{key("users"), idx(0), key("name")}},
		{Key: "users[0].pass", Value: "p", Path: []PathSeg{key("users"), idx(0), key("pass")}},
		{Key: "", Value: "empty key", Path: []PathSeg{key("")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flatten =\n%+v\nwant\n%+v", got, want)
	}
}

// Siblings are built by appending to one parent path. Three levels down that
// path has spare capacity, so without a copy every sibling would share one
// backing array and end up with the last sibling's key.
func TestFlattenJSONPathsAreNotShared(t *testing.T) {
	got, err := FlattenJSON([]byte(`{"a": {"b": {"c": {"x": "1", "y": "2", "z": "3"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"x", "y", "z"} {
		if k := got[i].Path[3].Key; k != want {
			t.Errorf("field %d last segment = %q, want %q", i, k, want)
		}
	}
}

func TestSegmentsOfAFlatField(t *testing.T) {
	f := Field{Key: "db.password", Value: "x"}
	if got := f.Segments(); !reflect.DeepEqual(got, []PathSeg{key("db.password")}) {
		t.Errorf("Segments = %+v, want the Key as its only segment", got)
	}
}
