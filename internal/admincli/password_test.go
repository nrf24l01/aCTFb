package admincli

import (
	"errors"
	"strings"
	"testing"
)

func TestReadPasswordStdinBoundedSingleRecord(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "newline", input: "correct horse battery\n", want: "correct horse battery"},
		{name: "crlf", input: "correct horse battery\r\n", want: "correct horse battery"},
		{name: "no trailing newline", input: "correct horse battery", want: "correct horse battery"},
		{name: "valid spaces are not trimmed", input: "  pass phrase  \n", want: "  pass phrase  "},
		{name: "all spaces remain a password", input: "        \n", want: "        "},
		{name: "empty", input: "\n", wantErr: ErrPasswordEmpty},
		{name: "empty eof", input: "", wantErr: ErrPasswordEmpty},
		{name: "extra record", input: "first password\nsecond password\n", wantErr: ErrPasswordStdinFormat},
		{name: "embedded newline", input: "first\npassword\n", wantErr: ErrPasswordStdinFormat},
		{name: "carriage return inside", input: "first\rpassword\n", wantErr: ErrPasswordStdinFormat},
		{name: "oversized", input: strings.Repeat("x", 129) + "\n", wantErr: ErrPasswordTooLong},
		{name: "very oversized", input: strings.Repeat("x", 64*1024), wantErr: ErrPasswordTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readPasswordStdin(strings.NewReader(tc.input))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("readPasswordStdin() error = %v, want %v", err, tc.wantErr)
				}
				if got != "" {
					t.Fatalf("readPasswordStdin() returned plaintext on error: %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readPasswordStdin() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("readPasswordStdin() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateNewPasswordPolicyRejectsBlankAndNUL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr error
	}{
		{name: "valid", value: "SecretPass123!"},
		{name: "spaces pass unchanged", value: "        "},
		{name: "blank", value: "", wantErr: ErrPasswordEmpty},
		{name: "short", value: "short", wantErr: ErrPasswordTooShort},
		{name: "too long", value: strings.Repeat("a", 129), wantErr: ErrPasswordTooLong},
		{name: "NUL", value: "SecretPass\x00123!", wantErr: ErrPasswordInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNewPassword(tc.value)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("validateNewPassword() error = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validateNewPassword() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestPromptNewPasswordConfirmationMismatchDoesNotReturnSecret(t *testing.T) {
	var prompts []string
	runner := &Runner{
		IsTerminal: func() bool { return true },
		ReadSecret: func(prompt string) (string, error) {
			prompts = append(prompts, prompt)
			if len(prompts) == 1 {
				return "SecretPass123!", nil
			}
			return "different password", nil
		},
	}
	password, err := runner.readNewPassword(false)
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("readNewPassword() error = %v, want ErrPasswordMismatch", err)
	}
	if password != "" {
		t.Fatalf("readNewPassword() returned %q on mismatch", password)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompt count = %d, want 2", len(prompts))
	}
}

func TestNonTTYWithoutPasswordStdinFailsInsteadOfBlocking(t *testing.T) {
	runner := &Runner{
		IsTerminal: func() bool { return false },
		ReadSecret: func(string) (string, error) {
			t.Fatal("non-TTY must not read a hidden prompt")
			return "", nil
		},
	}
	_, err := runner.readNewPassword(false)
	if !errors.Is(err, ErrPasswordInputRequired) {
		t.Fatalf("readNewPassword() error = %v, want ErrPasswordInputRequired", err)
	}
	if !strings.Contains(err.Error(), "--password-stdin") {
		t.Fatalf("non-TTY error does not mention --password-stdin: %v", err)
	}
}
