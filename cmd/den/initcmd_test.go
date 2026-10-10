package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/discover"
)

func TestPickBastion(t *testing.T) {
	inv := discover.Inventory{Bastions: []discover.Bastion{
		{InstanceID: "i-a", Name: "a", Online: true},
		{InstanceID: "i-b", Name: "b", Online: true},
		{InstanceID: "i-off", Name: "off"},
	}}
	if b, err := pickBastion(inv, "i-off", false); err != nil || b.InstanceID != "i-off" {
		t.Errorf("a named instance is used even when offline: %v %v", b, err)
	}
	if _, err := pickBastion(inv, "i-nope", false); err == nil {
		t.Error("an unknown --bastion must be an error")
	}
	if _, err := pickBastion(inv, "", false); err == nil || !strings.Contains(err.Error(), "i-a (a), i-b (b)") {
		t.Errorf("several candidates without a terminal must list them: %v", err)
	}
	inv.Bastions = inv.Bastions[1:]
	if b, err := pickBastion(inv, "", false); err != nil || b.InstanceID != "i-b" {
		t.Errorf("the only online instance is chosen: %v %v", b, err)
	}
	if _, err := pickBastion(discover.Inventory{}, "", false); err == nil {
		t.Error("no bastion at all must be an error")
	}
}

func TestWriteGeneratedNeverOverwritesUnasked(t *testing.T) {
	var stdout bytes.Buffer
	if where, err := writeGenerated(&stdout, "-", "x: 1\n", false); err != nil || where != "" || stdout.String() != "x: 1\n" {
		t.Fatalf("stdout: %q %v %q", where, err, stdout.String())
	}
	path := filepath.Join(t.TempDir(), "sub", "den.yaml")
	if _, err := writeGenerated(&stdout, path, "a: 1\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGenerated(&stdout, path, "b: 2\n", false); err == nil {
		t.Fatal("an existing file must not be overwritten without --force")
	}
	if b, _ := os.ReadFile(path); string(b) != "a: 1\n" {
		t.Fatalf("file changed: %q", b)
	}
	if _, err := writeGenerated(&stdout, path, "b: 2\n", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "b: 2\n" {
		t.Fatalf("--force did not overwrite: %q", b)
	}
}
