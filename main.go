package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/systemd"
)

const usage = `Usage: botid <command>

Commands:
  init   Set up credentials, git/gh isolation and the systemd timer
  issue  Issue a GitHub App installation token and write it to the botid store
  env    Print the environment variables that point gh and git at the botid store, in .env format
  help   Show this usage
`

const githubAPI = "https://api.github.com"

// Retry interval for 5xx responses and network errors. The plan does not specify a value, so this is the smallest value that avoids immediate retries.
const retryDelay = 2 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, newApp))
}

func newApp() (*cmd.App, error) {
	home, err := paths.Home()
	if err != nil {
		return nil, err
	}
	return &cmd.App{
		Home:   home,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Client: func(clientID string, key *rsa.PrivateKey) *ghapp.Client {
			return &ghapp.Client{BaseURL: githubAPI, RetryDelay: retryDelay, ClientID: clientID, Key: key}
		},
		Exec:    cmd.ExecCommand,
		Systemd: systemd.ExecRunner,
		Exe:     os.Executable,
	}, nil
}

func run(args []string, stdout, stderr io.Writer, newApp func() (*cmd.App, error)) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "init", "issue", "env":
	default:
		_, _ = fmt.Fprintf(stderr, "botid: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if len(args) > 1 {
		_, _ = fmt.Fprintf(stderr, "botid: unexpected extra arguments for %s: %q\n\n%s", args[0], args[1:], usage)
		return 2
	}

	app, err := newApp()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "botid: %v\n", err)
		return 1
	}
	switch args[0] {
	case "init":
		err = app.Init(context.Background())
	case "issue":
		err = app.Issue(context.Background())
	case "env":
		err = app.Env()
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "botid: %v\n", err)
		return 1
	}
	return 0
}
