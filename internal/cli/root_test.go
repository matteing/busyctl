package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matteing/busyctl/internal/applemusic"
)

func TestRootHelpDoesNotRunAnApp(t *testing.T) {
	called := false
	command := NewRootCommand(func(context.Context, applemusic.Config) error {
		called = true
		return nil
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(nil)
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("root help contacted an app runner")
	}
	if text := output.String(); !strings.Contains(text, "apple-music") || !strings.Contains(text, "--host") {
		t.Fatalf("root help is incomplete:\n%s", text)
	}
}

func TestAppleMusicDefaultsReachRunner(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "")
	t.Setenv("BUSYBAR_TOKEN", "")
	var captured applemusic.Config
	command := NewRootCommand(func(_ context.Context, config applemusic.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{"apple-music"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := applemusic.DefaultConfig()
	if captured != want {
		t.Fatalf("default config = %#v, want %#v", captured, want)
	}
}

func TestAppleMusicFlagsAndEnvironmentReachRunner(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "busybar.local")
	t.Setenv("BUSYBAR_TOKEN", "from-environment")
	var captured applemusic.Config
	command := NewRootCommand(func(_ context.Context, config applemusic.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{
		"apple-music",
		"--host", "192.0.2.25",
		"--token", "from-flag",
		"--priority", "42",
		"--keep-display",
		"--source", "https://example.test/playing",
		"--view", applemusic.ViewVisualizer,
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := applemusic.Config{
		Host:        "192.0.2.25",
		Token:       "from-flag",
		Source:      "https://example.test/playing",
		Priority:    42,
		KeepDisplay: true,
		View:        applemusic.ViewVisualizer,
	}
	if captured != want {
		t.Fatalf("flag config = %#v, want %#v", captured, want)
	}
}

func TestAppleMusicEnvironmentDefaultsStaySecret(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "busybar.test")
	t.Setenv("BUSYBAR_TOKEN", "do-not-print-this-token")

	var captured applemusic.Config
	command := NewRootCommand(func(_ context.Context, config applemusic.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{"applemusic"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if captured.Host != "busybar.test" || captured.Token != "do-not-print-this-token" {
		t.Fatalf("environment config = %#v", captured)
	}

	var output bytes.Buffer
	help := NewRootCommand(nil)
	help.SetOut(&output)
	help.SetErr(&output)
	help.SetArgs([]string{"apple-music", "--help"})
	if err := help.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "do-not-print-this-token") {
		t.Fatalf("help exposed BUSYBAR_TOKEN:\n%s", output.String())
	}
}

func TestInvalidAppleMusicInputNeverRuns(t *testing.T) {
	for name, args := range map[string][]string{
		"view":        {"apple-music", "--view", "both"},
		"positionals": {"apple-music", "unexpected"},
		"unknown":     {"not-an-app"},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			command := NewRootCommand(func(context.Context, applemusic.Config) error {
				called = true
				return nil
			})
			command.SetArgs(args)
			if err := command.ExecuteContext(context.Background()); err == nil {
				t.Fatalf("%v unexpectedly succeeded", args)
			}
			if called {
				t.Fatalf("%v invoked the runner", args)
			}
		})
	}
}

func TestCommandContextAndRunnerErrorsPropagate(t *testing.T) {
	sentinel := errors.New("runner failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := NewRootCommand(func(ctx context.Context, _ applemusic.Config) error {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("runner context error = %v, want canceled", ctx.Err())
		}
		return sentinel
	})
	command.SetArgs([]string{"music"})
	if err := command.ExecuteContext(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, sentinel)
	}
}
