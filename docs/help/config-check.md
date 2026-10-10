# Checking den.yaml

Each mistake in den.yaml, its line and its fix

## What it is

den checks `den.yaml` whenever it reads it, and `den config --check` does the same from the command line. It lists every problem, not the first: each with its line, a message and a hint on how to fix it. An error keeps den from using the file. A warning is something den runs with but you probably did not mean: a key it does not know, a file that is not there, two services on one local port, an AWS profile that is not defined, a name used twice.

## What you can do

- Run `den config --check` to see every problem in the file, or `den config --check other.yaml` for another file.
- Use `--json` for scripts: each problem has its line, YAML key, a stable code, the message and the hint.
- Use `--strict` in a pipeline to fail on warnings as well as errors.
- Press `p` in the Config panel to replace the file with the list of every problem, wrapped and with its hint.
- Press `enter` on a problem to see its line at the top of the file, and `n` or `N` to walk the marked lines.
- Read the count in the menu (`Config ● 3`), and the one-time note in the status bar when den starts with warnings.

## Before you start

- Nothing to install: the check only reads files, and calls no AWS API.
- It does not check tools on the path, SSO logins or ports that are busy now: `den doctor` does, and shows these warnings too.

## Usage

1. Run `den config --check` in the folder of your `den.yaml`.
2. Read each problem and the arrow line under it; the line number is in the form editors jump to.
3. Fix them, or open the Config panel and press `e` to edit in den.
4. In the panel, press `p` for the whole list when the summary above the file says there is more.
5. Run it again; the last line is the count, or `no problems`.

## Limitations

- Warnings never keep a file from loading: a config that worked before still loads.
- A relative path is read from den.yaml's folder for runbooks and VPN files, and from the folder den runs in for the others; the message shows the path it looked at.
- An RDS and a Redis service may share a name: only the same type and name twice is a duplicate.
- The first `runbooks.dirs` folder may be missing, `den runbook new` creates it; any other that is missing is reported.
- An environment no service uses is checked too: its profiles, region and instance id.
- The check cannot tell that a profile is logged in, only that it is defined.

## When it goes wrong

- A warning names a path you did not write: it is the path as den reads it, with `~` expanded and a relative path joined to the folder the message names.
- `p` does nothing in the Config panel: there are no problems to list.
- A profile is reported missing though it works: it is defined with a name den does not find, such as `[name]` without `profile ` in the AWS config; write `[profile name]`.
- Two services are reported on one port though you never set it: each type has a default local port, set `local_port` on one of them.

## See also

- Editing den.yaml
- Doctor
- Command line
- `docs/config-check/README.md`
- `docs/schema/README.md`
