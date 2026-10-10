# Editor support

## What it is

![schema.gif](schema.gif)

A [JSON Schema](https://json-schema.org) for `den.yaml`, generated from den's own
config structs. Point your editor's YAML language server at it and you get key
completion, inline descriptions and validation while you write the config:
a misspelled key, a wrong service `type` or a missing required field is
underlined before den ever loads the file.

den also checks the file itself, every time it reads it: an error is listed
with its line and the file is not applied, and a key den does not know is a
warning that names the key you most likely meant (see
[Editing den.yaml](../config-reload/README.md)). The schema catches the same
mistakes earlier and explains each key as you type it.

## Configuration

Nothing in `den.yaml` changes. Get the schema from the binary you run, so it
always matches that version:

```bash
den schema > ~/.config/den/den.schema.json
```

Then reference it on the first line of your `den.yaml`:

```yaml
# yaml-language-server: $schema=den.schema.json
```

The path is relative to the YAML file. The `den.yaml` in this repository does the
same with the committed copy, `schema/den.schema.json`.

## Prerequisites

An editor with the YAML language server:

- **VS Code** — the *YAML* extension by Red Hat
- **JetBrains IDEs** — built in; the `$schema` comment is honoured
- **Neovim** — `yaml-language-server` through your LSP client

## Usage

```bash
den schema                     # print the schema
den schema > den.schema.json   # save it next to your den.yaml
```

### For contributors

The schema is generated from `internal/config/schema.go`; descriptions come from
the field comments. After changing a config struct, regenerate and commit it —
CI fails when the committed file is out of date:

```bash
make schema    # runs go generate, writes schema/den.schema.json
```

Mark a field required with `jsonschema:"required"` (untagged fields are optional,
because many of them can come from a named environment), and list allowed values
with `jsonschema:"enum=a,enum=b"`.
