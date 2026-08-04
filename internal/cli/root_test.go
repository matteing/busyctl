package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/matteing/busyctl/internal/applemusic"
	"github.com/matteing/busyctl/internal/clock"
	"github.com/matteing/busyctl/internal/codextokens"
	"github.com/matteing/busyctl/internal/muni"
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
	if text := output.String(); !strings.Contains(text, "music") || !strings.Contains(text, "clock") || !strings.Contains(text, "tokens") || !strings.Contains(text, "muni") || !strings.Contains(text, "--host") {
		t.Fatalf("root help is incomplete:\n%s", text)
	}
}

func TestCodexTokenFlagsReachRunner(t *testing.T) {
	var captured codextokens.Config
	command := newRootCommandWithApps(nil, nil, nil, func(_ context.Context, config codextokens.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{"tokens", "--view", "count", "--database", "/tmp/codex.sqlite", "--poll", "5s", "--priority", "41", "--keep-display"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if captured.Database != "/tmp/codex.sqlite" || captured.PollInterval != 5*time.Second || captured.Priority != 41 || captured.View != codextokens.ViewCount || !captured.KeepDisplay {
		t.Fatalf("Codex token config = %#v", captured)
	}
}

func TestMuniLocationReachesRunner(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "")
	t.Setenv("BUSYBAR_TOKEN", "")
	var captured muni.Config
	command := newRootCommandWithMuni(nil, nil, func(_ context.Context, config muni.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{"muni", "37.7694,-122.3875", "--priority", "42", "--keep-display"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if captured.Location != "37.7694,-122.3875" || captured.Priority != 42 || !captured.KeepDisplay {
		t.Fatalf("Muni config = %#v", captured)
	}
}

func TestClockDefaultsReachRunner(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "")
	t.Setenv("BUSYBAR_TOKEN", "")
	var captured clock.Config
	command := newRootCommand(nil, func(_ context.Context, config clock.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{"clock"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := clock.DefaultConfig()
	if captured != want {
		t.Fatalf("default clock config = %#v, want %#v", captured, want)
	}
}

func TestClockFlagsReachRunner(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "busybar.local")
	t.Setenv("BUSYBAR_TOKEN", "from-environment")
	var captured clock.Config
	command := newRootCommand(nil, func(_ context.Context, config clock.Config) error {
		captured = config
		return nil
	})
	command.SetArgs([]string{
		"clock",
		"--host", "192.0.2.40",
		"--token", "from-flag",
		"--priority", "42",
		"--keep-display",
		"--12-hour",
		"--seconds=false",
		"--blink-colon",
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := clock.Config{
		Host:        "192.0.2.40",
		Token:       "from-flag",
		Priority:    42,
		KeepDisplay: true,
		TwelveHour:  true,
		ShowSeconds: false,
		BlinkColon:  true,
	}
	if captured != want {
		t.Fatalf("clock flag config = %#v, want %#v", captured, want)
	}
}

func TestInvalidClockPriorityNeverRuns(t *testing.T) {
	called := false
	command := newRootCommand(nil, func(context.Context, clock.Config) error {
		called = true
		return nil
	})
	command.SetArgs([]string{"clock", "--priority", "0"})
	if err := command.ExecuteContext(context.Background()); err == nil {
		t.Fatal("invalid clock priority unexpectedly succeeded")
	}
	if called {
		t.Fatal("invalid clock priority invoked runner")
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
	command.SetArgs([]string{"music"})
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
		"music",
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
	help.SetArgs([]string{"music", "--help"})
	if err := help.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "do-not-print-this-token") {
		t.Fatalf("help exposed BUSYBAR_TOKEN:\n%s", output.String())
	}
}

func TestInvalidAppleMusicInputNeverRuns(t *testing.T) {
	for name, args := range map[string][]string{
		"view":        {"music", "--view", "both"},
		"positionals": {"music", "unexpected"},
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
