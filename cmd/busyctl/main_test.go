package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAppsCommand(t *testing.T) {
	var output bytes.Buffer
	if err := execute(context.Background(), []string{"apps"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "apple-music") {
		t.Fatalf("apps output = %q", output.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var output bytes.Buffer
	if err := execute(context.Background(), []string{"version"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(output.String()); got != "busyctl "+version {
		t.Fatalf("version output = %q", got)
	}
}

func TestSelectAppByName(t *testing.T) {
	var output bytes.Buffer
	selected, err := selectApp(strings.NewReader("apple-music\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if selected.name != "apple-music" {
		t.Fatalf("selected %q", selected.name)
	}
}

func TestUnknownAppSuggestsAppsCommand(t *testing.T) {
	err := execute(context.Background(), []string{"nope"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "busyctl apps") {
		t.Fatalf("error = %v", err)
	}
}
