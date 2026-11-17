package admincli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

func noRuntimeRunner(t *testing.T) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	runner := &Runner{
		In:  strings.NewReader(""),
		Out: &out,
		Err: &errOut,
		LoadConfig: func() (*config.Config, error) {
			t.Fatal("help/usage parsing must not load config")
			return nil, nil
		},
		OpenDB: func(context.Context, *config.PostgresConfig) (*postgres.DB, error) {
			t.Fatal("help/usage parsing must not open PostgreSQL")
			return nil, nil
		},
	}
	return runner, &out, &errOut
}

func TestAdminHelpDoesNotLoadConfigOrDatabase(t *testing.T) {
	for _, args := range [][]string{
		{"admin", "--help"},
		{"admin", "-h"},
		{"--help"},
		{"help"},
		{"admin", "help"},
		{"admin", "create", "--help"},
		{"admin", "promote", "--help"},
		{"admin", "reset-password", "--help"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			runner, out, errOut := noRuntimeRunner(t)
			if code := runner.Run(context.Background(), args); code != 0 {
				t.Fatalf("Run(%v) = %d, want 0; stderr=%s", args, code, errOut.String())
			}
			if out.Len() == 0 {
				t.Fatalf("Run(%v) printed no help to stdout", args)
			}
			if errOut.Len() != 0 {
				t.Fatalf("Run(%v) wrote stderr on help: %s", args, errOut.String())
			}
		})
	}
}

func TestAdminCreateHelpDocumentsSafePasswordInputs(t *testing.T) {
	runner, out, _ := noRuntimeRunner(t)
	if code := runner.Run(context.Background(), []string{"admin", "create", "--help"}); code != 0 {
		t.Fatalf("create help exit = %d", code)
	}
	text := out.String()
	for _, want := range []string{"--username", "--email", "--password-stdin", "hidden"} {
		if !strings.Contains(text, want) {
			t.Fatalf("create help missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "plaintext password flag") {
		t.Fatalf("create help advertises a plaintext password flag:\n%s", text)
	}
}

func TestAdminTopLevelUnknownCommandIsUsageErrorWithoutRuntime(t *testing.T) {
	for _, args := range [][]string{
		{"not-a-command"},
		{"--version"},
		{"admin", "not-a-command"},
		{"admin", "list", "--not-a-flag"},
		{"admin", "create", "--username", "admin"},
		{"admin", "create", "--username", "admin", "--email", "not-an-email"},
		{"admin", "promote"},
		{"admin", "promote", "--username", "a", "--id", "00000000-0000-0000-0000-000000000000"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			runner, out, errOut := noRuntimeRunner(t)
			if code := runner.Run(context.Background(), args); code != 2 {
				t.Fatalf("Run(%v) = %d, want usage exit 2; stdout=%s stderr=%s", args, code, out.String(), errOut.String())
			}
			if strings.TrimSpace(errOut.String()) == "" {
				t.Fatalf("Run(%v) did not write a clear stderr error", args)
			}
		})
	}
}

func TestAdminNoSubcommandIsUsageError(t *testing.T) {
	runner, _, errOut := noRuntimeRunner(t)
	if code := runner.Run(context.Background(), []string{"admin"}); code != 2 {
		t.Fatalf("admin with no subcommand = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage") && !strings.Contains(errOut.String(), "subcommand") {
		t.Fatalf("admin no-subcommand error is unclear: %s", errOut.String())
	}
}
