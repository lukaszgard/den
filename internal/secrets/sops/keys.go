package sops

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// masterKeys is what a sops file records about the keys its data key is
// encrypted to. sops keeps it in plaintext beside the encrypted values, so
// den can tell which key is missing without trying to decrypt.
type masterKeys struct {
	Age []string // recipients: age1… or an ssh-ed25519/ssh-rsa public key
	PGP []string // fingerprints
	KMS []kmsKey
	// Other lists backends den cannot check (gcp_kms, azure_kv, hc_vault).
	Other []string
}

type kmsKey struct{ ARN, Profile string }

func (k masterKeys) empty() bool {
	return len(k.Age) == 0 && len(k.PGP) == 0 && len(k.KMS) == 0 && len(k.Other) == 0
}

// readMasterKeys reads the metadata of the sops file at path, in the format
// sops itself picks from the extension. ok is false when the file cannot be
// parsed (den then leaves the verdict to sops); a file that parses but
// carries no metadata returns ok with found false.
func readMasterKeys(path string) (keys masterKeys, found, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return masterKeys{}, false, false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		var doc map[string]any
		if yaml.Unmarshal(data, &doc) != nil {
			return masterKeys{}, false, false
		}
		return fromTree(doc["sops"])
	case ".env":
		return fromFlat(data, "sops_", "=")
	case ".ini":
		return fromINI(data)
	}
	// .json, and binary files, which sops stores as JSON.
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return masterKeys{}, false, false
	}
	return fromTree(doc["sops"])
}

// fromTree reads the sops section of a YAML or JSON document: the backends
// at its top level and inside key_groups (Shamir secret sharing).
func fromTree(section any) (masterKeys, bool, bool) {
	m, isMap := section.(map[string]any)
	if !isMap {
		return masterKeys{}, false, true
	}
	var keys masterKeys
	groups := []map[string]any{m}
	if kg, isList := m["key_groups"].([]any); isList {
		for _, g := range kg {
			if gm, isMap := g.(map[string]any); isMap {
				groups = append(groups, gm)
			}
		}
	}
	for _, g := range groups {
		for backend, list := range g {
			entries, isList := list.([]any)
			if !isList {
				continue
			}
			for _, e := range entries {
				fields, isMap := e.(map[string]any)
				if !isMap {
					continue
				}
				str := func(k string) string { s, _ := fields[k].(string); return s }
				keys.add(backend, str)
			}
		}
	}
	return keys, true, true
}

func (k *masterKeys) add(backend string, field func(string) string) {
	switch backend {
	case "age":
		if r := field("recipient"); r != "" {
			k.Age = append(k.Age, r)
		}
	case "pgp":
		if fp := field("fp"); fp != "" {
			k.PGP = append(k.PGP, fp)
		}
	case "kms":
		if arn := field("arn"); arn != "" {
			k.KMS = append(k.KMS, kmsKey{ARN: arn, Profile: field("aws_profile")})
		}
	case "gcp_kms", "azure_kv", "hc_vault":
		if !slices.Contains(k.Other, backend) {
			k.Other = append(k.Other, backend)
		}
	}
}

// flatKey is how dotenv and INI files spell the metadata tree:
// age__list_0__map_recipient, or inside key_groups
// key_groups__list_0__map_age__list_0__map_recipient.
var flatKey = regexp.MustCompile(`^(?:key_groups__list_(\d+)__map_)?(age|pgp|kms|gcp_kms|azure_kv|hc_vault)__list_(\d+)__map_(\w+)$`)

// fromFlat reads metadata stored as prefixed key/value lines (dotenv).
func fromFlat(data []byte, prefix, sep string) (masterKeys, bool, bool) {
	entries := map[string]map[string]string{} // group/backend/index → fields
	var order []string
	backends := map[string]string{}
	found := false
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), sep)
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || !strings.HasPrefix(key, prefix) {
			continue
		}
		found = true
		m := flatKey.FindStringSubmatch(strings.TrimPrefix(key, prefix))
		if m == nil {
			continue
		}
		id := m[1] + "/" + m[2] + "/" + m[3]
		if _, seen := entries[id]; !seen {
			entries[id] = map[string]string{}
			order = append(order, id)
			backends[id] = m[2]
		}
		entries[id][m[4]] = value
	}
	var keys masterKeys
	for _, id := range order {
		fields := entries[id]
		keys.add(backends[id], func(k string) string { return fields[k] })
	}
	return keys, found, true
}

// fromINI reads the [sops] section of an INI file, whose keys are the
// flattened ones without a prefix.
func fromINI(data []byte) (masterKeys, bool, bool) {
	var section []string
	in := false
	for line := range strings.SplitSeq(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			in = t == "[sops]"
			continue
		}
		if in {
			section = append(section, t)
		}
	}
	if len(section) == 0 {
		return masterKeys{}, false, true
	}
	keys, _, ok := fromFlat([]byte(strings.Join(section, "\n")), "", "=")
	return keys, true, ok
}

// keyProblem says why no master key of the file is usable on this machine,
// or "" when one is, or when den cannot tell (a key command, a plugin, a
// cloud KMS other than AWS). missing names a CLI that is not installed when
// that is the whole problem.
func (it *Item) keyProblem(k masterKeys) (reason, missing string) {
	if len(k.Other) > 0 || k.empty() {
		return "", ""
	}
	var problems []string
	note := func(p string) {
		if !slices.Contains(problems, p) {
			problems = append(problems, p)
		}
	}

	for _, key := range k.KMS {
		profile := it.spec.AWSProfile
		if profile == "" {
			profile = key.Profile
		}
		p := it.aws.Check(profile, time.Now())
		if p == "" {
			return "", ""
		}
		note(p)
	}

	if len(k.Age) > 0 {
		p, usable := ageProblem(k.Age)
		if usable {
			return "", ""
		}
		note(p)
	}

	if len(k.PGP) > 0 {
		if _, err := exec.LookPath("gpg"); err != nil {
			if len(problems) == 0 {
				return "", "gpg"
			}
			note("gpg not installed")
		} else {
			if slices.ContainsFunc(k.PGP, hasSecretKey) {
				return "", ""
			}
			note("no PGP secret key for " + shortFP(k.PGP[0]))
		}
	}
	return strings.Join(problems, "; "), ""
}

// hasSecretKey asks gpg whether the secret half of key fp is in the keyring;
// gpg exits 0 only then. It may start gpg-agent, hence the bound.
func hasSecretKey(fp string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "gpg", "--batch", "--list-secret-keys", fp).Run() == nil
}

// ageProblem checks the age keys sops would try against the file's
// recipients. usable is also true when den cannot tell: a key command, a
// plugin or a post-quantum identity may match anything.
func ageProblem(recipients []string) (reason string, usable bool) {
	if os.Getenv("SOPS_AGE_KEY_CMD") != "" || os.Getenv("SOPS_AGE_SSH_PRIVATE_KEY_CMD") != "" {
		return "", true
	}
	have := map[string]bool{}
	unknown := false // a plugin or post-quantum identity, which may match anything
	var looked []string
	collect := func(text string) {
		for line := range strings.SplitSeq(text, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "AGE-PLUGIN-"):
				unknown = true
			case strings.HasPrefix(line, "AGE-SECRET-KEY-"):
				if r := ageRecipient(line); r != "" {
					have[r] = true
				} else {
					unknown = true
				}
			}
		}
	}
	if key := os.Getenv("SOPS_AGE_KEY"); key != "" {
		collect(key)
		looked = append(looked, "SOPS_AGE_KEY")
	}
	for _, file := range ageKeyFiles() {
		looked = append(looked, tildePath(file))
		if data, err := os.ReadFile(file); err == nil {
			collect(string(data))
		}
	}
	if unknown {
		return "", true
	}
	for _, r := range recipients {
		if strings.HasPrefix(r, "ssh-") {
			if sshKeyFor(r) {
				return "", true
			}
			continue
		}
		if have[r] {
			return "", true
		}
	}
	return "no age key for " + shortRecipient(recipients[0]) + " (looked in " + strings.Join(looked, ", ") + ")", false
}

// ageKeyFiles are the key files sops reads: SOPS_AGE_KEY_FILE and the
// default keys.txt, which on macOS depends on XDG_CONFIG_HOME.
func ageKeyFiles() []string {
	var files []string
	if f := os.Getenv("SOPS_AGE_KEY_FILE"); f != "" {
		files = append(files, f)
	}
	home, _ := os.UserHomeDir()
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		files = append(files, filepath.Join(xdg, "sops", "age", "keys.txt"))
	}
	switch runtime.GOOS {
	case "darwin":
		files = append(files, filepath.Join(home, "Library", "Application Support", "sops", "age", "keys.txt"))
	case "windows":
		files = append(files, filepath.Join(os.Getenv("AppData"), "sops", "age", "keys.txt"))
	default:
		files = append(files, filepath.Join(home, ".config", "sops", "age", "keys.txt"))
	}
	return slices.Compact(files)
}

// sshKeyFor reports whether a private key sops would use for an SSH
// recipient exists. When its .pub sits beside it, the two must match.
func sshKeyFor(recipient string) bool {
	var candidates []string
	if f := os.Getenv("SOPS_AGE_SSH_PRIVATE_KEY_FILE"); f != "" {
		candidates = append(candidates, f)
	}
	home, _ := os.UserHomeDir()
	candidates = append(candidates, filepath.Join(home, ".ssh", "id_ed25519"), filepath.Join(home, ".ssh", "id_rsa"))
	want := strings.Fields(recipient)
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		pub, err := os.ReadFile(c + ".pub")
		if err != nil {
			return true
		}
		if got := strings.Fields(string(pub)); len(got) >= 2 && len(want) >= 2 && got[0] == want[0] && got[1] == want[1] {
			return true
		}
	}
	return false
}

func shortRecipient(r string) string {
	if strings.HasPrefix(r, "ssh-") {
		if f := strings.Fields(r); len(f) > 2 {
			return f[0] + " key " + f[2]
		}
		return strings.Fields(r)[0] + " key"
	}
	if len(r) > 24 {
		return r[:12] + "…" + r[len(r)-6:]
	}
	return r
}

func shortFP(fp string) string {
	if len(fp) > 16 {
		return fp[len(fp)-16:]
	}
	return fp
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
