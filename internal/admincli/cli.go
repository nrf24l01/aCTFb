package admincli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const adminUsage = `Usage:
  backend admin <command> [flags]

Commands:
  create          Create an enabled, email-verified admin account
  list            List users with UUID, username, email, and status
  promote         Grant the admin role to an existing user
  demote          Remove the admin role while keeping the account
  reset-password  Reset an admin password and revoke its sessions

Run "backend admin <command> --help" for command flags.
Default "backend" with no arguments starts the HTTP server unchanged.
`

const createUsage = `Usage:
  backend admin create --username <name> --email <address> [--password-stdin]

Creates an enabled, email-verified user with roles user,admin.
The password is read with hidden terminal input and confirmation when
--password-stdin is omitted. --password-stdin reads one bounded line from
stdin for non-interactive automation; it never accepts a password in argv or
an environment variable.
`

const listUsage = `Usage:
  backend admin list

Prints UUID, username, email, and enabled/disabled + admin/user status.
Password hashes and tokens are never printed.
`

const promoteHelp = `Usage:
  backend admin promote (--username <name> | --id <uuid>)

Grants the admin role to an existing enabled user and preserves its other
roles. Already-admin accounts are reported as unchanged.
`

const demoteHelp = `Usage:
  backend admin demote (--username <name> | --id <uuid>)

Removes the admin role while keeping the account and its other roles. The last
enabled admin cannot be demoted. Already-non-admin accounts are reported as
unchanged.
`

const resetHelp = `Usage:
  backend admin reset-password (--username <name> | --id <uuid>) [--password-stdin]

Resets the password of an existing admin account, bumps auth_version, and
revokes all refresh sessions. The password is read with hidden terminal input
and confirmation when --password-stdin is omitted. --password-stdin reads one
bounded line from stdin and never accepts a password in argv or an environment
variable.
`

func NewRunner() *Runner {
	runner := &Runner{
		In:         os.Stdin,
		Out:        os.Stdout,
		Err:        os.Stderr,
		LoadConfig: config.LoadForAdmin,
		OpenDB:     postgres.NewPostgresClient,
	}
	runner.IsTerminal = func() bool {
		file, ok := runner.In.(*os.File)
		return ok && term.IsTerminal(int(file.Fd()))
	}
	runner.ReadSecret = func(prompt string) (string, error) {
		file, ok := runner.In.(*os.File)
		if !ok {
			return "", ErrPasswordInputRequired
		}
		fmt.Fprint(runner.Err, prompt)
		value, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(runner.Err)
		if err != nil {
			return "", err
		}
		return string(value), nil
	}
	return runner
}

func (r *Runner) Run(ctx context.Context, args []string) int {
	r.ensureStreams()
	if len(args) == 0 {
		fmt.Fprint(r.Err, adminUsage)
		return 2
	}
	if isHelp(args[0]) {
		fmt.Fprint(r.Out, adminUsage)
		return 0
	}
	if args[0] != "admin" {
		fmt.Fprintf(r.Err, "backend: unknown command %q\n\n%s", args[0], adminUsage)
		return 2
	}
	if len(args) == 1 {
		fmt.Fprint(r.Err, "backend admin: a subcommand is required\n\n"+adminUsage)
		return 2
	}
	subcommand := args[1]
	rest := args[2:]
	if isHelp(subcommand) {
		fmt.Fprint(r.Out, adminUsage)
		return 0
	}
	if subcommand == "help" {
		if len(rest) == 0 {
			fmt.Fprint(r.Out, adminUsage)
			return 0
		}
		return r.printCommandHelp(rest[0])
	}

	switch subcommand {
	case "create":
		return r.runCreate(ctx, rest)
	case "list":
		return r.runList(ctx, rest)
	case "promote":
		return r.runRoleChange(ctx, rest, true)
	case "demote":
		return r.runRoleChange(ctx, rest, false)
	case "reset-password":
		return r.runResetPassword(ctx, rest)
	default:
		fmt.Fprintf(r.Err, "backend admin: unknown subcommand %q\n\n%s", subcommand, adminUsage)
		return 2
	}
}

func (r *Runner) printCommandHelp(command string) int {
	switch command {
	case "create":
		fmt.Fprint(r.Out, createUsage)
	case "list":
		fmt.Fprint(r.Out, listUsage)
	case "promote":
		fmt.Fprint(r.Out, promoteHelp)
	case "demote":
		fmt.Fprint(r.Out, demoteHelp)
	case "reset-password":
		fmt.Fprint(r.Out, resetHelp)
	default:
		fmt.Fprintf(r.Err, "backend admin: unknown subcommand %q\n\n%s", command, adminUsage)
		return 2
	}
	return 0
}

func (r *Runner) runCreate(ctx context.Context, args []string) int {
	fs := newFlagSet("admin create")
	username := fs.String("username", "", "username")
	email := fs.String("email", "", "email address")
	passwordStdin := fs.Bool("password-stdin", false, "read one password line from stdin")
	if ok, code := r.parseFlags(fs, args, createUsage); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return r.usageError("create does not accept positional arguments", createUsage)
	}
	trimmedUsername := strings.TrimSpace(*username)
	trimmedEmail := strings.TrimSpace(*email)
	if trimmedUsername == "" || trimmedEmail == "" {
		return r.usageError("create requires --username and --email", createUsage)
	}
	if !access.ValidateAdminUsername(trimmedUsername) {
		return r.usageError("invalid --username: use 3-32 characters from [A-Za-z0-9_.-]", createUsage)
	}
	if !access.ValidateAdminEmail(trimmedEmail) {
		return r.usageError("invalid --email address", createUsage)
	}

	password, err := r.loadPasswordBeforeRun(*passwordStdin)
	if err != nil {
		return r.operationError(err)
	}
	service, closeDB, err := r.openService(ctx)
	if err != nil {
		return r.operationError(err)
	}
	defer closeDB()
	user, err := service.Create(ctx, CreateInput{
		Username: trimmedUsername,
		Email:    trimmedEmail,
		Password: password,
	})
	if err != nil {
		return r.operationError(err)
	}
	fmt.Fprintf(r.Out, "created admin user %s (%s, %s)\n", user.ID, user.Username, user.Email)
	return 0
}

// loadPasswordBeforeRun validates the password before connecting so an
// automation error such as an empty stdin line fails fast without mutation.
func (r *Runner) loadPasswordBeforeRun(fromStdin bool) (string, error) {
	return r.readNewPassword(fromStdin)
}

func (r *Runner) runList(ctx context.Context, args []string) int {
	fs := newFlagSet("admin list")
	if ok, code := r.parseFlags(fs, args, listUsage); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return r.usageError("list does not accept positional arguments", listUsage)
	}
	service, closeDB, err := r.openService(ctx)
	if err != nil {
		return r.operationError(err)
	}
	defer closeDB()
	users, err := service.List(ctx)
	if err != nil {
		return r.operationError(err)
	}
	writer := tabwriter.NewWriter(r.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "UUID\tUSERNAME\tEMAIL\tSTATUS")
	for _, user := range users {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", user.ID, user.Username, user.Email, adminUserStatus(user))
	}
	if err := writer.Flush(); err != nil {
		return r.operationError(fmt.Errorf("write admin list: %w", err))
	}
	return 0
}

func (r *Runner) runRoleChange(ctx context.Context, args []string, promote bool) int {
	command := "demote"
	help := demoteHelp
	if promote {
		command = "promote"
		help = promoteHelp
	}
	target, ok, code := r.parseTarget(args, command, help)
	if !ok {
		return code
	}
	service, closeDB, err := r.openService(ctx)
	if err != nil {
		return r.operationError(err)
	}
	defer closeDB()

	var user *AdminUser
	var changed bool
	if promote {
		user, changed, err = service.Promote(ctx, target)
	} else {
		user, changed, err = service.Demote(ctx, target)
	}
	if err != nil {
		return r.operationError(err)
	}
	if !changed {
		if promote {
			fmt.Fprintf(r.Out, "no change: %s (%s) already has the admin role\n", user.Username, user.ID)
		} else {
			fmt.Fprintf(r.Out, "no change: %s (%s) does not have the admin role\n", user.Username, user.ID)
		}
		return 0
	}
	if promote {
		fmt.Fprintf(r.Out, "granted admin role to %s (%s)\n", user.Username, user.ID)
	} else {
		fmt.Fprintf(r.Out, "removed admin role from %s (%s); account preserved\n", user.Username, user.ID)
	}
	return 0
}

func (r *Runner) runResetPassword(ctx context.Context, args []string) int {
	fs := newFlagSet("admin reset-password")
	username := fs.String("username", "", "username")
	id := fs.String("id", "", "exact user UUID")
	passwordStdin := fs.Bool("password-stdin", false, "read one password line from stdin")
	if ok, code := r.parseFlags(fs, args, resetHelp); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return r.usageError("reset-password does not accept positional arguments", resetHelp)
	}
	target, ok := targetFromFlags(*username, *id)
	if !ok {
		return r.usageError("reset-password requires exactly one of --username or --id", resetHelp)
	}
	password, err := r.readNewPassword(*passwordStdin)
	if err != nil {
		return r.operationError(err)
	}
	service, closeDB, err := r.openService(ctx)
	if err != nil {
		return r.operationError(err)
	}
	defer closeDB()
	user, err := service.ResetPassword(ctx, target, password)
	if err != nil {
		return r.operationError(err)
	}
	fmt.Fprintf(r.Out, "password reset for admin %s (%s); sessions revoked\n", user.Username, user.ID)
	return 0
}

func (r *Runner) parseTarget(args []string, command, help string) (Target, bool, int) {
	fs := newFlagSet("admin " + command)
	username := fs.String("username", "", "username")
	id := fs.String("id", "", "exact user UUID")
	if ok, code := r.parseFlags(fs, args, help); !ok {
		return Target{}, false, code
	}
	if fs.NArg() != 0 {
		return Target{}, false, r.usageError(command+" does not accept positional arguments", help)
	}
	target, ok := targetFromFlags(*username, *id)
	if !ok {
		return Target{}, false, r.usageError(command+" requires exactly one of --username or --id", help)
	}
	return target, true, 0
}

func targetFromFlags(username, rawID string) (Target, bool) {
	username = strings.TrimSpace(username)
	rawID = strings.TrimSpace(rawID)
	if (username == "") == (rawID == "") {
		return Target{}, false
	}
	if username != "" {
		if !access.ValidateAdminUsername(username) {
			return Target{}, false
		}
		return Target{Username: username}, true
	}
	id, err := uuid.Parse(rawID)
	if err != nil || id == uuid.Nil {
		return Target{}, false
	}
	return Target{ID: id}, true
}

func (r *Runner) parseFlags(fs *flag.FlagSet, args []string, help string) (bool, int) {
	for _, arg := range args {
		if isHelp(arg) {
			fmt.Fprint(r.Out, help)
			return false, 0
		}
	}
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(r.Out, help)
			return false, 0
		}
		return false, r.usageError("invalid flags: "+err.Error(), help)
	}
	return true, 0
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

func isHelp(value string) bool {
	return value == "-h" || value == "--help" || value == "help"
}

func adminUserStatus(user AdminUser) string {
	status := "enabled"
	if user.Disabled {
		status = "disabled"
	}
	role := "user"
	for _, candidate := range user.Roles {
		if candidate == "admin" {
			role = "admin"
			break
		}
	}
	return status + " " + role
}

func (r *Runner) usageError(message, help string) int {
	fmt.Fprintf(r.Err, "backend admin: %s\n\n%s", message, help)
	return 2
}

func (r *Runner) operationError(err error) int {
	if err == nil {
		return 1
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "operation failed"
	}
	fmt.Fprintf(r.Err, "backend admin: %s\n", message)
	return 1
}

func (r *Runner) ensureStreams() {
	if r.In == nil {
		r.In = strings.NewReader("")
	}
	if r.Out == nil {
		r.Out = io.Discard
	}
	if r.Err == nil {
		r.Err = io.Discard
	}
}

func (r *Runner) openService(ctx context.Context) (*Service, func(), error) {
	loadConfig := r.LoadConfig
	if loadConfig == nil {
		loadConfig = config.LoadForAdmin
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	openDB := r.OpenDB
	if openDB == nil {
		openDB = postgres.NewPostgresClient
	}
	db, err := openDB(ctx, &cfg.Datasources.Postgres)
	if err != nil {
		return nil, nil, redactDatabaseError(err, cfg.Datasources.Postgres.Password)
	}
	closeDB := func() {
		_ = db.Close()
	}
	service := NewService(db, cfg.Argon2id)
	if err := service.CheckSchema(ctx); err != nil {
		closeDB()
		return nil, nil, err
	}
	return service, closeDB, nil
}

func redactDatabaseError(err error, password string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if password != "" {
		message = strings.ReplaceAll(message, password, "[redacted]")
	}
	return fmt.Errorf("connect postgres: %s", message)
}
