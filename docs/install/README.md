# Install

Install den with one command, or by hand

## What it is

`install.sh` (macOS and Linux) and `install.ps1` (Windows) install the latest den
release, or the one you name. They read the project's GitHub Releases, check the
download against the release's `checksums.txt` and put `den` where you say. The same
releases are what the [Updates](../updates/README.md) pane reads.

Each release publishes the scripts next to `latest.json`, so the script you fetch is
the one that matches the newest release. A team that hosts its own copy of the releases
in S3 points the scripts at it with `DEN_INSTALL_BASE`.

## The one-liners

**macOS / Linux:**

```sh
curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://github.com/lukaszgard/den/releases/latest/download/install.ps1 | iex
```

A specific version, in another folder:

```sh
curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh -s -- --version v1.0.0 --dir /usr/local/bin
```

```powershell
$env:DEN_VERSION = "v1.0.0"
$env:DEN_INSTALL_DIR = "C:\Tools\den"
irm https://github.com/lukaszgard/den/releases/latest/download/install.ps1 | iex
```

Then check what the rest of your setup needs and write a first config:

```sh
den doctor
den init --profile acme-dev
```

## Options

`| sh` and `| iex` read the script from a pipe, so Windows takes its options from the
environment only; `install.sh` takes a flag or the same variable.

| Flag (`install.sh`) | Variable | Default | Meaning |
|---------------------|----------|---------|---------|
| `--version vX.Y.Z` | `DEN_VERSION` | the latest release | The release to install; the `v` is optional. |
| `--dir DIR` | `DEN_INSTALL_DIR` | `~/.local/bin`, on Windows `%LOCALAPPDATA%\Programs\den` | Where `den` goes; created when missing. |
| `--profile NAME` | `AWS_PROFILE` | the default profile | The AWS profile that reads an S3 mirror; unused for GitHub. |
| — | `DEN_INSTALL_BASE` | GitHub Releases | A mirror of the releases, laid out as `latest.json` and `<version>/<files>`. An `s3://` location goes through `aws`; an `https://` one through `curl` or `Invoke-WebRequest`. |
| — | `DEN_AWS_REGION` | `eu-central-1` | The mirror bucket's region. |

`install.ps1` also takes `-Version`, `-Dir` and `-Profile` when you save it and run it
as a file.

## What the script does

1. Finds the OS and CPU: macOS or Linux on amd64 or arm64. Under Rosetta it picks arm64.
2. Reads `latest.json` for the newest version, unless you named one.
3. Downloads `den_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) and the release's
   `checksums.txt` into a temporary folder.
4. Checks the archive's SHA-256 against `checksums.txt`. A mismatch, or an archive the
   list does not name, stops the install before anything is written.
5. Extracts `den` (`den.exe`) and puts it in the folder. On macOS and Linux it is copied
   next to the target and renamed over it, so a den that is running is replaced
   atomically. On Windows the old `den.exe` is renamed to `den.exe.old` first, because
   Windows will not overwrite a running program; the old file is deleted straight away,
   or on the next run when den is still running.
6. Prints `den --version`.
7. Tells you how to put the folder on your `PATH` when it is not there (on Windows it
   adds the folder to your user `PATH` itself), and warns when another `den` earlier on
   `PATH` would shadow the new one.

Nothing else is changed: no package manager runs, and nothing is written outside the
folder, the temporary folder and, on Windows, the user `PATH`.

## Prerequisites

- `curl`, `tar` and `sha256sum` or `shasum` on macOS and Linux; they ship with the system.
- PowerShell 5.1 or 7 on Windows.
- Only for an S3 mirror: the AWS CLI v2, a profile with `s3:GetObject` on the mirror, and a
  live login (`aws sso login --profile <name>`).

## Read the script first

Piping a download into a shell runs it unseen. Read it first:

```sh
curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | less
```

or save it, read it and run it:

```sh
curl -fsSL -o install.sh https://github.com/lukaszgard/den/releases/latest/download/install.sh
sh install.sh --dir ~/bin
```

The sources are `scripts/install.sh` and `scripts/install.ps1` in this repository.

## Install by hand

Download the archive for your platform from the
[Releases](https://github.com/lukaszgard/den/releases) page, or with `curl`:

```sh
curl -fsSLO https://github.com/lukaszgard/den/releases/download/v1.0.0/den_1.0.0_darwin_arm64.tar.gz
curl -fsSLO https://github.com/lukaszgard/den/releases/download/v1.0.0/checksums.txt
grep darwin_arm64 checksums.txt | shasum -a 256 -c -
tar -xzf den_1.0.0_darwin_arm64.tar.gz den
mv den ~/.local/bin/den
den --version
```

| OS | Archive |
|----|---------|
| macOS | `den_<version>_darwin_<arm64 or amd64>.tar.gz` |
| Linux | `den_<version>_linux_<arm64 or amd64>.tar.gz` |
| Windows | `den_<version>_windows_<arm64 or amd64>.zip` |

On Windows, unzip `den.exe` into a folder on your `PATH`. The version has no `v` in
the archive name.

## Build from source

Building needs the Go toolchain named in `go.mod`; an older `go` (1.21 or newer)
downloads it by itself. From a checkout:

```sh
git clone https://github.com/lukaszgard/den.git
cd den
go install ./cmd/den
```

Without a checkout: `go install github.com/lukaszgard/den/cmd/den@latest`. `make install`
builds into `~/.local/bin` instead of `$(go env GOPATH)/bin`.

## Uninstall

Delete the binary and, if you want them gone, den's own files.

```sh
rm ~/.local/bin/den
rm -rf ~/.config/den        # den.yaml, VPN settings, runbooks: your own files (`den paths` lists them)
rm -rf ~/.local/state/den ~/.local/share/den   # run history, runbook sources
```

On Windows, delete `%LOCALAPPDATA%\Programs\den` and remove that folder from your user
`PATH` (Settings, System, About, Advanced system settings, Environment Variables).

## Troubleshooting

- **`ExpiredToken`, or "Unable to locate credentials"** (an S3 mirror): the SSO login has
  expired. Run `aws sso login --profile <name>` and try again.
- **`AccessDenied` or a 403 from `aws s3 cp`** (an S3 mirror): the profile cannot read the
  bucket. It needs `s3:GetObject` on the mirror. Try another profile with `--profile`.
- **"checksum mismatch"**: the download is damaged or was altered. Nothing is installed;
  run the command again, and report it when it repeats.
- **`den` is not found after the install**: the folder is not on your `PATH`. The script
  printed the line to add to your shell's startup file; open a new terminal after adding it.
- **`den --version` shows an older version**: another `den` earlier on `PATH` shadows
  the new one. The script names it; remove it or put the install folder first.
- **Windows: `den` is not recognised**: the user `PATH` change reaches terminals opened
  afterwards. Close the terminal and open a new one.
- **"this is Windows"** in Git Bash, MSYS or Cygwin: use `install.ps1` from PowerShell.
  Inside WSL, `install.sh` installs the Linux build.
- **The one-liner says the object does not exist**: the scripts are published by a
  release. A release from before they existed has none; install by hand.
