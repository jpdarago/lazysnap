# lazysnap

A terminal UI for [tarsnap](https://www.tarsnap.com/), inspired by [lazygit](https://github.com/jesseduffield/lazygit) and [lazydocker](https://github.com/jesseduffield/lazydocker).

Tarsnap is excellent but its CLI-only interface makes routine backup management tedious. lazysnap provides a fast, keyboard-driven TUI with local caching so you can browse archives, inspect contents, create backups, and manage your tarsnap usage without waiting for repeated network round-trips.

## Design

### Architecture

```
┌─────────────────────────────────────────────────────┐
│                     lazysnap                        │
├──────────┬──────────────────────────────────────────┤
│          │                                          │
│ Archives │  Archive Detail / File Browser            │
│ (list)   │                                          │
│          │  Name:  daily-2026-04-01                  │
│ > daily… │  Size:  1.2 GB (deduplicated: 45 MB)     │
│   daily… │  Date:  2026-04-01 03:00:00              │
│   weekly…│                                          │
│   manual…│  Files:                                  │
│          │    /home/user/documents/                  │
│          │    /home/user/photos/                     │
│          │    /etc/                                  │
│          │                                          │
├──────────┼──────────────────────────────────────────┤
│ Status bar: account balance, cache age, keybindings │
└──────────┴──────────────────────────────────────────┘
```

### Panels

| Panel | Description |
|-------|-------------|
| **Archives** | List of all tarsnap archives, sorted by date. Filterable and searchable. |
| **Detail** | Metadata for the selected archive: name, size, unique/compressed size, creation date. |
| **File Browser** | Tree view of files within the selected archive. Navigate and optionally restore individual files. |
| **Status Bar** | Account balance, cache freshness, and contextual keybindings. |

### Core Features

- **Browse archives** — list, search, and filter existing tarsnap archives
- **Inspect archive contents** — view the file tree of any archive without extracting
- **Create backups** — run a new tarsnap backup from predefined or ad-hoc paths
- **Delete archives** — remove archives with confirmation
- **Restore files** — extract full archives or individual files to a target directory
- **Account info** — display current tarsnap balance and usage statistics

### Caching

Tarsnap operations are network-bound and metered (you pay per API call). lazysnap maintains a local SQLite cache to minimize unnecessary calls:

| Data | Cache Strategy |
|------|---------------|
| Archive list | Cached on first fetch. Refresh on demand or after create/delete. |
| Archive contents (file listings) | Cached per archive. Archives are immutable so these never expire. |
| Archive stats (size, unique data) | Cached per archive. Immutable. |
| Account balance | Cached with short TTL (5 min default). Manual refresh available. |

The cache lives at `~/.cache/lazysnap/cache.db` (respects `$XDG_CACHE_HOME`).

### Keybindings

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up/down |
| `Enter` | Expand / select |
| `c` | Create new archive |
| `d` | Delete archive (with confirmation) |
| `r` | Restore archive or file |
| `R` | Refresh (re-fetch from tarsnap) |
| `/` | Search / filter |
| `?` | Help |
| `q` | Quit |
| `Tab` | Switch panel focus |

### Tech Stack

| Component | Choice |
|-----------|--------|
| Language | Go |
| TUI framework | [Bubble Tea](https://github.com/charmbracelet/bubbletea) |
| TUI components | [Bubbles](https://github.com/charmbracelet/bubbles) + [Lip Gloss](https://github.com/charmbracelet/lipgloss) |
| Cache | SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO) |
| Tarsnap interface | Wraps the `tarsnap` CLI — parses stdout/stderr |
| Dev environment | [devenv](https://devenv.sh/) + [direnv](https://direnv.net/) |

### Project Structure (planned)

```
.
├── main.go                 # entrypoint
├── internal/
│   ├── tarsnap/            # tarsnap CLI wrapper
│   │   ├── client.go       # exec tarsnap commands, parse output
│   │   └── types.go        # Archive, FileEntry, Stats types
│   ├── cache/              # SQLite caching layer
│   │   ├── db.go           # open/migrate database
│   │   └── queries.go      # cached archive list, contents, stats
│   └── ui/                 # Bubble Tea TUI
│       ├── app.go          # root model, panel layout
│       ├── archives.go     # archive list panel
│       ├── detail.go       # archive detail panel
│       ├── filebrowser.go  # file tree panel
│       ├── statusbar.go    # status bar
│       ├── create.go       # create-archive modal
│       └── styles.go       # Lip Gloss styles
├── devenv.nix
├── devenv.yaml
├── go.mod
├── go.sum
└── README.md
```

### Non-Goals (for now)

- **Scheduling** — lazysnap is interactive, not a cron replacement.
- **Config file management** — it reads `tarsnap.conf` but does not manage it.
- **Multi-machine** — operates on the local machine's tarsnap keyfile only.

## Install

### Homebrew (macOS / Linux)

```sh
brew install jpdarago/tap/lazysnap
```

This also installs `tarsnap` as a dependency.

### From source

```sh
go install github.com/jpdarago/lazysnap@latest
```

Or download a prebuilt binary from the [releases page](https://github.com/jpdarago/lazysnap/releases).

## Setting up a new machine

tarsnap needs your key file to access your archives. If you keep a backup of the
key (for example in a password manager), `lazysnap init` bootstraps a fresh
machine in one step: it writes the key, generates a `~/.tarsnaprc`, and rebuilds
the local cache with `tarsnap --fsck`.

lazysnap **never** talks to a password manager — you decide how the key reaches
it, via stdin or a file:

```sh
# 1. Install (also pulls in tarsnap)
brew install jpdarago/tap/lazysnap

# 2. Provide the key. Pick whichever fits how you stored it:
pbpaste | lazysnap init                       # paste from clipboard (macOS)
lazysnap init --key-file ~/Downloads/tarsnap.key   # from an exported file
lazysnap init                                 # paste interactively, then Ctrl-D

# 3. Browse
lazysnap
```

`init` refuses to overwrite an existing key or config unless you pass `--force`,
and writes the key with `0600` permissions. See `lazysnap init --help` for all
flags (`--key-out`, `--cachedir`, `--rc`, `--no-fsck`, ...).

> **Backing up the key:** your tarsnap key file (`~/.tarsnap.key`) is the only
> thing needed to recover. Store its full contents — from `# START OF TARSNAP
> KEY FILE` to the end — somewhere safe. Without it, your archives are
> unrecoverable.

## Getting Started

```sh
# Enter dev environment (provides Go + tarsnap)
direnv allow

# Build
go build -o lazysnap .

# Run
./lazysnap
```

Requires a working tarsnap configuration (`/etc/tarsnap.conf` or `~/.tarsnaprc` with a valid keyfile).

## License

MIT
