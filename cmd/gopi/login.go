package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/chatgpt"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
)

// loginProviderID is the provider that gopi login signs in to.
const loginProviderID = "chatgpt"

// loginTimeout is the time that gopi login waits for the browser.
const loginTimeout = 5 * time.Minute

// logoutTimeout is the time that gopi logout waits for the revocation.
const logoutTimeout = 30 * time.Second

// authFile is the name of the login file in the gopi home folder.
const authFile = "auth.json"

// authPath returns the path of the login file in home.
func authPath(home string) string {
	return filepath.Join(home, authFile)
}

// detectors returns the detector of a ChatGPT login, then the chain of
// package env. The login comes first, because a sign-in is a choice of
// the user, and the default model of gopi is the first model of the
// first provider that it finds.
func detectors(home string, set catalog.ModelFile) []catalog.Detector {
	login := catalog.Detector{
		Provider: loginProviderID,
		Source:   "gopi login chatgpt",
		Models:   set.Of(loginProviderID),
		Detect: func() (ai.TextProvider, bool) {
			path := authPath(home)
			a, err := chatgpt.LoadAccount(path)
			if err != nil || !a.SignedIn() {
				return nil, false
			}
			return chatgpt.New(chatgpt.FileSource(path, chatgpt.Auth{})), true
		},
	}
	return append([]catalog.Detector{login}, env.Detectors(set)...)
}

// homeDetectors returns [detectors] for the gopi home folder. If gopi
// has no home folder, it returns the chain of package env only.
func homeDetectors(set catalog.ModelFile) []catalog.Detector {
	home, err := gopiHome(os.Getenv)
	if err != nil {
		return env.Detectors(set)
	}
	return detectors(home, set)
}

// loginProvider checks the arguments of gopi login or gopi logout.
// ChatGPT is the only provider with a sign-in. Anthropic does not let
// another app use the login of a Claude plan, so a Claude plan runs
// through the claude-code driver.
func loginProvider(command string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gopi %s chatgpt", command)
	}
	switch strings.ToLower(args[0]) {
	case loginProviderID:
		return nil
	case "anthropic", "claude":
		return errors.New("gopi has no Claude sign-in: use the claude-code driver, or set ANTHROPIC_API_KEY")
	}
	return fmt.Errorf("no sign-in for %q: use gopi %s chatgpt", args[0], command)
}

// loginCommand signs in to the ChatGPT plan of the user.
func loginCommand() *cli.Command {
	return &cli.Command{
		Name:      "login",
		Usage:     "sign in with a ChatGPT account, so the chatgpt provider runs on its plan",
		ArgsUsage: "chatgpt",
		Action: func(ctx context.Context, c *cli.Command) error {
			if err := loginProvider("login", c.Args().Slice()); err != nil {
				return err
			}
			return runLogin(ctx, c.Root().ErrWriter)
		},
	}
}

// logoutCommand signs out of the ChatGPT account.
func logoutCommand() *cli.Command {
	return &cli.Command{
		Name:      "logout",
		Usage:     "sign out of the ChatGPT account, and revoke its token",
		ArgsUsage: "chatgpt",
		Action: func(ctx context.Context, c *cli.Command) error {
			if err := loginProvider("logout", c.Args().Slice()); err != nil {
				return err
			}
			return runLogout(ctx, c.Root().ErrWriter)
		},
	}
}

// runLogin signs in and saves the account. It reuses the registration
// of an earlier sign-in, so each login does not add a registration to
// the account of the user.
func runLogin(ctx context.Context, w io.Writer) error {
	home, err := gopiHome(os.Getenv)
	if err != nil {
		return err
	}
	path := authPath(home)

	var prior *chatgpt.Account
	a, err := chatgpt.LoadAccount(path)
	switch {
	case err == nil:
		prior = &a
	case !errors.Is(err, chatgpt.ErrNoAccount):
		return err
	}

	cfg := chatgpt.SignInConfig{
		AppName: "gopi",
		HostID:  chatgpt.NewHostID(),
		Port:    chatgpt.DefaultPort,
		Open: func(url string) error {
			fmt.Fprintf(w, "Open this URL in a browser to sign in:\n\n  %s\n\n", url)
			fmt.Fprintln(w, "Waiting for the browser. Or paste the redirect URL here.")
			openBrowser(url)
			return nil
		},
	}
	if isTerminal(os.Stdin) {
		cfg.Paste = func(ctx context.Context) (string, error) {
			return readLine(ctx, os.Stdin)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	signed, err := chatgpt.Auth{}.SignIn(ctx, cfg, prior)
	if err != nil {
		return err
	}
	if err := chatgpt.SaveAccount(path, signed); err != nil {
		return err
	}
	fmt.Fprintf(w, "Signed in as %s\n  Saved to %s\n", signed.Email, path)
	return nil
}

// runLogout revokes the refresh token and keeps only the registration.
// If the revocation fails, the tokens still go away, and the user can
// disconnect gopi in the ChatGPT settings.
func runLogout(ctx context.Context, w io.Writer) error {
	home, err := gopiHome(os.Getenv)
	if err != nil {
		return err
	}
	path := authPath(home)
	a, err := chatgpt.LoadAccount(path)
	if errors.Is(err, chatgpt.ErrNoAccount) || (err == nil && !a.SignedIn()) {
		return errors.New("gopi is not signed in to chatgpt")
	}
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, logoutTimeout)
	defer cancel()
	revokeErr := chatgpt.Auth{}.SignOut(ctx, a)
	if err := chatgpt.SaveAccount(path, a.Registration()); err != nil {
		return err
	}
	if revokeErr != nil {
		fmt.Fprintf(w, "Removed the tokens from %s.\n  OpenAI did not confirm the revocation: %v\n  Disconnect gopi in the ChatGPT settings.\n", path, revokeErr)
		return nil
	}
	fmt.Fprintln(w, "Signed out of chatgpt. OpenAI revoked the token.")
	return nil
}

// readLine reads one trimmed line from r. If ctx ends first, readLine
// returns, and the read stays blocked until the process exits. That is
// acceptable for a command that exits after the sign-in.
func readLine(ctx context.Context, r io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		s := bufio.NewScanner(r)
		if s.Scan() {
			ch <- result{line: strings.TrimSpace(s.Text())}
			return
		}
		ch <- result{err: cmpErr(s.Err(), io.EOF)}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// cmpErr returns err, or def if err is nil.
func cmpErr(err, def error) error {
	if err != nil {
		return err
	}
	return def
}

// openBrowser opens url in the browser of the system. If it cannot, the
// user opens the printed URL.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}
