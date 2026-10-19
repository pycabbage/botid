# 001: botid implementation plan

The source of truth for requirements is `docs/INSTRUCTION.md`. This document sets out the decisions and steps for turning `INSTRUCTION.md` into an implementation. Features not in `INSTRUCTION.md` are not built.

## 1. Scope

To build:

- `botid init`
- `botid issue`
- `botid env`

Not to build:

- An `uninstall` subcommand (not in `INSTRUCTION.md`)
- A way to change the timer interval (`INSTRUCTION.md` states that it provides none)
- Wrappers or wrapper scripts for gh / git
- Support for GitHub Enterprise Server (`INSTRUCTION.md` covers `github.com` only)
- Carrying over signing settings, and countermeasures for SSH (not in `INSTRUCTION.md`)

There is only one change from the `INSTRUCTION.md` schema. To support multiple installations, `installationId` is added to `config.json`. The user chooses it at `botid init` time (user instruction). `INSTRUCTION.md` itself has not been rewritten.

## 2. Primary sources consulted

- GitHub JWT generation: GitHub's official docs recommend using the Client ID for `iss`. `alg` is RS256, `iat` should be 60 seconds in the past, and `exp` is at most 10 minutes. https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
- Reference implementation `actions/create-github-app-token` (`lib/main.js`, `README.md`)
  - It uses `POST /app/installations/{id}/access_tokens` to issue the installation token.
  - It gets the bot's user id from `GET /users/<slug>[bot]`.
  - It runs `git config user.name '<slug>[bot]'`, `user.email '<id>+<slug>[bot]@users.noreply.github.com'`, and `gh auth setup-git`.
  - It retries up to 3 times, on 5xx and network errors only.
  - It passes the Client ID as `appId`.
- Source and real-machine verification of gh 2.102.0 (real machine)
  - `GH_CONFIG_DIR` takes precedence over `XDG_CONFIG_HOME` and `HOME`.
  - `gh auth token` reads `oauth_token` directly under the host. It cannot read it from the nested `users:` alone.
  - `GH_TOKEN` and `GITHUB_TOKEN` take precedence over hosts.yml.
  - The keyring key does not include `GH_CONFIG_DIR`. If hosts.yml has `oauth_token`, the keyring is not consulted.
  - `gh auth setup-git` runs `git config --global`, and `GIT_CONFIG_GLOBAL` can isolate where it writes. It fails if there is no authenticated host. The absolute path of gh is embedded in the helper.
  - gh reads its configuration only once, at startup.
- Official git documentation: setting `GIT_CONFIG_GLOBAL` stops both `~/.gitconfig` and `$XDG_CONFIG_HOME/git/config` from being read. `~` is not expanded, so an absolute path is required.
- systemd 255 (real machine): confirmed that a minimal `.service` and `.timer` including `Restart=on-failure` pass `systemd-analyze --user verify`. The start of `ExecStart=` needs an absolute path.

## 3. Directory layout

Configuration (managed by the user) and generated files (written by botid) are placed in separate directories.

```
~/.config/botid/
  config.json         0600  clientId, privateKeyPath, installationId
  private-key.pem     0600  private key (saved by init)

~/.local/share/botid/
  gh/hosts.yml        0600  the directory GH_CONFIG_DIR points to. Written by init and issue
  git/config                the file GIT_CONFIG_GLOBAL points to. Created by the git that init runs
```

- The `gh/` and `git/` directories are 0700. The file mode of `git/config` is left to git. botid does not `chmod` it. This file holds only `user.name`, `user.email`, and the credential helper command. It holds no token.
- The configuration directory is fixed to `$HOME/.config/botid`. `INSTRUCTION.md` states the fixed path.
- The generated-files directory is fixed to `$HOME/.local/share/botid` (user instruction).
- Neither refers to `os.UserConfigDir()`, `XDG_CONFIG_HOME`, or `XDG_DATA_HOME`. `os.UserConfigDir()` refers to `XDG_CONFIG_HOME`, so the resolved path can differ between `init` (shell) and the systemd service because their environments differ. Go has no `os.UserDataDir()`. The paths that `botid env` prints must also be fixed values.
- Directories are created with 0700. `MkdirAll(0700)` does not fix the permissions of an existing directory, so `Chmod(0700)` is called after creation.
- `os.WriteFile` also does not fix the permissions of an existing file. All files that botid writes (`config.json`, `private-key.pem`, `hosts.yml`, the systemd units) are written by atomic replacement (§6, §8). `git/config` is written by git.
- If `gh/` and `hosts.yml` are deleted, `init` or `issue` can recreate them. `git/` and `git/config` are created only by `init`. `issue` does not create `git/`. If `config.json` and the private key are lost, `init` must be run again.
- The `systemd` units go in a different place (`~/.config/systemd/user/`, §8). They are not part of the two directories above.

## 4. Repository layout

```
main.go                      only dispatches subcommands
internal/paths/              derives the paths of the two directories above (`New(home)` is a pure function that takes HOME as an argument. `Home()` calls `os.UserHomeDir()` and returns an error if the result is not an absolute path)
internal/config/             reads, writes, and validates config.json
internal/ghapp/              JWT, GitHub API client
internal/store/              atomic writes, directory creation, hosts.yml, .env output
internal/systemd/            unit generation and systemctl calls
internal/cmd/                implementation of init / issue / env
internal/integration/        integration tests with the real tools (gh, git) (test code only)
```

No external dependencies are added. Everything is written with the standard library only.

- The CLI is built with a `switch` on `os.Args` (§9).
- The JWT is written in-house with `crypto/rsa`, `crypto/sha256`, and `encoding/base64`. The official procedure is only "join the header and payload with base64url and sign with RS256". It is not complex enough to need a library. `ghinstallation` does not support the Client ID and `go-github` cannot create a JWT, so neither is used.
- `go 1.26.5` in `go.mod` exists, and the Go on the real machine satisfies it. It is not changed.

## 5. Types and signatures (sketch)

The following matches the signatures of the implementation (under `internal/` and `main.go`).

```go
// internal/paths
type Paths struct {
    ConfigDir, ConfigFile, PrivateKeyFile string  // ~/.config/botid, config.json, private-key.pem
    DataDir, GHConfigDir, HostsFile       string  // ~/.local/share/botid, gh/, gh/hosts.yml
    GitConfigDir, GitConfigFile           string  // git/, git/config
}
func New(home string) Paths                       // pure function. Does not refer to XDG_*
func Home() (string, error)                       // os.UserHomeDir(). Error if not an absolute path

// internal/config
type Config struct {
    ClientID       string `json:"clientId"`
    PrivateKey     string `json:"privateKey,omitempty"`
    PrivateKeyPath string `json:"privateKeyPath,omitempty"`
    InstallationID int64  `json:"installationId"`
}
// If the file does not exist, the error wraps fs.ErrNotExist. It is an error if there are unknown fields,
// extra content after the JSON, an empty clientId, or both or neither of privateKey and privateKeyPath.
// It is not an error if installationId is 0 (issue tells the user to run init again).
func Load(path string) (Config, error)
func Save(path string, c Config) error            // writes atomically with 0600. The caller creates the parent directory
func (c Config) Key() (*rsa.PrivateKey, error)    // ParsePEM on the file at privateKeyPath, or on the privateKey string

// internal/ghapp
type Client struct {
    BaseURL    string                   // https://api.github.com in production, the httptest URL in tests
    HTTP       *http.Client             // if nil, the default client with a 30-second timeout
    Now        func() time.Time         // if nil, time.Now
    RetryDelay time.Duration            // 2 seconds in the production main.go. The shared test helper in ghapp leaves it unset (0)
    ClientID   string
    Key        *rsa.PrivateKey
}
type Credential struct {
    Token     string
    Slug      string       // slug from GET /app
    ExpiresAt time.Time
}
func ParsePEM(pem []byte) (*rsa.PrivateKey, error)           // PKCS#1 and PKCS#8 (RSA only). Error for encrypted PEM
type Installation struct {
    ID    int64
    Login string                        // account.login
}
func (c *Client) Installations(ctx context.Context) ([]Installation, error) // excludes suspended ones. Used by init only
func (c *Client) Issue(ctx context.Context, installationID int64) (Credential, error)  // error if installationID <= 0
func (c *Client) BotUserID(ctx context.Context, token, slug string) (int64, error)

// internal/store
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error
func EnsureDir(path string) error                            // Chmod(0700) after MkdirAll(0700). Does not change permissions of ancestor directories
func HostsYAML(slug, token string) ([]byte, error)           // rejection conditions are in §6.1
func DotEnv(ghConfigDir, gitConfigGlobal string) (string, error)  // error if a path is not absolute or contains ' or a newline

// internal/systemd
type Runner func(name string, args ...string) ([]byte, error)
func ExecRunner(name string, args ...string) ([]byte, error)     // runs with os/exec and returns the combined stdout and stderr output
func Install(home, exe string, run Runner) error                 // only calls install(home, exe, run, "/run/systemd/system", os.TempDir())

// internal/cmd
type App struct {
    Home    string                                                   // $HOME. All paths are derived with paths.New(Home)
    Stdin   io.Reader
    Stdout  io.Writer
    Stderr  io.Writer
    Client  func(clientID string, key *rsa.PrivateKey) *ghapp.Client // creates a ghapp.Client. Tests replace BaseURL
    Exec    func(env []string, name string, args ...string) ([]byte, error) // git and gh that init runs
    Systemd systemd.Runner                                           // systemctl calls
    Exe     func() (string, error)                                   // os.Executable in production
}
func ExecCommand(env []string, name string, args ...string) ([]byte, error) // runs with env added to os.Environ(). Returns the combined output
func (a *App) Init(ctx context.Context) error
func (a *App) Issue(ctx context.Context) error
func (a *App) Env() error
```

`systemd.Install` passes `/run/systemd/system` and `os.TempDir()` to the unexported `install`. The reason is that tests can then replace the systemd boot check and the temporary-directory check.

The `git` and `gh` that `init` runs are called through `App.Exec`. Environment variables are passed in the `env` argument. Tests replace it with a fake that only records the calls. The production code has no hook for replacing it through environment variables.

## 6. Command specifications

### 6.1 `botid issue`

1. Read `config.json`. If it does not exist, print `<HOME>/.config/botid/config.json does not exist; run botid init`. The path is the absolute path with `$HOME` expanded, not `~`. `main` prepends `botid: ` to the error, prints it as one line to standard error, and exits with code 1 (§9).
2. If `installationId` is unset (0), fail with `<HOME>/.config/botid/config.json has no installationId; run botid init again`. This check runs before the key is read.
3. Read the private key (`Config.Key()`). If it is `privateKeyPath`, read that file. If it is `privateKey`, use that string.
4. Get the token with `Client.Issue(config.InstallationID)` (§7).
5. Build the hosts.yml content with `HostsYAML(slug, token)`. On error, fail without writing anything.
6. Create `~/.local/share/botid/` and `gh/` with `EnsureDir` (0700), and rewrite `~/.local/share/botid/gh/hosts.yml` atomically (with 0600).

The hosts.yml content is fixed to the form below. As in the INSTRUCTION.md example, the nested `users:` and the entry directly under the host are both written. gh reads `oauth_token` directly under the host, so the entry directly under the host is required.

```yaml
github.com:
    users:
        "<slug>[bot]":
            oauth_token: ghs_...
    git_protocol: https
    user: "<slug>[bot]"
    oauth_token: ghs_...
```

- `HostsYAML(slug, token) ([]byte, error)` embeds `token` in the YAML without quotes and `slug` inside double quotes. A value that would break the YAML causes an error and hosts.yml is not created. The rejection conditions are as follows (`internal/store/store.go`).
  - `slug` or `token` is empty.
  - It contains invalid UTF-8.
  - It contains whitespace (Unicode whitespace) or control characters.
  - It contains `"` or `\`.
  - `token` starts with a YAML reserved character (`!&*-?:,[]{}#|>'%@` and `` ` ``). The start of `slug` is not checked, because it is enclosed in double quotes.
  - The length and prefix of the token are not checked (§7). A value that contains `_`, `.`, or `-`, such as `ghs_12345_<JWT format>`, passes (`-` except at the start).
- `git_protocol` is `https`. `ghs_` tokens are meant to be used over HTTPS.
- `issue` runs neither `gh` nor `git`. It works even when `gh` is not in the `PATH` of the systemd service.
- On failure, the existing hosts.yml is not broken. It is written only after the token has been obtained.

### 6.2 `botid env`

Print only the following 2 lines to standard output.

```
GH_CONFIG_DIR='/home/<user>/.local/share/botid/gh'
GIT_CONFIG_GLOBAL='/home/<user>/.local/share/botid/git/config'
```

- Each value is the absolute path with `$HOME` expanded, enclosed in single quotes. Research confirmed that docker compose, systemd `EnvironmentFile`, and direnv all treat a single-quoted value as is, without expansion.
- If a path contains `'` or a newline (`\n`, `\r`), exit with an error, because the way to write it differs between parsers. A path that is not absolute (including one that starts with `~`) is also an error (`store.DotEnv`).
- `GH_TOKEN` and `GITHUB_TOKEN` are not printed. They are values that change, and they take precedence over hosts.yml, so updates would stop taking effect.
- `XDG_CONFIG_HOME` and `HOME` are not printed. They would also change the configuration location of other tools.
- A notice saying that these variables should be set only once on the coding-agent side is printed to standard error (after the write to standard output has succeeded). Standard output is in `.env` format only.
- It does not depend on whether `config.json` exists.

### 6.3 `botid init`

Read from standard input. One `bufio.Reader` is shared to the end, so that no buffered input is lost between prompts.

1. If `config.json` exists, print a warning to standard error and ask `Overwrite? [y/n]: ` on standard output. Continue only if the input, with surrounding whitespace removed, is exactly `y`. Anything other than `y` (including `Y`, `yes`, and EOF) changes nothing, prints `Exited without changing anything.` to standard output, and ends with exit code 0.
2. Read `Client ID: `. If it is empty (including EOF), end with an error (exit code 1).
3. Read `Private Key (PEM contents or file path): `.
   - If the first line starts with `-----BEGIN`, treat the input as PEM contents. Read up to the line that starts with `-----END`.
   - If the input ends before `-----END` is found, it is an error.
   - Otherwise, treat it as a file path. A leading `~/` is expanded to `App.Home` (`$HOME`). `~user/` is not expanded. If it is empty, it is an error.
4. Validate with `ParsePEM`. Both PKCS#1 (`RSA PRIVATE KEY`) and PKCS#8 (`PRIVATE KEY`) are read. Encrypted PEM cannot be handled by the standard library, so it is an error that states it is not supported.
5. With the in-memory `Config` and key, decide the installation and issue a token once. If this stage fails, end without writing anything to disk.
   1. Get the installations, excluding suspended ones, with `Client.Installations`.
   2. If there are 0, end with `this App is not installed on any account`.
   3. If there is 1, select it and show the selected `account.login` and `id`.
   4. If there are 2 or more, show `account.login` and `id` with numbers, and read a number at `Installation [1-N]: `. A number out of range, a non-number, or EOF ends with an error. There is no default value. This is so that a wrong organization is not selected silently.
   5. Call `Client.Issue` with the selected ID and issue a token. Then build the hosts.yml content with `HostsYAML` (§6.1). If any step up to here fails, end without writing anything to disk.
6. Write in the following order.
   1. Create `~/.config/botid/` with `EnsureDir` (0700).
   2. `~/.config/botid/private-key.pem` (0600). Whether the input was PEM contents or a file path, the PEM bytes that were read are saved.
   3. `~/.config/botid/config.json` (0600. `privateKeyPath` records the absolute path of `private-key.pem`, together with `installationId`. `privateKey` is not used.)
   4. Create `~/.local/share/botid/` and `gh/` with `EnsureDir` (0700).
   5. `~/.local/share/botid/gh/hosts.yml` (0600)
7. Create `~/.local/share/botid/git/` with `EnsureDir` (0700). Run the following in an environment where `GH_CONFIG_DIR` and `GIT_CONFIG_GLOBAL` point to botid's side. The environment variables are passed as `os.Environ()` plus these two (`HOME` and `XDG_*` are not changed). The user's own `~/.gitconfig` and `~/.config/gh/` are not touched.
   1. Get the bot's user id with `BotUserID`. Call `GET /users/<slug>%5Bbot%5D` with the installation token.
   2. `git config --global user.name '<slug>[bot]'`
   3. `git config --global user.email '<id>+<slug>[bot]@users.noreply.github.com'`
   4. `gh auth setup-git` (`--force` is not needed, because hosts.yml was written first)
8. Create and enable the systemd units (§8). If `os.Executable()` cannot be obtained, it is an error.
9. Show what was done and tell the user to run `botid env`.

Handling of failures:

- If a failure happens at step 6 or later, the files written so far are not rolled back. If `git` or `gh` is not found, fail with `<name> not found; install it and add it to PATH`. If a command fails, fail with the command line and the combined stdout and stderr output.
- If registering systemd (step 8) fails, fail with `the configuration and credentials were saved, but registering the systemd timer failed` prepended.

Notes:

- The helper that `gh auth setup-git` writes to git config has the absolute path of `gh` embedded in it (such as `/usr/bin/gh`). If `gh` is moved, the helper breaks.
- The isolated git config holds only `user.*` and the credential helper. The user's own `~/.gitconfig` is not included, so signing settings, aliases, and `safe.directory` are not carried over.
- For a repository with an SSH remote, isolating git config has no effect on the SSH key and agent. The git documentation says that SSH authentication is decided by the ssh configuration. This behavior is unverified on the real machine. `INSTRUCTION.md` does not mention SSH, so no countermeasure is added.

Items that the integration test (`internal/integration/integration_test.go`) asserts in code (the results of running the tests are not covered in this document):

- When the output of `HostsYAML("example-app", "ghs_dummy")` is written under `GH_CONFIG_DIR`, `gh auth token` returns `ghs_dummy`. The form is the nested `users:` plus the entry directly under the host (asserted).
- `gh auth setup-git` writes `[credential "https://github.com"]` to the file that `GIT_CONFIG_GLOBAL` points to. The test checks that this section contains `helper =` (the reset line) and `helper = !<path of gh> auth git-credential`. The embedded helper line is written to the log (asserted).
- The output of `git credential fill` contains both `username=example-app[bot]` and `password=ghs_dummy` (asserted). Because `HostsYAML("example-app", "ghs_dummy")` writes `user: "example-app[bot]"`, the username becomes `<slug>[bot]`. Confirmed (asserted by the `internal/integration` test).
- The environment of the child process does not inherit `os.Environ()`. The test builds `PATH`, `HOME`, `XDG_*`, `GH_CONFIG_DIR`, `GIT_CONFIG_GLOBAL`, and so on. `GH_TOKEN` and similar are not passed. Before and after the test, it checks that the existence and modification time of the real `~/.config/gh` and `~/.gitconfig` have not changed (contents are not read).

## 7. GitHub API client

Common request specification:

- Headers: `Accept: application/vnd.github+json`, `User-Agent: botid`, `X-GitHub-Api-Version: 2022-11-28`. This matches the API version the reference implementation relies on. Without `User-Agent`, the request is rejected.
- The `http.Client` timeout is 30 seconds. This is the default client used when `Client.HTTP` is nil.
- Only 5xx and network errors are retried. There are at most 3 retries in addition to the first attempt, so a single request is tried at most 4 times (`maxRetries = 3`). `RetryDelay` is inserted between retries. The production `main.go` sets `RetryDelay` to 2 seconds. This value is an implementation-time decision not in the plan. It was chosen as the smallest value that avoids immediate retries. 4xx is not retried.
- Retries are per request. `Issue` sends 2 requests, `access_tokens` and `GET /app`, and each is tried independently up to 4 times.
- If a 5xx does not recover after the retries, return the status of the last response and use the 5xx message. If network errors continue, fail with `cannot connect to GitHub API (after 3 retries)`.
- If the `context` is cancelled, stop retrying and fail. If the request cannot be built (for example, an invalid `BaseURL`), it is not treated as a network error and is not retried.

The JWT is created on every call and is not saved. `alg=RS256`, `iss=<clientId>`, `iat=now-60`, `exp=now+540` (integer seconds).

Steps of `Installations` (called by `init` only):

1. Create a JWT.
2. Repeat `GET /app/installations?per_page=100&page=N` until the number of items returned is less than 100.
3. Exclude installations whose `suspended_at` is not null, and return the list of `Installation{ID, Login}`. The branching by count (0, 1, 2 or more) is done by the caller, `init` (§6.3).

Steps of `Issue(installationID)` (called by `init` and `issue`):

1. Create a JWT.
2. Call `POST /app/installations/{installationID}/access_tokens` with the JWT. No request body is sent (all permissions and all repositories of the installation). Read `token` and `expires_at`.
3. Call `GET /app` with the JWT and read `slug`. `slug` is used for `user` in hosts.yml and for `user.name` in `init`.
4. Return `Credential{Token, Slug, ExpiresAt}`.

`issue` does not call `GET /app/installations`. The installation is fixed by `installationId` in `config.json`.

Error policy:

- The JWT and token values are not included in error messages or logs.
- An error message has the form `<operation name>: <body>`. The `message` in the GitHub response, if it could be read from the JSON, is appended at the end as ` [GitHub: <message>]`.
- Messages are split by status (`statusError`). The checks run in order from the top.
  - 401, for a request authenticated with the JWT (the `Installations` request and the 2 requests of `Issue`): the Client ID or private key may be wrong, or the clock of the PC may be off.
  - 401, for a request authenticated with the installation token (`BotUserID`): the installation token is invalid or has expired.
  - 404: `not found (404)`. For a 404 from `access_tokens` only, the message goes on to say that the installation may have been deleted, and to run `botid init` again and choose again.
  - 403 with `Resource not accessible by integration` in `message`: the App does not have enough permissions.
  - Rate limit: the status is 429, or it is 403 and any of the following holds: `X-Ratelimit-Remaining` is `0`, there is a `Retry-After` header, or `message` contains `rate limit` (case-insensitive). Show the wait time until `X-Ratelimit-Reset` (or `Retry-After` if it is absent) and end. If neither can be read, show that it could not be determined. There is no automatic retry.
  - Any other 403: `access denied (403)`.
  - 5xx: state that it did not recover after retries.
  - Anything else: show `unexpected status` and the status code.
- The length and prefix of the token are not validated. GitHub has been moving to the `ghs_APPID_JWT` format since 2026-04-27, so a fixed length cannot be assumed.

## 8. systemd

Files to generate (`~/.config/systemd/user/`):

`botid-issue.service`

```ini
[Unit]
Description=Reissue botid credentials

[Service]
Type=oneshot
ExecStart="<exe>" issue
TimeoutStartSec=2min
```

`botid-issue.timer`

```ini
[Unit]
Description=Reissue botid credentials every 45 minutes

[Timer]
OnStartupSec=0
OnUnitActiveSec=45min

[Install]
WantedBy=timers.target
```

Verification status:

- In the research at planning time, it was confirmed that a version containing `Restart=on-failure` and `RestartSec=30s` passes `systemd-analyze --user verify` on the real machine (systemd 255). The final form written here (without `Restart=`, with `TimeoutStartSec=2min`) had not been passed through `verify` at that time.
- `TestGeneratedUnitsPassSystemdAnalyzeVerify` in `internal/systemd` checks this in one place. It passes the files actually written by `systemd.Install` to `systemd-analyze --user verify`, and requires that the exit code is 0 and the output is empty. It calls `install` with an executable placed at a path containing `%` (`.../100%/botid`) and passes the generated `.service` and `.timer`. If `systemd-analyze` is not present, it calls `t.Skip`.
- When `go test ./...` runs packages in parallel, `systemd-analyze` instances compete for the same private socket, so the call site is limited to one place. `internal/integration` does not call `systemd-analyze`.
- The results of running this test are not covered in this document.

Rationale for the design:

- `OnStartupSec=0` runs once when the user manager starts (at login) and right after `enable --now`. `OnUnitActiveSec=45min` counts from the start time of the previous run, so run time does not accumulate into the period.
- `TimeoutStartSec=2min` is set explicitly. For `Type=oneshot` there is no timeout by default. If `botid issue` hangs, the unit stays `activating` and all later triggers stop.
- `Restart=` is not set. This avoids continuing to call the GitHub API on a permanent failure such as an invalid private key. Temporary failures are absorbed by the retries of 5xx and network errors inside `Issue` (per request, at most 3 retries in addition to the first attempt; §7).
- The default of `AccuracySec` (1 minute) and the default of `RandomizedDelaySec` (0) are not changed. They are small enough compared with the 15-minute margin.
- `OnCalendar` is not used together with it. `INSTRUCTION.md` states that it provides no way to change the interval, so the interval is changed by the user editing the `.timer` directly. After a suspend, the next trigger is delayed, and there can be a period when the token has expired. This is accepted as a known limitation.

The path in `ExecStart`:

- Use the result of `os.Executable()` (`App.Exe`).
- If the path is not absolute, abort.
- If the path is under `os.TempDir()` or contains `go-build`, treat it as a temporary binary from `go run` and abort. It was confirmed on the real machine that the temporary directory of `go run` disappears after the run. The `os.TempDir()` check is made at the directory boundary (`/tmp-other/botid` is not treated as under `/tmp`).
- If the path contains `"`, `\`, `$`, or a newline (`\n`, `\r`), abort. The escaping rules for writing these out are unconfirmed, so they are not accepted.
- The path is enclosed in double quotes, and `%` is escaped as `%%` (it was confirmed on the real machine that quoting and escaping a path that contains `%` passes `verify`).
- The checks above run before the unit directory is created. If aborted, nothing is written and `systemctl` is not called.

Idempotency:

- `~/.config/systemd/user/` is created with 0755 if it does not exist. The permissions of an existing directory are not changed.
- The `.service` is rewritten atomically (with 0644) every time. `ExecStart` is a value derived from the path of the executable, and it must follow when the binary is moved.
- If the `.timer` already exists, it is not touched (existence is checked with `Lstat`). The user may have edited the interval by hand. If it does not exist, it is written with 0644.
- After writing, run `systemctl --user daemon-reload` and `systemctl --user enable --now botid-issue.timer`, in this order, through `Runner` (`systemd.ExecRunner` in production, using `os/exec`). `go-systemd` is not used. It adds a dependency and gives no benefit. If `daemon-reload` fails, `enable` is not run.

When `systemctl` fails:

- The output of `systemctl` is included in the error as is. The text is not parsed (it changes with the locale).
- If `systemctl` is not found, check for `exec.ErrNotFound` and print `systemctl not found; systemd is required`.
- If `/run/systemd/system` does not exist, treat it as an environment not booted with systemd and fail without writing the units (this is checked after the check of the `ExecStart` path and before the unit directory is created).
- If `XDG_RUNTIME_DIR` is empty, `systemctl --user` fails with `Failed to connect to bus` (confirmed on the real machine). Fail with that output included.
- If `enable` fails after the units were created, show the commands to enable them manually.
- In an environment with `Linger=no`, the timer does not run while the user is logged out. The linger setting is not in `INSTRUCTION.md`, so it is not changed. The output of `init` only mentions that `loginctl enable-linger` exists.

## 9. CLI behavior

- No arguments, `help`, `-h`, `--help`: print the usage to standard output and exit with code 0.
- Unknown command: print a `botid: unknown command "<name>"` line, a blank line and the usage to standard error, and exit with code 2.
- Extra arguments to any of the 3 commands: print a `botid: unexpected extra arguments for <command>: [...]` line, a blank line and the usage to standard error, and exit with code 2.
- Runtime error: print `botid: <message>` to standard error and exit with code 1. A message is, in principle, one line. However, errors that include the output of an external command (`git`, `gh`, `systemctl`), and the error that tells the user the manual commands when `enable` fails, are multi-line (§6.3, §8).
- Messages are in English.

Structure of the implementation (`main.go`):

- `main` only calls `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, newApp))`.
- `run(args []string, stdout, stderr io.Writer, newApp func() (*cmd.App, error)) int` judges the arguments, creates the `App`, runs the command, and decides the exit code. `newApp` is an argument so that tests can replace the `App`. If `newApp` fails, print `botid: <error>` and exit with code 1.
- The production `newApp` decides `HOME` with `paths.Home()`, makes `Client` create a `ghapp.Client` with `BaseURL=https://api.github.com` and `RetryDelay=2 seconds`, and sets `cmd.ExecCommand` for `Exec`, `systemd.ExecRunner` for `Systemd`, and `os.Executable` for `Exe`.
- `init` and `issue` run with `context.Background()`. The `ctx` is passed to ghapp through `app.Init` and `app.Issue`.

## 10. Implementation order

Each stage proceeds only after the tests of the previous stage pass.

1. `paths`, `config`, `ParsePEM`
   - Tests: reading and writing `config.json` (exclusivity of `privateKey` and `privateKeyPath`, the error when neither is set), reading PKCS#1 and PKCS#8, errors for encrypted PEM and invalid input. Keys are created during the test with `rsa.GenerateKey`. No private key is placed in the repository.
2. `ghapp`
   - Tests: plug `httptest.NewServer` into `BaseURL`.
     - Verify the JWT signature with the public key and check `alg`, `iss`, `iat`, and `exp` (`Now` is fixed).
     - `Installations`: 0 items, 1 item, 2 items, exclusion of suspended ones, pagination beyond 100 items.
     - `Issue` does not call `/app/installations`. It calls only `access_tokens` for the given ID and `GET /app`.
     - 5xx and network errors are retried up to 3 times in addition to the first attempt (at most 4 attempts). 4xx is not retried.
     - The error message for each status does not contain the JWT or the token.
     - `BotUserID` reaches `/users/<slug>%5Bbot%5D`. This is checked with `r.URL.EscapedPath()`.
3. `store`
   - Tests: `WriteFileAtomic` (it can replace an existing 0644 file with 0600, no temporary file is left, the directory becomes 0700). `paths` returns the configuration directory (`.config/botid`) and the generated-files directory (`.local/share/botid`) separately, and the result does not change when `XDG_CONFIG_HOME` and `XDG_DATA_HOME` are set. The output of `HostsYAML` is compared with a string literal. The quoting of `DotEnv`, and rejection of a path that contains `'`.
4. `issue`, `env`, and command dispatch
   - Tests: point `HOME` at `t.TempDir()`, run `issue` against a mock server, and check the contents of hosts.yml. The message and exit code when `config.json` is missing. The message when `installationId` is unset. The output of `env`.
5. `init`
   - Tests: record the calls with a fake `App.Exec` (`git`, `gh`) and `App.Systemd` (`systemctl`).
     - `y`, `n`, and EOF against an existing `config.json`.
     - Both input forms: pasted PEM contents and a file path.
     - With 1 installation, no selection is asked for. With 2 or more, one can be chosen by number. With an out-of-range number, a non-number, or EOF, nothing is written to disk.
     - The chosen `installationId` is saved to `config.json`.
     - `hosts.yml` and `git/config` are created under `.local/share/botid/`, and `.config/botid/` has only `config.json` and `private-key.pem`.
     - When the credentials are wrong, nothing is written to disk.
     - The environment variables passed to `git` and `gh` point to botid's paths, and `~/.gitconfig` is not touched.
6. `systemd`
   - Tests: compare the generated unit contents with string literals. Escaping of `%`, rejection of paths that contain `"`, `\`, or `$`, rejection of paths that contain `go-build` or are under `os.TempDir()`. The `.timer` is not rewritten if it exists. The `.service` is rewritten every time.
7. Integration tests with the real tools (`t.Skip` if `gh` or `git` is not in `PATH`)
   - Point `HOME`, `GH_CONFIG_DIR`, and `GIT_CONFIG_GLOBAL` at `t.TempDir()`, and write hosts.yml with a dummy token (`ghs_dummy`).
     - `gh auth token` returns the dummy token.
     - `gh auth setup-git` rewrites only the isolated git config.
     - `git credential fill` returns `username=example-app[bot]` and `password=ghs_dummy`.
     - If the parent directory of `GIT_CONFIG_GLOBAL` does not exist, `git config --global` fails with a non-zero exit code and creates neither the file nor the directory.
   - `systemd-analyze --user verify` on the generated `.service` and `.timer` is done in only one place, `TestGeneratedUnitsPassSystemdAnalyzeVerify` in `internal/systemd` (§8).
   - Before and after the run, check that the existence and modification time of the user's own `~/.config/gh/` and `~/.gitconfig` have not changed (contents are not read).

`systemctl --user enable` on the real machine is not run in tests. This is because it would register a real timer in the user's environment.

## 11. Verification

The condition for completion is to run the following and check the results.

- `go vet ./...` and `go test ./...` pass.
- `go build` passes.
- The integration tests (7) pass with the real tools.

Verifying `botid init` with the credentials of a real GitHub App can be done only by a user who has the credentials. The following remain unverified within the scope of this plan.

- That a JWT with the Client ID as `iss` is accepted by the real API (only the official documentation and the description of the reference implementation)
- That `GET /app` returns `slug` (only the schema in the official documentation, and the measurement that it returns 401 without authentication. The success case with a JWT is unverified)
- The success cases of `gh api`, `gh pr create`, `gh repo clone`, and HTTPS `git clone` and `git push` with a `ghs_` token (with a dummy token, only up to 401 can be confirmed)
- The possibility that a gh command that queries `viewer` fails with an installation token (this comes from reading the gh source and has not been measured)
- `noctx` (golangci-lint) is not enabled. It reports `exec.Command` without a context in `internal/systemd/systemd.go`, `internal/cmd/app.go` and in tests (`internal/integration/integration_test.go`). Enabling it needs a context in the `systemd.Runner` and `App.Exec` function types; this is a design decision that has not been made.

## 12. Items to settle by measurement during implementation

- The behavior of running `enable --now` twice is unconfirmed. Whether `init` fails when it is run again is handled on the premise that the output of `systemctl` is shown as is. The result becomes known only when the user runs `init` again.
- git does not create the parent directory of `GIT_CONFIG_GLOBAL`. Confirmed (asserted by the `internal/integration` test).
  - The `git does not create missing parent dir` subtest of the integration test runs `git config --global user.name ...` without `GitConfigDir`, and asserts that the exit code is non-zero and that neither `GitConfigDir` nor `GitConfigFile` is created. The environment of the child process has `LC_ALL=C`, so it also asserts that standard error contains `could not lock config file`.
  - Therefore `init` must create the parent directory (`~/.local/share/botid/git/`) with `EnsureDir` before it runs `git config --global`. This is implemented (`setupGit` in `internal/cmd/init.go`).
  - The other integration tests (`gh auth setup-git`, `git user config`, `git credential fill`) create the parent directory with `ensureGitConfigDir` before they run `git`.
- The username of `git credential fill` is `<slug>[bot]`. Confirmed (asserted by the `internal/integration` test). The `git credential fill` subtest asserts both `username=example-app[bot]` and `password=ghs_dummy` (§6.3).
