package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/datalock"
	"github.com/frankgraave/subglance/internal/store"
)

// resetPasswordTimeout bounds opening the database, its migrations and the
// write. Hashing is the slow step and takes well under a second.
const resetPasswordTimeout = 2 * time.Minute

// secretSource is where the new password comes from. In the shipped binary it
// is the terminal; the tests hand in their own.
type secretSource interface {
	// Interactive reports whether a person can be asked: standard input is
	// a terminal.
	Interactive() bool
	// ReadSecret shows the prompt and reads one line without echoing it.
	ReadSecret(prompt string) (string, error)
}

// runResetPassword sets a new password for one account, for an owner who
// can no longer sign in. See resetPassword.
func runResetPassword(args []string, out io.Writer) error {
	return resetPassword(args, out, terminalSecrets{in: os.Stdin, prompt: os.Stderr})
}

// resetPassword is runResetPassword with the password source passed in.
//
// The trust boundary is the data directory. Whoever can read and write it
// already holds every monitor and account in it (and every channel secret
// not encrypted under --secret-key), so being able to run this command
// against it proves nothing less than an emailed link would; and a
// self-hosted instance often has no mail set up to send one.
//
// It refuses while a server is running, with the same two guards as
// `subglance restore`: the data directory lock, which --force never
// overrides, and something answering on --addr, which --force skips for the
// case where that is not SubGlance. A server left running would also be
// left serving the sessions this command is about to end.
//
// The password is read from the terminal, twice and without echo, or made
// up with --generate and shown once. There is no flag or variable to pass it
// in: either would put it in the shell history or the process list.
func resetPassword(args []string, out io.Writer, secrets secretSource) error {
	fs := flag.NewFlagSet("subglance reset-password", flag.ContinueOnError)
	fs.SetOutput(out)
	email := fs.String("email", "", "address of the account to reset (required)")
	generate := fs.Bool("generate", false, "make up a password and show it once, instead of asking for one")
	force := fs.Bool("force", false, "reset even though something answers on --addr (never while a server holds the data directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "usage: subglance reset-password --email ADDRESS [--generate] [--force] [--data-dir DIR]")
		fs.PrintDefaults()
	}
	own, rest := splitResetPasswordFlags(args)
	if err := fs.Parse(own); err != nil {
		return err
	}
	address := strings.TrimSpace(*email)
	if address == "" {
		return errors.New("--email is required: the address of the account whose password to reset")
	}

	cfg, err := config.Load(rest)
	if err != nil {
		return err
	}

	// Checked before the lock and before opening anything: store.Open would
	// otherwise create an empty database in a mistyped directory and report
	// that the account does not exist, which sends the operator looking for
	// the wrong problem.
	dbPath := cfg.DBPath()
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("there is no database at %s; point --data-dir (or SUBGLANCE_DATA_DIR) "+
			"at the directory the server uses", dbPath)
	} else if err != nil {
		return fmt.Errorf("check database %s: %w", dbPath, err)
	}

	lock, err := datalock.Acquire(cfg.DataDir)
	if errors.Is(err, datalock.ErrLocked) {
		return fmt.Errorf("a SubGlance server is using %s (it holds %s); stop it first, then run reset-password again",
			cfg.DataDir, datalock.Path(cfg.DataDir))
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	if !*force && listening(cfg.Addr) {
		return fmt.Errorf("something is answering on %s, which is probably SubGlance itself; stop it first, "+
			"or pass --force if that is not SubGlance", displayAddr(cfg.Addr))
	}

	ctx, cancel := context.WithTimeout(context.Background(), resetPasswordTimeout)
	defer cancel()

	// No secret key, as for a backup: resetting a password never reads a
	// channel's configuration, and requiring the key would lock out an owner
	// who has lost the password and the key together.
	db, err := store.Open(ctx, store.Options{Path: dbPath, SkipChannelEncryption: true})
	if err != nil {
		return fmt.Errorf("open database %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	// Before the password is asked for, so a mistyped address costs one
	// command rather than two typed passwords. The refusal names only the
	// address given: listing the accounts that do exist would hand them to
	// anyone who can run the binary without being able to read the database.
	user, err := db.GetUserByEmail(ctx, address)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no account has the address %q", address)
	}
	if err != nil {
		return err
	}

	password, err := newPassword(user.Email, *generate, secrets)
	if err != nil {
		return err
	}
	ended, err := db.ResetPassword(ctx, user.ID, password)
	if err != nil {
		return err
	}

	if *generate {
		_, _ = fmt.Fprintf(out, "New password for %s, shown this once:\n\n    %s\n\n", user.Email, password)
	}
	_, _ = fmt.Fprintf(out, "Reset the password of %s and signed it out of %s. API tokens are unchanged.\n",
		user.Email, plural(ended, "session", "sessions"))
	return nil
}

// newPassword asks for the password twice, or makes one up.
func newPassword(email string, generate bool, secrets secretSource) (string, error) {
	if generate {
		// 26 characters of base32, 128 bits from the system's random source.
		return rand.Text(), nil
	}
	if !secrets.Interactive() {
		return "", errors.New("standard input is not a terminal, so there is nobody to ask for the new password; " +
			"run the command interactively (docker compose run needs no extra flag, docker run needs -it), " +
			"or pass --generate to have one made up and shown once")
	}
	first, err := secrets.ReadSecret(fmt.Sprintf("New password for %s: ", email))
	if err != nil {
		return "", fmt.Errorf("read the new password: %w", err)
	}
	// The same rule the sign-in page and Settings apply, checked before the
	// second prompt so a password that will be refused is not typed twice.
	if err := auth.ValidatePassword(first); err != nil {
		return "", err
	}
	again, err := secrets.ReadSecret("Repeat it: ")
	if err != nil {
		return "", fmt.Errorf("read the new password: %w", err)
	}
	if again != first {
		return "", errors.New("the two passwords differ; nothing was changed")
	}
	return first, nil
}

// splitResetPasswordFlags separates --email, --generate, --force and help
// from the server flags, which go to config.Load unchanged.
func splitResetPasswordFlags(args []string) (own, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		name, _, hasValue := strings.Cut(name, "=")
		switch {
		case !strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		case name == "generate" || name == "force" || name == "h" || name == "help":
			own = append(own, a)
		case name == "email":
			own = append(own, a)
			if !hasValue && i+1 < len(args) {
				i++
				own = append(own, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	return own, rest
}

// plural writes a count with the word that agrees with it.
func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// terminalSecrets reads a password from the terminal without echoing it.
type terminalSecrets struct {
	in     *os.File
	prompt io.Writer
}

func (t terminalSecrets) Interactive() bool { return term.IsTerminal(int(t.in.Fd())) }

func (t terminalSecrets) ReadSecret(prompt string) (string, error) {
	_, _ = fmt.Fprint(t.prompt, prompt)
	b, err := term.ReadPassword(int(t.in.Fd()))
	// The terminal swallowed the Enter along with the echo; without this
	// newline the next prompt or message starts on the same line.
	_, _ = fmt.Fprintln(t.prompt)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
