# 002: per-repository identities

This document is the requirements definition and the implementation plan for per-repository identities. The requirements in §1.1 restate the user's instruction for this feature. Features that are not in §1.1 are not built. Candidates for additions are in §2.3 and are built only after the user approves them.

## 1. Requirements

### 1.1 Requirements from the instruction

| ID | Requirement |
| --- | --- |
| REQ-1 | An identity is either (a) one of one or more GitHub Apps that botid handles, or (b) the user's own act-as-user setup in the gh CLI, which means `GH_CONFIG_DIR` and `GIT_CONFIG_GLOBAL` are set to nothing. |
| REQ-2 | botid always has exactly one default identity. When nothing else is configured, botid uses it. |
| REQ-3 | A repository can be given an identity other than the default. When only the repository is configured, all worktrees of that repository follow the repository's setting. |
| REQ-4 | A branch of a repository can be given yet another identity. The identity switches when the user changes worktree, and also when the user switches branches inside a repository. |
| REQ-5 | A branch is specified by a glob pattern. |
| REQ-6 | A repository is specified by a glob pattern on its path, or by a glob pattern on its remote. Example: `github.com/octocorp/*` uses App ①, and `github.com/*/anyproduct-*` uses App ②. |
| REQ-7 | The behavior above is implemented in the botid binary. The OpenCode plugin contains only what concerns OpenCode, written simply. The plugin must not contain variable definitions, conditionals, loops, log output, or comments. |

### 1.2 Terms

- **App identity**: a GitHub App with one installation, stored by `botid init`. Its name is the App's slug.
- **User identity**: the built-in identity named `user`. It sets no environment variables, so gh and git behave as the user set them up. It has no credentials and `botid issue` does not touch it.
- **Default identity**: the identity named by `default` in `config.json`. It can be an App identity or `user`.
- **Rule**: an entry of `rules` in `config.json`. It has an optional `path`, an optional `remote`, an optional `branch`, and an `identity`.
- **Branch rule**: a rule that has `branch`. **Repository rule**: a rule that has no `branch`.
- **Repository path**: the path of the main worktree of the repository (the first entry of `git worktree list`), or the repository directory itself for a bare repository. All linked worktrees of one repository share it.

### 1.3 Decisions that interpret the requirements

| # | Question | Decision | Reason |
| --- | --- | --- | --- |
| I-1 | Which path does a `path` pattern match? | The repository path, not the path of the current worktree. | REQ-3: all worktrees follow the repository's setting. |
| I-2 | What does a `remote` pattern match? | The normalized remote string `host/owner/repo` (§4.3), for example `github.com/octocorp/widget`. | The example in REQ-6 has this form. |
| I-3 | A repository has several remotes (`origin`, `upstream`). | The `remote` pattern matches if any remote matches. | Git's own `includeIf "hasconfig:remote.*.url:..."` uses the same rule: "If there exists at least one remote URL that matches this pattern, the include condition is met" (git-config, v2.56.0). |
| I-4 | Case sensitivity | `remote` is case-insensitive. `path` and `branch` are case-sensitive. | GitHub resolves owner and repository names case-insensitively (measured: `GET /repos/OCTOCAT/hello-world` returns `full_name: octocat/Hello-World`). Paths and branch names are case-sensitive on Linux and in git. |
| I-5 | A rule has both `path` and `remote`. | Both must match. | A rule that narrows by two keys is a conjunction. |
| I-6 | Which rule wins when several match? | A matching branch rule wins over every repository rule. Among branch rules, and among repository rules, the first match in file order wins. If nothing matches, the default identity is used. | REQ-4 gives a branch a more specific identity than its repository. Example: `github.com/octocorp/anyproduct-x` matches both patterns in REQ-6. File order decides, and the user controls the order. |
| I-7 | HEAD is detached, or not under `refs/heads/`. | The repository has no branch. No branch rule matches. Repository rules and the default still apply. | There is no branch name to match. |
| I-8 | The directory is not inside a git repository. | The default identity is used. | REQ-2. |
| I-9 | A rule that applies to every repository. | `"path": "/**"`. A rule without `path` and without `remote` is invalid. | REQ-4 names a branch of a specific repository. No extra syntax is needed. |
| I-10 | When is the identity decided? | At the start of each OpenCode shell command, from the command's working directory and the branch checked out at that moment. | OpenCode starts one process per command and fires the hook per command (§3). This gives REQ-4 between commands. |
| I-11 | One identity per App? | An App identity holds one installation. Running `init` again for the same App replaces it. | Same as botid v0.1.0, which asks the user to choose one installation. |

## 2. Scope

### 2.1 To build

- Multiple App identities in `config.json`, the `user` identity, one default, and the rules of §5.
- Selection of the identity from the working directory (§4).
- `botid init` adds or replaces one App identity (§7.1).
- `botid issue` issues credentials for every App identity (§7.2).
- `botid env` prints the variables of the selected identity (§7.3).
- The OpenCode plugin (§8).

### 2.2 Not to build

- Anything that changes how GitHub Apps authenticate, how tokens are issued, or the systemd units (`botid-issue.service` / `.timer` stay as they are).
- Wrappers for gh / git, and support for GitHub Enterprise Server (unchanged from 001).
- Switching identity inside one shell command (§11, L1).
- Support for the v0.1.0 layout after the migration. botid converts the files once (§2.4) and reads only the new format afterwards.

### 2.3 Additions awaiting approval

None of these is in the instruction. They are not part of this plan's implementation until the user approves each one.

| # | Addition | Why it might be wanted |
| --- | --- | --- |
| A-1 | `botid use <identity> [--path G] [--remote G] [--branch G]` to write rules and the default. | Without it the user edits `config.json` by hand. 001 also leaves configuration to the user's own editing. |
| A-2 | `botid init --name NAME` to name an identity independently of the slug. | Needed to keep two installations of the same App, and to avoid the slug `user` (§11, L9). |
| A-3 | `botid which` (print the selected identity and the rule that selected it), and a command that lists identities and rules. | Debugging a rule. |
| A-4 | Commands that remove an identity or a rule. | Cleanup without editing `config.json`. |

### 2.4 Changes for people who already use v0.1.0

v0.1.0 (tag `v0.1.0`, an immutable release) wrote a flat `config.json` and one set of credentials. This feature changes that layout (§5), so:

1. The first botid command that reads a v0.1.0 `config.json` (`init`, `issue` or `env`) migrates it. No separate command exists, because the plugin runs `botid env` before every shell command and the systemd timer runs `botid issue`, so the migration happens without the user doing anything.
2. The migration (§7.5) turns the v0.1.0 App into an identity named after its App slug and makes it the default. The slug comes from `GET /app`, so the first run needs the network and the private key that `config.json` points to. If the request fails, the command fails with the reason, the old files stay as they were, and the next run tries again.
3. The v0.1.0 `gh/` and `git/` directories are moved to the identity's directories, so the git settings and the credentials keep working. The private key is not moved. The new `config.json` points to the place where the old one pointed.
4. `botid env` now depends on the working directory and on `config.json`. Its stderr notice ("Set the environment variables above once...") is removed, because its values are no longer fixed. During the first run after the upgrade it prints the migration notice to stderr (§7.5).
5. The environment variables that the v0.1.0 completion message told the user to set once must be removed from the environment that starts OpenCode. The plugin sets the variables per command for App identities, but for the `user` identity it sets nothing, so a leftover `GH_CONFIG_DIR` would keep using the old bot credentials.
6. With the plugin installed, update `botid` before or together with the plugin. A v0.1.0 `botid` on the `PATH` rejects `env --format json`, which blocks every shell command (§12). A new `botid` with a v0.1.0 `config.json` is not a problem, because it migrates the file on the first command.

Design choice: the migration needs the network because the identity name is the App slug. The alternative, reading the slug from the v0.1.0 `hosts.yml`, would work offline, but the file is a derived copy that botid does not own after a user edit. `GET /app` is the source that `init` uses.

## 3. Primary sources consulted

OpenCode v2.2.5 (npm `@opencode/plugin` 2.2.5; git tag `v2.2.5`, commit `9bbb8ce`; the local `opencode --version` is v2.2.5):

- Docs: https://opencode.ai/v2/docs/build/plugins and https://opencode.ai/v2/docs/server ("Shell": `ctx.shell.hook("create.before", ...)`, `ShellCreateBefore { command, cwd, timeout, shell, env }`).
- `packages/core/src/shell.ts`: `Shell.create` spawns one non-interactive process per command. It builds `{command, cwd: input.cwd ?? location.directory, timeout, shell, env}`, triggers `create.before`, and spawns with `invocation.cwd` and `invocation.env` read after the hooks, so changes to `event.env` take effect.
- `packages/core/src/tool/plugin/shell.ts`: the shell tool's own `before` step runs after the plugin hooks and resolves `~`, `~/x` and relative `cwd` against `location.directory`. At hook time `event.cwd` is therefore the raw value.
- `packages/core/src/pty.ts`: `Pty.create` spawns interactive terminals with `process.env` and `input.env`. It does not call `Shell.create` and has no hook.
- `packages/plugin/src/promise/adapter.ts`: a Promise-based hook is wrapped in `Effect.promise`, so a rejection becomes a defect and aborts `Shell.create` before the spawn.
- Measured on a local `opencode serve` with isolated `XDG_*` directories and a probe plugin:
  - The plugin runtime is Bun. `ctx.location.directory` is the project directory.
  - Through `POST /api/shell`, an absolute `cwd`, `"."` and `"~"` reach the hook unchanged. Without `cwd`, the hook sees `location.directory`.
  - Variables set on `event.env` reach the child process.
  - A hook that throws makes the request return HTTP 500 with an empty body. The command is not spawned. The cause (`Error: <message>`) is written to `opencode.log`.
  - The first requests to `/api/shell` right after the server starts can run before the plugin is active.

git (documentation and source at tag v2.56.0):

- `git worktree list`: "The main worktree is listed first". `--porcelain -z` exists since Git 2.36 (RelNotes 2.36: "introducing NUL terminated output format with `-z`").
- `git remote -v` (`builtin/remote.c`): prints the first fetch URL of each remote and every push URL, after `insteadOf` / `pushInsteadOf` rewriting.
- `git symbolic-ref -q HEAD` exits with status 1, without a message, when HEAD is detached. `--short` can print `heads/foo` when a tag `foo` exists, so the plan uses the full ref.
- gitignore(5), also used by `includeIf` conditions: `*` matches anything except a slash; a leading `**/` matches in all directories; a trailing `/**` matches everything inside; `/**/` matches zero or more directories; other consecutive asterisks are regular asterisks.
- git-config "Conditional includes": `gitdir` matches both the symlink and the realpath version of a path; `worktree` matches only the realpath; `onbranch` and `hasconfig:remote.*.url` use the same glob dialect.

## 4. Behavior

### 4.1 Selection

`Select(info, home)` is a function of the repository facts and the config only.

1. If `info` says the directory is not in a repository, return `default`.
2. Go through the rules that have `branch`, in file order. Return the `identity` of the first rule that matches.
3. Go through the rules that have no `branch`, in file order. Return the `identity` of the first rule that matches.
4. Return `default`.

A rule matches when all of its present keys match:

- `path`: the glob matches the repository path (§4.2).
- `remote`: the glob, in lowercase, matches at least one normalized remote (§4.3), which is already in lowercase.
- `branch`: `info.Branch` is not empty and the glob matches it.

### 4.2 Glob

One function serves `path`, `remote` and `branch`. The pattern and the subject are split on `/`.

- A segment `**` matches zero or more segments. When `**` is the last segment of a pattern that has more than one segment, it matches one or more segments (gitignore(5): "`abc/**` matches all files inside directory `abc`"). A pattern that is only `**` matches any non-empty subject.
- Any other segment is matched with `path.Match`, so `*`, `?`, `[...]` and `\` escapes work, and `*` never matches `/`.
- `**` inside a longer segment (`a**b`) is two ordinary asterisks.
- A pattern is invalid if it is empty, or has an empty segment (`a//b`, a trailing `/`), or a segment that `path.Match` rejects. A `path` pattern may start with one `/`. Validity is checked when `config.json` is loaded, so a bad pattern is a load error and no command runs with a half-understood rule.

`path` patterns:

- A leading `~/` is expanded to `$HOME` (`App.Home`) when matching. After expansion the pattern must start with `/`; otherwise the rule is invalid.
- Symlinks are resolved on both sides before matching: the repository path with `filepath.EvalSymlinks`, and the literal leading segments of the pattern (the segments before the first one that contains `*`, `?`, `[` or `\`) with `filepath.EvalSymlinks`. If the literal prefix does not exist, it is used as written. This follows `includeIf "gitdir:"`, which matches both the symlink and the realpath form.

`branch` patterns: `release/*` does not match `release/1.0/rc`; use `release/**`.

### 4.3 Remote normalization

`repo.Normalize(url)` turns a remote URL into `host/path`, then lowercases it.

- `scheme://[user[:pw]@]host[:port]/path` (https, ssh, git, file): `host` is `u.Hostname()` (no userinfo, no port) followed by the decoded path. For an empty host (`file:///srv/r.git`) the result is the path.
- scp-like `[user@]host:path` (a colon before the first slash, no `://`): `host/` followed by the path without its leading `/`.
- Anything else is a local path and is kept as is.
- A trailing `/` and a trailing `.git` are removed.

Examples. `https://github.com/OctoCorp/Widget.git`, `git@github.com:octocorp/widget.git` and `ssh://git@github.com:22/octocorp/widget` all become `github.com/octocorp/widget`.

A URL that cannot be parsed is an error that names the remote. It is not skipped, so a rule can never be bypassed by a remote that botid failed to read.

### 4.4 Repository inspection

`repo.Inspect(ctx, run, cwd)` runs three git commands with `git -C <cwd>`:

1. `worktree list --porcelain -z`. The first record's `worktree <path>` line is the repository path.
2. `symbolic-ref -q HEAD`. Exit status 1 means no branch. Otherwise strip `refs/heads/`; a ref outside `refs/heads/` means no branch. Other failures are errors.
3. `remote -v`. Each line must match `^(\S+)\t(.+) \((fetch|push)\)( \[.*\])?$`. A line that does not match is an error. The URLs of both kinds are normalized and de-duplicated.

Handling of failures:

- `cwd` must be an existing directory. Otherwise `Inspect` fails with `cwd does not exist: <path>`. The command could not run in that directory either.
- If step 1 fails with exit status 128 and its stderr contains `not a git repository`, the directory is not in a repository: `Inspect` returns an empty `Info` and no error. Every other failure (for example `dubious ownership`, or a git older than 2.36) is returned as an error that carries git's stderr. The identity then cannot be decided and the caller fails closed.
- The git processes run with `LC_ALL=C` so the message text is stable, and without `GIT_CONFIG_GLOBAL` so that the result does not depend on the identity that was selected for a previous command. `~/.gitconfig` (including `insteadOf` rules) is read as usual.

## 5. `config.json` and directory layout

```
~/.config/botid/
  config.json                           0600
  identities/<name>/private-key.pem     0600   (directories 0700)

~/.local/share/botid/
  identities/<name>/gh/hosts.yml        0600
  identities/<name>/git/config
```

`config.json`:

```json
{
  "default": "bot-a",
  "identities": {
    "bot-a": {
      "clientId": "Iv23...",
      "privateKeyPath": "/home/me/.config/botid/identities/bot-a/private-key.pem",
      "installationId": 12345
    },
    "bot-b": {
      "clientId": "Iv23...",
      "privateKeyPath": "/home/me/.config/botid/identities/bot-b/private-key.pem",
      "installationId": 67890
    }
  },
  "rules": [
    { "remote": "github.com/octocorp/*", "identity": "bot-a" },
    { "remote": "github.com/*/anyproduct-*", "identity": "bot-b" },
    { "path": "~/src/private/**", "branch": "release/**", "identity": "user" }
  ]
}
```

Validation at load (the loader keeps `DisallowUnknownFields` and the trailing-content check of v0.1.0):

- `default` is required. It is `user` or a key of `identities`.
- A key of `identities` matches `^[A-Za-z0-9][A-Za-z0-9._-]*$` and is not `user`. The key is used as a directory name, so it cannot be empty, contain `/`, or start with `.`.
- Each identity keeps the v0.1.0 rules: `clientId` is not empty, and exactly one of `privateKey` and `privateKeyPath` is set. `installationId` of 0 is accepted when loading; `issue` reports it for that identity (§7.2).
- A rule has `identity` (`user` or a key of `identities`), and at least one of `path` and `remote`. Its patterns are valid (§4.2).
- A top-level `clientId` means a v0.1.0 file. Load returns an error that satisfies `errors.Is(err, config.ErrLegacy)` (`<path> was written by botid v0.1.0`). The commands catch it and migrate (§7.5). `config.LoadLegacy` reads such a file as an `Identity` with the rules above.

`Save` writes indented JSON atomically with 0600. `identities` keys are written in sorted order and `rules` keep their order. JSON has no comments, so a save loses nothing the loader would have accepted.

Directory handling is as in 001 §3: directories are created with 0700 and then `Chmod(0700)`; files are written atomically. `EnsureDir` is called for each directory botid creates (`identities/`, `identities/<name>/`, `gh/`, `git/`). The paths stay fixed to `$HOME/.config/botid` and `$HOME/.local/share/botid`, without `XDG_*`.

## 6. Repository layout and types

New or changed packages:

```
internal/glob/      Validate, Match (standard library only)
internal/repo/      Normalize, Inspect, Runner, ExecGit
internal/paths/     per-identity paths
internal/config/    config.json v2, Select
internal/store/     adds JSONEnv
internal/cmd/       Init, Issue, Env changed
main.go             flag parsing for env
plugin/opencode/    botid.ts rewritten
```

No external dependency is added.

```go
// internal/glob
func Validate(pattern string) error
func Match(pattern, name string) bool        // the caller has validated the pattern

// internal/repo
type Info struct {
    Path    string   // repository path (§1.2); "" when the directory is not in a repository
    Branch  string   // "" when there is no branch (§1.3 I-7)
    Remotes []string // normalized, lowercase, de-duplicated
}
type ExitError struct {
    Code   int
    Stderr string
}
func (e *ExitError) Error() string
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error) // stdout; a non-zero exit is an *ExitError
func ExecGit(ctx context.Context, dir string, args ...string) ([]byte, error)    // runs git -C dir with LC_ALL=C and without GIT_CONFIG_GLOBAL
func Inspect(ctx context.Context, run Runner, cwd string) (Info, error)
func Normalize(remoteURL string) (string, error)

// internal/paths
type Paths struct {
    ConfigDir, ConfigFile string // ~/.config/botid, config.json
    DataDir               string // ~/.local/share/botid
}
type Identity struct {
    ConfigDir, PrivateKeyFile                    string // identities/<name>/, private-key.pem
    DataDir, GHConfigDir, HostsFile              string
    GitConfigDir, GitConfigFile                  string
}
func New(home string) Paths
func (p Paths) Identity(name string) Identity // name must have passed config.ValidName

// internal/config
const User = "user"
type Identity struct {
    ClientID       string `json:"clientId"`
    PrivateKey     string `json:"privateKey,omitempty"`
    PrivateKeyPath string `json:"privateKeyPath,omitempty"`
    InstallationID int64  `json:"installationId"`
}
type Rule struct {
    Path     string `json:"path,omitempty"`
    Remote   string `json:"remote,omitempty"`
    Branch   string `json:"branch,omitempty"`
    Identity string `json:"identity"`
}
type Config struct {
    Default    string              `json:"default"`
    Identities map[string]Identity `json:"identities"`
    Rules      []Rule              `json:"rules,omitempty"`
}
var ErrLegacy error
func Load(path string) (Config, error)
func LoadLegacy(path string) (Identity, error)                // the top level of a v0.1.0 file
func Save(path string, c Config) error
func ValidName(name string) error
func (c Config) AppNames() []string                        // sorted keys of Identities
func (i Identity) Key() (*rsa.PrivateKey, error)
func (c Config) Select(info repo.Info, home string) string // §4.1

// internal/store
func JSONEnv(ghConfigDir, gitConfigGlobal string) (string, error) // {"GH_CONFIG_DIR":"...","GIT_CONFIG_GLOBAL":"..."}\n

// internal/cmd
type App struct {
    Home, Cwd string // Cwd is the working directory of the botid process
    Stdin     io.Reader
    Stdout    io.Writer
    Stderr    io.Writer
    Client    func(clientID string, key *rsa.PrivateKey) *ghapp.Client
    Exec      func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) // git and gh that init runs
    Git       repo.Runner                                                                         // git that env runs
    Systemd   systemd.Runner
    Exe       func() (string, error)
}
func (a *App) Init(ctx context.Context) error
func (a *App) Issue(ctx context.Context) error
func (a *App) Env(ctx context.Context, cwd, format string) error // cwd "" means a.Cwd; format "dotenv" or "json"
```

`config.Identity` replaces v0.1.0's `config.Config` (same four fields). `paths.Paths` loses its per-identity fields in the same change. All callers move in the same stage (§9); no compatibility layer is kept.

`Exec` stays for `init` because it returns combined output, which is what an error message needs. `Git` is separate because `Inspect` parses stdout and must tell stderr apart.

## 7. Command specifications

### 7.1 `botid init`

Prompts and the choice of installation are those of 001 §6.3. The differences are:

0. `systemd.Preflight` runs first, as in 001.
1. If `config.json` exists, load it. A v0.1.0 file (`ErrLegacy`) is migrated first (§7.5), so the App that it holds stays an identity and `init` can add another one next to it. If the migration fails, `init` ends with that error. Any other load error also ends `init` with that error, so a typo in hand-written rules is never overwritten.
2. Read `Client ID: ` and the private key, parse the key, choose the installation, and issue a token. Nothing is written before this succeeds.
3. The identity name is the slug from `GET /app`. If the slug fails `ValidName` or equals `user`, end with an error that names the slug (§11, L9).
4. If the config already has an identity with that name, warn `Warning: identity "<name>" already exists in <path>. If you continue, its credentials and private key will be overwritten.` and ask `Overwrite? [y/n]: `. Anything other than `y` changes nothing and exits with 0.
5. Write, in this order: `identities/<name>/private-key.pem` (config side), `gh/hosts.yml`, then the git setup of 001 §6.3 step 7 against the identity's `git/` and `gh/` directories (bot user id, `user.name`, `user.email`, `gh auth setup-git`), and last `config.json` with the identity added or replaced. The config is written last so that it never refers to an identity whose files are incomplete.
6. When `config.json` did not exist, the new identity is also the `default`. In every other case (including a migrated file) `default` and `rules` are kept as they are.
7. Register the systemd timer as in 001 §8. It is idempotent.
8. Print what was done: the identity name, whether it is the default, and that rules are assigned by editing `config.json`, with its path. The line "Run botid env and set the printed environment variables once..." is removed.

If a step after 5 fails, written files are not rolled back, as in 001.

### 7.2 `botid issue`

1. Load `config.json`. A missing file gives `<path> does not exist; run botid init`. A v0.1.0 file is migrated first (§7.5).
2. For each App identity, in sorted name order:
   1. If `installationId` is 0: `has no installationId; run botid init again`.
   2. Read the key, issue the token (001 §7), build the hosts.yml content, `EnsureDir` the identity's data and `gh/` directories, and replace `hosts.yml` atomically (0600).
3. A failure for one identity does not stop the others. The errors are collected in name order, each prefixed with `identity "<name>": `, joined with `errors.Join`, and the command exits with 1 when there is any.

With no App identity (only `user`), the command does nothing and exits with 0.

### 7.3 `botid env [--cwd DIR] [--format dotenv|json]`

1. Load `config.json` (same errors as `issue`).
2. `DIR` defaults to the working directory of the process. `~` and `~/x` are expanded with `$HOME`. A relative path is joined to the working directory of the process. The result is cleaned.
3. `repo.Inspect(ctx, a.Git, DIR)`, then `Config.Select(info, a.Home)`.
4. Print to stdout:
   - App identity, `dotenv`: `GH_CONFIG_DIR='…/identities/<name>/gh'` and `GIT_CONFIG_GLOBAL='…/identities/<name>/git/config'` in the quoting of 001 §6.2.
   - App identity, `json`: the same two variables as one JSON object and a newline.
   - `user` identity: nothing for `dotenv`, `{}` and a newline for `json`.
5. Nothing is printed to stderr on success. On failure the error goes to stderr with exit code 1, and stdout stays empty.

`dotenv` is the default format, so `botid env` keeps its v0.1.0 output form. `--cwd` and `--format` exist so the plugin (§8) can hand over the command's directory and get JSON back without parsing code.

### 7.4 Argument parsing (`main.go`)

- `init` and `issue` take no arguments. Extra arguments still exit with 2.
- `env` is parsed with a `flag.FlagSet` (`ContinueOnError`, output discarded). A parse error, a positional argument, or a `--format` other than `dotenv` / `json` prints `botid: <message>`, a blank line and the usage to stderr, and exits with 2.
- The usage text describes `env` as: `Print the environment variables that point gh and git at the identity selected for a directory, in .env or JSON format`.
- `newApp` sets `Cwd` from `os.Getwd()` and `Git` to `repo.ExecGit`.

### 7.5 Migration of a v0.1.0 `config.json`

`init`, `issue` and `env` call `App.migrate` when `config.Load` returns `ErrLegacy`.

1. Open the old `config.json` and take an exclusive `flock` on it. A concurrent command (the plugin starts one `botid env` per shell command) waits here. When it gets the lock, it loads `config.json` again. The path now holds the new file, so it uses that and does nothing else.
2. Read the file with `config.LoadLegacy`. An `installationId` of 0 is an error (`installationId is not set`).
3. Read the private key, issue a token and read the App slug (`Client.Issue`). The slug is the identity name and must pass `config.ValidName`.
4. Create `identities/<slug>/` under the data directory, then move `<data>/gh` to `identities/<slug>/gh` and `<data>/git` to `identities/<slug>/git` with `os.Rename`. A directory that does not exist is skipped, because an earlier run moved it or v0.1.0 never created it. A destination that is not empty makes the rename fail, and the command fails with the error of `os.Rename`.
5. Write the `hosts.yml` of the new identity from the token of step 3.
6. Write the new `config.json` last, atomically: `default` is the slug and the identity has the `clientId`, the key (`privateKey` or the same `privateKeyPath`) and the `installationId` of the old file.
7. Print to stderr `botid: migrated <path> from the format of botid v0.1.0; the identity "<slug>" is the default`, and `botid: moved <old> to <new>` for each directory that was moved. Nothing goes to stdout, so the output of `env` stays parseable.

An error in steps 2 to 6 is returned as `cannot migrate <path> from the format of botid v0.1.0: <reason> (to discard the old configuration instead, remove the file and run botid init)`. The old `config.json` is not changed in that case, and every step is safe to repeat: a second run skips the directories that the first run moved, and issues a new token. The private key is never moved or deleted, so a failed run cannot lose it.

## 8. OpenCode plugin

`plugin/opencode/botid.ts` becomes:

```ts
import { execFile } from "node:child_process"
import { promisify } from "node:util"
import { Plugin } from "@opencode/plugin"

export default Plugin.define({
  id: "botid.env",
  async setup(ctx) {
    await ctx.shell.hook("create.before", async (event) => {
      Object.assign(
        event.env,
        JSON.parse(
          (await promisify(execFile)("botid", ["env", "--format", "json", "--cwd", event.cwd], { cwd: ctx.location.directory })).stdout
        )
      )
    })
  }
})
```

- The hook is one expression. It defines no variable (the three `import` lines load the modules; `ctx` and `event` are parameters, as in the current file), and it has no conditional, loop, log output or comment.
- `event.cwd` is passed on as it arrives, raw. It reaches the hook as the caller wrote it, because OpenCode resolves it after the hooks have run (`FileAccess.resolvePath` in `packages/core/src/file-access.ts`, tag v2.2.5). `botid env --cwd` accepts any directory path in the usual way: it expands a leading `~` to the home directory, and it joins a relative path to the working directory of its own process, which the plugin sets to `ctx.location.directory`. Neither rule mentions OpenCode, and the plugin does not repeat them.
- A rejection (`botid` missing from `PATH`, a config error, a git error) aborts `Shell.create` and the command is not run (source and measurement in §3). The cause appears in `opencode.log`. This is fail-closed: no command runs under an identity that botid could not decide.
- The hook runs for the shell tool, the session `!` shell and `/api/shell`, which all call `Shell.create`. It does not run for interactive terminals (`Pty.create`).
- The `botid` binary must be on the `PATH` of the OpenCode service process.

## 9. Implementation order

Each stage ends with `go vet ./...` and `go test ./...` passing. Stages 3 and 4 are one change, because replacing the single-identity types breaks their callers, and the callers are migrated in the same change.

1. `glob`
   - Tests (table): the examples of §4.2; `*` and `?` and `[a-z]` do not cross `/`; `**` leading, middle and trailing; trailing `**` needs one or more segments; `a**b`; escapes; invalid patterns (empty, `a//b`, trailing `/`, `[`).
2. `repo`
   - `Normalize` tests (table): the three forms of §4.3, ports, userinfo, upper case, `.git`, trailing `/`, `file://`, a local path, an unparsable URL.
   - `Inspect` tests with a fake `Runner`: a git failure other than `not a git repository` is an error; `ExitError` code 1 on `symbolic-ref` means no branch; an unparsable `remote -v` line is an error; a non-existing `cwd` is an error.
   - `Inspect` tests with the real git in `t.TempDir()` (in `internal/integration`): main and linked worktree return the same `Path`; `git switch` changes `Branch`; detached HEAD; an unborn branch; a bare repository; a directory outside a repository; two remotes; `url.<base>.insteadOf`; a symlinked parent directory. These also settle the items of §12.
3. `paths`, `config`
   - `Load` tests: every rule of §5, the v0.1.0 file (`errors.Is(err, ErrLegacy)`), `LoadLegacy` for a key file, an inline key and an unset `installationId`, unknown fields, trailing content, unknown identity in `default` or in a rule, `user` as an identity key, a bad glob.
   - `Save` and `Load` round trip with 0600.
   - `Select` tests: the two examples of REQ-6 on `github.com/octocorp/widget`, `github.com/someone/anyproduct-x` and `github.com/octocorp/anyproduct-x` (file order decides the last one); a branch rule beats a repository rule listed before it; no branch; not in a repository; `path` with `~/`; a `path` given through a symlinked directory; `remote` case; a rule with `path` and `remote`.
4. `cmd`, `store`, `main`
   - `Init`: first identity becomes the default; a second identity keeps `default` and `rules`; the same slug asks for confirmation; `n` and EOF change nothing; a v0.1.0 file is migrated first and `init` then asks about the same slug; a broken `config.json` is never overwritten; the config is written after the files; a failing git step leaves `config.json` unchanged; the slug `user` is rejected.
   - `Issue`: every identity gets its `hosts.yml`; one failing identity does not stop the others, the others are written, the exit code is 1 and the message names the failing identity; no App identity is a no-op; a missing file gives the message of §7.2; a v0.1.0 file is migrated and then issued.
    - Migration (§7.5): `env` migrates a v0.1.0 file and prints the identity's variables; an inline key is kept; missing `gh/` and `git/` directories are skipped; each failure (`installationId` unset, missing key file, GitHub rejects the credentials, an App slug that is not a valid name) leaves every old file as it was; a run that stopped at a non-empty destination finishes when repeated; eight commands that start together migrate once (one token request) and all print the same output.
   - `Env`: the selected identity's two variables in both formats; `user` prints nothing / `{}`; `--cwd` relative, absolute and with `~`; the default `cwd`; a missing config; stdout stays empty on failure.
   - `main`: `env` flags, an unknown flag, a bad `--format`, extra arguments for `init` and `issue`, exit codes.
   - The tests of `internal/integration` use `paths.New(home).Identity(name)`.
5. Plugin: replace `plugin/opencode/botid.ts` with §8.
6. End-to-end with a real OpenCode (§10), run by hand. The repository keeps no test that starts OpenCode.
7. README: describe `config.json` (identities, `default`, `rules`, the glob dialect, the precedence of §1.3 I-6) and the plugin requirements (§8, §2.4 item 4). README is currently the user's draft (`# botid` and the `botid init` block), so the wording is shown to the user.

## 10. Verification

Done means all of these pass:

- `go vet ./...`, `go test ./...`, `go build`.
- `golangci-lint run ./...` and `golangci-lint fmt --diff`, which `lefthook.yml` runs on commit.

End-to-end with a real OpenCode v2.2.5, run by hand with a throwaway harness under `/tmp/opencode`. No file in the repository starts OpenCode or reads the plugin, so botid, its tests and its build stay independent of OpenCode (REQ-7). The harness used an isolated server (`XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME` under `/tmp/opencode`, `opencode serve --hostname 127.0.0.1`), the real `botid` binary built from the tree, a temporary `HOME` holding a v2 `config.json` with two App identities, and the plugin in the project's `.opencode/plugins/`. The harness waits until `GET /api/plugin` reports the plugin as active before it sends commands. Each check runs `printenv GH_CONFIG_DIR GIT_CONFIG_GLOBAL` through `POST /api/shell` and compares the output with a literal expected value:

| Case | Expected |
| --- | --- |
| Repository without rules | The default identity's paths |
| Repository with a `remote` rule | The rule's identity |
| Repository with a `path` rule | The rule's identity |
| Linked worktree of that repository | The same identity as the main worktree |
| Branch with a matching branch rule | The branch rule's identity |
| `git switch` back and forth, one command per switch | The identity follows the branch from the next command on |
| Rule identity `user`, and default `user` | Both variables empty |
| Directory outside a repository | The default identity |
| `cwd` given as `.` and a relative path | Resolved against the project directory |
| `botid` removed from `PATH`, or `config.json` broken | The command is not run, and `opencode.log` has the cause |

`cwd` given as `~` or `~/x` is not part of this matrix. `POST /api/shell` does not expand `~` when it spawns the process (the server log shows `NotFound: FileSystem.access (~)`), so the command fails there whatever the hook does. The model-facing shell tool resolves `~` after the hook, but that path was not driven (§12). The expansion in `botid env` is covered by the unit test `TestEnvResolvesCwd` and a run of the built binary with a temporary `HOME`.

The harness ran once against the tree on 2026-11-06, with `botid` built from the tree, any installed `botid` removed from the `PATH` of the server, and the plugin of §8 copied into the project. All rows passed. The harness is not kept in the repository.

With the real `gh` and `git` (the existing integration tests, run per identity): `gh auth token` returns each identity's own dummy token, `git config user.email` returns each identity's own address, and `~/.config/gh` and `~/.gitconfig` are unchanged.

Not possible without credentials of a real GitHub App: that `init` and `issue` work against the real API for two Apps (the same limit as 001 §11).

## 11. Limitations

- **L1.** The identity is decided when a command starts. `git switch x && git push` and `cd ../other && git push` run under the identity of the starting directory and branch. Run them as separate commands, and pass the directory through the tool's `workdir` instead of `cd`. Switching inside a command would need wrappers for gh / git, which are out of scope (001 §1).
- **L2.** Interactive terminals (`Pty.create`) do not call the hook, so they run with the environment of the OpenCode process.
- **L3.** A `POST /api/shell` sent right after the server starts can run before the plugin is active. The shell tool and the session `!` shell exist only after activation (source: `SessionShell.start` awaits `Plugin.awaitActivation`).
- **L4.** If `botid` is not on the `PATH` of the OpenCode service, or botid fails, no shell command runs (fail-closed, §8).
- **L5.** `git remote -v` shows only the first fetch URL of a remote. `insteadOf` rewriting is applied before matching.
- **L6.** `GH_TOKEN` and `GITHUB_TOKEN` in the inherited environment take precedence over `hosts.yml` (001 §2). Leftover `GH_CONFIG_DIR` / `GIT_CONFIG_GLOBAL` affect the `user` identity (§2.4 item 4).
- **L7.** One installation per App identity (§1.3 I-11).
- **L8.** `botid env` needs git 2.36 or newer (`worktree list -z`). An older git makes `Inspect` fail with git's message.
- **L9.** The App slug `user` cannot be used, because the name is reserved. `init` stops with an error.
- **L10.** Two `init` runs at the same moment can lose one update of `config.json` (read, modify, write). `init` is interactive.
- **L11.** Every shell command costs one `botid` process and three git processes. The cost is measured in §12.
- **L12.** The migration of a v0.1.0 file (§7.5) needs the network once. Until it succeeds, `env` fails and the plugin blocks every shell command (fail-closed, L4). The error message names the reason. Removing the old `config.json` and running `botid init` discards the old configuration instead.

## 12. Items to settle by measurement during implementation

Settled by measurement:

- `promisify(execFile)` under the Bun runtime of OpenCode v2.2.5 resolves to an object with `stdout`. The unmodified plugin of §8 selects the identity in the end-to-end run of §10.
- A local plugin that imports `Plugin` from `@opencode/plugin` loads and reports `active` in `.opencode/plugins/` without any package installation.
- When the hook rejects, `POST /api/shell` returns HTTP 500, the command is not spawned, and `opencode.log` holds the cause. For a broken `config.json` the cause contains `botid env --format json --cwd <dir>` and botid's message (`botid: cannot read <path>`). For a missing `botid` the cause is Bun's `Executable not found in $PATH: "botid"`, not Node's `spawn botid ENOENT`. The log is flushed with a delay of up to a few seconds, so the harness polled it.
- `botid` v0.1.0 on the `PATH` rejects `env --format json` (`unexpected extra arguments for env`). With such a binary on the `PATH`, every command is blocked. Update `botid` together with the plugin.
- git (the version on the development machine, through `internal/integration/repo_test.go`): `not a git repository` appears on stderr with exit status 128 under `LC_ALL=C`; `symbolic-ref -q HEAD` on an unborn branch returns the branch name; a bare repository reports the bare directory as `Path` and `HEAD`'s branch; a path that contains a newline works with `-z`; a repository reached through a symlink is reported with its real path; a linked worktree and a subdirectory report the main worktree's path; the caller's `GIT_CONFIG_GLOBAL` does not change the result.
- `path.Match(segment, "")` returns `ErrBadPattern` for every malformed segment that the glob tests contain (`[`, `a[`, `[a-`, a trailing `\`, `a/[/b`), so `Validate` relies on it.
- Time: 50 runs of `botid env --format json --cwd <repository>` took 0.61 s in total, about 12 ms per run including the three git processes (built binary, default identity `user`, this repository).

Not measured:

- How the model-facing shell tool presents a failed hook. By the source, `Effect.mapError` in the tool maps only typed failures, so the defect is not mapped. The check needs a session driven by a model, for example a local mock provider that returns a shell tool call. The acceptance condition is that the command does not run and the cause is in `opencode.log`.
- `cwd` given as `~` or `~/x` through the model-facing shell tool (§10).
- `POST /api/shell` right after the server starts (L3). The first requests found the plugin active in the end-to-end run, so the race did not reproduce there.
