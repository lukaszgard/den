// Package vault reads HashiCorp Vault KV secrets (v1 and v2) through the
// vault CLI. The CLI brings the user's own token (~/.vault-token, VAULT_TOKEN
// or a token helper), so den never handles Vault credentials; when there is
// none, the pane can run the configured login command.
package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/secrets"
)

// readTimeout bounds one read, or one whole folder listing.
const readTimeout = 30 * time.Second

// A folder listing costs one CLI call per sub-folder, so a big tree is cut
// off rather than left to hang the pane.
const (
	maxEntries = 500
	maxDepth   = 10
)

const maxErrLen = 300

// Item is one Vault secret, or a folder of them.
type Item struct {
	spec config.VaultSecretSpec
}

var (
	_ secrets.Item           = (*Item)(nil)
	_ secrets.Browser        = (*Item)(nil)
	_ secrets.LoginCommander = (*Item)(nil)
	_ secrets.Viewer         = (*Item)(nil)
)

// New builds an item from its config entry.
func New(spec config.VaultSecretSpec) *Item {
	if spec.Mount == "" {
		spec.Mount = "secret"
	}
	spec.Mount = strings.Trim(spec.Mount, "/")
	return &Item{spec: spec}
}

func (it *Item) Name() string        { return it.spec.Name }
func (it *Item) Description() string { return it.spec.Description }
func (it *Item) Provider() string    { return "vault" }
func (it *Item) Location() string    { return it.spec.Mount + "/" + strings.TrimPrefix(it.spec.Path, "/") }
func (it *Item) AutoReveal() bool    { return it.spec.AutoReveal }

// TreeView reports whether the values open as a tree. providers.Items has
// already filled an empty view from secrets.view, so empty here means tree.
func (it *Item) TreeView() bool { return it.spec.View != config.SecretViewFlat }

func (it *Item) Details() []secrets.Detail {
	addr := it.spec.Address
	if addr == "" {
		addr = "(VAULT_ADDR from the environment)"
	}
	d := []secrets.Detail{{Label: "Address", Value: addr}}
	if it.spec.Namespace != "" {
		d = append(d, secrets.Detail{Label: "Namespace", Value: it.spec.Namespace})
	}
	path := it.Location()
	switch {
	case it.Browsing():
		path += " (every secret under it)"
	case it.spec.Field != "":
		path += " › " + it.spec.Field
	}
	if it.spec.Version > 0 {
		path += " @ v" + strconv.Itoa(it.spec.Version)
	}
	d = append(d, secrets.Detail{Label: "Path", Value: path})
	if it.spec.Login != "" {
		d = append(d, secrets.Detail{Label: "Login (L)", Value: it.spec.Login})
	}
	return d
}

// Probe checks the CLI and that some token source exists. It never contacts
// the server, so an expired token only shows up when a read fails.
func (it *Item) Probe() secrets.Status {
	if _, err := it.cli(); errors.Is(err, errNotInstalled) {
		return secrets.NotInstalled("vault")
	} else if err != nil {
		return secrets.Status{Reason: err.Error()}
	}
	if !hasTokenSource() {
		reason := "vault login required"
		if it.spec.Login != "" {
			reason += " — L to log in"
		}
		return secrets.Status{Reason: reason}
	}
	return secrets.Status{Ready: true}
}

// hasTokenSource reports whether the CLI has any way to find a token:
// VAULT_TOKEN, the default helper's ~/.vault-token, or a custom token_helper.
func hasTokenSource() bool {
	if os.Getenv("VAULT_TOKEN") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return true // can't tell; let the read decide
	}
	if fi, err := os.Stat(filepath.Join(home, ".vault-token")); err == nil && fi.Size() > 0 {
		return true
	}
	cfg := os.Getenv("VAULT_CONFIG_PATH")
	if cfg == "" {
		cfg = filepath.Join(home, ".vault")
	}
	if b, err := os.ReadFile(cfg); err == nil && strings.Contains(string(b), "token_helper") {
		return true
	}
	return false
}

func (it *Item) cli() (string, error) {
	if it.spec.CLIPath != "" {
		p := platform.ExpandHome(it.spec.CLIPath)
		if _, err := os.Stat(p); err != nil {
			return "", errors.New("cli_path not found")
		}
		return p, nil
	}
	p, err := exec.LookPath("vault")
	if err != nil {
		return "", errNotInstalled
	}
	return p, nil
}

var errNotInstalled = errors.New("vault not installed")

// Tools is what den doctor checks for this item: vault on PATH, unless
// cli_path points elsewhere (Probe checks that path).
func (it *Item) Tools() []string {
	if it.spec.CLIPath != "" {
		return nil
	}
	return []string{"vault"}
}

// LoginCmd runs the configured login through the shell, with the item's
// VAULT_* settings, so `vault login -method=oidc` opens the browser and writes
// the token where every later read finds it.
func (it *Item) LoginCmd() *exec.Cmd {
	if it.spec.Login == "" {
		return nil
	}
	shell, args := platform.ShellCommand(it.spec.Login)
	cmd := exec.Command(shell, args...)
	cmd.Env = it.env()
	return cmd
}

// Read reads the configured secret.
func (it *Item) Read(ctx context.Context) ([]secrets.Field, error) {
	return it.ReadEntry(ctx, it.spec.Path, nil)
}

// Browsing reports whether the path is a folder to pick secrets from.
func (it *Item) Browsing() bool { return strings.HasSuffix(it.spec.Path, "/") }

// ReadEntry reads one secret. pw is unused: Vault auth comes from the token.
func (it *Item) ReadEntry(ctx context.Context, path string, _ []byte) ([]secrets.Field, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	args := []string{"kv", "get", "-format=json", "-mount=" + it.spec.Mount}
	if it.spec.Version > 0 && !it.Browsing() {
		args = append(args, "-version="+strconv.Itoa(it.spec.Version))
	}
	out, err := it.run(ctx, append(args, strings.TrimPrefix(path, "/"))...)
	defer clear(out)
	if err != nil {
		return nil, err
	}
	fields, err := parseSecret(out)
	if err != nil {
		return nil, err
	}
	if f := it.spec.Field; f != "" && !it.Browsing() {
		fields = onlyField(fields, f)
		if len(fields) == 0 {
			return nil, fmt.Errorf("vault: no field %q in %s", f, path)
		}
	}
	return fields, nil
}

// parseSecret pulls the secret's own data out of `vault kv get -format=json`.
// KV v2 nests it as data.data next to data.metadata; KV v1 has it as data.
func parseSecret(out []byte) ([]secrets.Field, error) {
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		// The decoder error can quote the plaintext; keep it out.
		return nil, errors.New("vault: output is not valid JSON")
	}
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, errors.New("vault: output is not valid JSON")
	}
	if inner, meta := resp.Data["data"], resp.Data["metadata"]; inner != nil && meta != nil && isObject(inner) && isObject(meta) {
		data = inner
	}
	// Vault returns keys sorted, and re-encoding the v1 map keeps them sorted.
	fields, err := secrets.FlattenJSON(data)
	if err != nil {
		return nil, errors.New("vault: output is not valid JSON")
	}
	return fields, nil
}

func isObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

// onlyField keeps field and anything nested under it.
func onlyField(fields []secrets.Field, field string) []secrets.Field {
	var out []secrets.Field
	for _, f := range fields {
		if f.Key == field || strings.HasPrefix(f.Key, field+".") || strings.HasPrefix(f.Key, field+"[") {
			out = append(out, f)
		}
	}
	return out
}

// ListEntries lists every secret under the folder path, recursively, as full
// paths in sorted order. A listing cut short at maxEntries or maxDepth comes
// back with both the entries found and an error saying so.
func (it *Item) ListEntries(ctx context.Context, _ []byte) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	root := strings.TrimPrefix(it.spec.Path, "/")
	var entries []string
	truncated := false
	var walk func(folder string, depth int) error
	walk = func(folder string, depth int) error {
		if len(entries) >= maxEntries || depth > maxDepth {
			truncated = true
			return nil
		}
		keys, err := it.list(ctx, folder)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if len(entries) >= maxEntries {
				truncated = true
				return nil
			}
			if strings.HasSuffix(k, "/") {
				if err := walk(folder+k, depth+1); err != nil {
					return err
				}
				continue
			}
			entries = append(entries, folder+k)
		}
		return nil
	}
	if err := walk(root, 1); err != nil {
		return nil, err
	}
	sort.Strings(entries)
	if truncated {
		return entries, fmt.Errorf("vault: stopped listing after %d secrets or %d levels; point path at a smaller folder", maxEntries, maxDepth)
	}
	return entries, nil
}

// list returns one folder's keys; sub-folders end in "/". An empty or missing
// folder is not an error: the CLI prints {} and exits 2 for both.
func (it *Item) list(ctx context.Context, folder string) ([]string, error) {
	out, err := it.run(ctx, "kv", "list", "-format=json", "-mount="+it.spec.Mount, folder)
	if err != nil {
		if strings.TrimSpace(string(out)) == "{}" {
			return nil, nil
		}
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(out, &keys); err != nil {
		return nil, fmt.Errorf("vault: unexpected list output for %s", folder)
	}
	return keys, nil
}

// run runs the vault CLI with the item's environment and returns stdout, even
// on failure (a missing folder still prints {}). The caller clears it.
func (it *Item) run(ctx context.Context, args ...string) ([]byte, error) {
	cli, err := it.cli()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Env = it.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.Bytes(), fmt.Errorf("vault: timed out after %s", readTimeout)
		}
		return stdout.Bytes(), describe(stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

// describe turns the CLI's stderr into one line. A 403 is nearly always a
// missing or expired token, so it says how to fix that.
func describe(stderr string, runErr error) error {
	if strings.Contains(stderr, "Code: 403") || strings.Contains(stderr, "permission denied") ||
		strings.Contains(stderr, "missing client token") {
		return errors.New("vault: permission denied — token expired or no access (L to log in)")
	}
	msg := strings.TrimPrefix(strings.TrimSpace(stderr), "Error making API request.")
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return fmt.Errorf("vault: %w", runErr)
	}
	if r := []rune(msg); len(r) > maxErrLen {
		msg = string(r[:maxErrLen-1]) + "…"
	}
	return errors.New("vault: " + msg)
}

func (it *Item) env() []string {
	env := os.Environ()
	if it.spec.Address != "" {
		env = append(env, "VAULT_ADDR="+it.spec.Address)
	}
	if it.spec.Namespace != "" {
		env = append(env, "VAULT_NAMESPACE="+it.spec.Namespace)
	}
	if it.spec.CACert != "" {
		env = append(env, "VAULT_CACERT="+platform.ExpandHome(it.spec.CACert))
	}
	return env
}
