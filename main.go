package main

import (
	"context"
	"crypto/rsa"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/systemd"
	"github.com/pycabbage/botid/internal/version"
)

const usage = `Usage: botid <command>

Commands:
  init     Set up credentials, git/gh isolation and the systemd timer
  issue    Issue a GitHub App installation token and write it to the botid store
  env      Print the environment variables that point gh and git at the identity selected for a directory, in .env or JSON format
  version  Print the version
  help     Show this usage
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
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot get the working directory: %w", err)
	}
	return &cmd.App{
		Home:   home,
		Cwd:    cwd,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Client: func(clientID string, key *rsa.PrivateKey) *ghapp.Client {
			return &ghapp.Client{BaseURL: githubAPI, RetryDelay: retryDelay, ClientID: clientID, Key: key}
		},
		Exec:    cmd.ExecCommand,
		Git:     repo.ExecGit,
		Systemd: systemd.ExecRunner,
		Exe:     os.Executable,
	}, nil
}

type envArgs struct {
	cwd    string
	format string
}

func parseEnvArgs(args []string) (envArgs, error) {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var parsed envArgs
	fs.StringVar(&parsed.cwd, "cwd", "", "")
	fs.StringVar(&parsed.format, "format", cmd.FormatDotEnv, "")
	if err := fs.Parse(args); err != nil {
		return envArgs{}, err
	}
	if fs.NArg() > 0 {
		return envArgs{}, fmt.Errorf("unexpected extra arguments for env: %q", fs.Args())
	}
	if parsed.format != cmd.FormatDotEnv && parsed.format != cmd.FormatJSON {
		return envArgs{}, fmt.Errorf("--format must be %s or %s, not %q", cmd.FormatDotEnv, cmd.FormatJSON, parsed.format)
	}
	return parsed, nil
}

func run(args []string, stdout, stderr io.Writer, newApp func() (*cmd.App, error)) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	var env envArgs
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(args) > 1 {
			_, _ = fmt.Fprintf(stderr, "botid: unexpected extra arguments for %s: %q\n\n%s", args[0], args[1:], usage)
			return 2
		}
		_, _ = fmt.Fprintf(stdout, "botid version %s\n", version.String())
		return 0
	case "init", "issue":
		if len(args) > 1 {
			_, _ = fmt.Fprintf(stderr, "botid: unexpected extra arguments for %s: %q\n\n%s", args[0], args[1:], usage)
			return 2
		}
	case "env":
		var err error
		if env, err = parseEnvArgs(args[1:]); err != nil {
			_, _ = fmt.Fprintf(stderr, "botid: %v\n\n%s", err, usage)
			return 2
		}
	default:
		_, _ = fmt.Fprintf(stderr, "botid: unknown command %q\n\n%s", args[0], usage)
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
		err = app.Env(context.Background(), env.cwd, env.format)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "botid: %v\n", err)
		return 1
	}
	return 0
}
