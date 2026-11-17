package admincli

import (
	"fmt"
	"io"
	"strings"
)

const (
	minPasswordLength = 8
	maxPasswordLength = 128
)

// readPasswordStdin reads at most one bounded password record. Exactly one
// trailing LF (optionally preceded by CR) is accepted. Other whitespace is
// preserved because it is valid password material; it is never trimmed.
func readPasswordStdin(r io.Reader) (string, error) {
	if r == nil {
		return "", ErrPasswordEmpty
	}
	data, err := io.ReadAll(io.LimitReader(r, maxPasswordLength+2))
	if err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	if len(data) > maxPasswordLength+2 {
		return "", ErrPasswordTooLong
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}
	if len(data) > maxPasswordLength {
		return "", ErrPasswordTooLong
	}
	if len(data) == 0 {
		return "", ErrPasswordEmpty
	}
	if strings.ContainsAny(string(data), "\r\n") {
		return "", ErrPasswordStdinFormat
	}
	password := string(data)
	if err := validateNewPassword(password); err != nil {
		return "", err
	}
	return password, nil
}

func validateNewPassword(password string) error {
	switch {
	case len(password) == 0:
		return ErrPasswordEmpty
	case len(password) < minPasswordLength:
		return ErrPasswordTooShort
	case len(password) > maxPasswordLength:
		return ErrPasswordTooLong
	case strings.IndexByte(password, 0) >= 0:
		return ErrPasswordInvalid
	default:
		return nil
	}
}

func (r *Runner) readNewPassword(fromStdin bool) (string, error) {
	if fromStdin {
		return readPasswordStdin(r.In)
	}
	if r.IsTerminal == nil || !r.IsTerminal() {
		return "", ErrPasswordInputRequired
	}
	if r.ReadSecret == nil {
		return "", fmt.Errorf("terminal password reader is not configured")
	}

	password, err := r.ReadSecret("New password: ")
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	confirmation, err := r.ReadSecret("Confirm password: ")
	if err != nil {
		return "", fmt.Errorf("read password confirmation: %w", err)
	}
	if password != confirmation {
		return "", ErrPasswordMismatch
	}
	if err := validateNewPassword(password); err != nil {
		return "", err
	}
	return password, nil
}
