package sops

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
)

// ageKey is a fresh age identity: the secret key line and its recipient.
type ageKey struct{ secret, recipient string }

func newAgeKey(t *testing.T) ageKey {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := bech32Encode("age-secret-key-", priv.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return ageKey{secret: strings.ToUpper(secret), recipient: ageRecipient(strings.ToUpper(secret))}
}

// A throwaway key made by age-keygen, kept as its raw scalar so secret
// scanners do not flag it, and the recipient age-keygen printed for it.
const (
	ageKeygenScalar    = "e8512f6a788554a59a1503076b202dc4d4a73fd3a32f2ba52f65ab07bf98242f"
	ageKeygenRecipient = "age1dy0tdp5dj0wpdlvfy4xsnu7ucwqa2j769sn4md7k5zyq5mcj7c8s2f63w8"
)

// den must derive the recipient age-keygen printed.
func TestAgeRecipientMatchesAgeKeygen(t *testing.T) {
	secret, err := bech32Encode("age-secret-key-", mustHex(t, ageKeygenScalar))
	if err != nil {
		t.Fatal(err)
	}
	if got := ageRecipient(strings.ToUpper(secret)); got != ageKeygenRecipient {
		t.Errorf("recipient = %s, want %s", got, ageKeygenRecipient)
	}
	for _, bad := range []string{"", "AGE-SECRET-KEY-1", "AGE-SECRET-KEY-1qqqq", "age1dy0tdp5dj0wpdlvfy4xsnu7ucwqa2j769sn4md7k5zyq5mcj7c8s2f63w8", strings.ToUpper(secret[:len(secret)-1]) + "Q"} {
		if got := ageRecipient(bad); got != "" {
			t.Errorf("ageRecipient(%q) = %q, want nothing", bad, got)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The metadata layouts below are what `sops --encrypt` writes (checked
// against sops 3.9 output); only the values are shortened. {R} is the age
// recipient under test.
const (
	yamlDoc = `db_password: ENC[AES256_GCM,data:x,type:str]
sops:
    kms:
        - arn: arn:aws:kms:eu-central-1:111122223333:key/abc
          aws_profile: kms-owner
    gcp_kms: []
    azure_kv: []
    hc_vault: []
    age:
        - recipient: {R}
          enc: |
            -----BEGIN AGE ENCRYPTED FILE-----
            -----END AGE ENCRYPTED FILE-----
    pgp:
        - fp: 85D77543B3D624B63CEA9E6DBC17301B491B3F21
    version: 3.9.0
`
	jsonDoc = `{"db_password":"ENC[AES256_GCM,data:x,type:str]","sops":{"kms":null,"gcp_kms":null,
"azure_kv":null,"hc_vault":null,"age":[{"recipient":"{R}","enc":"-----BEGIN AGE ENCRYPTED FILE-----\n"}],
"pgp":null,"version":"3.9.0"}}`
	envDoc = `DB_PASSWORD=ENC[AES256_GCM,data:x,type:str]
sops_age__list_0__map_enc=-----BEGIN AGE ENCRYPTED FILE-----\nYWdl\n-----END AGE ENCRYPTED FILE-----\n
sops_age__list_0__map_recipient={R}
sops_lastmodified=2026-09-26T18:54:32Z
sops_version=3.9.0
`
	iniDoc = `[db]
password = ENC[AES256_GCM,data:x,type:str]

[sops]
lastmodified               = 2026-09-26T18:54:32Z
age__list_0__map_recipient = {R}
age__list_0__map_enc       = -----BEGIN AGE ENCRYPTED FILE-----\nYWdl\n
version                    = 3.9.0
`
	keyGroupsDoc = `a: ENC[x]
sops:
    key_groups:
        - age:
            - recipient: {R}
        - pgp:
            - fp: 85D77543B3D624B63CEA9E6DBC17301B491B3F21
    shamir_threshold: 2
`
)

func writeDoc(t *testing.T, name, doc, recipient string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(doc, "{R}", recipient)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadMasterKeys(t *testing.T) {
	r := "age1dy0tdp5dj0wpdlvfy4xsnu7ucwqa2j769sn4md7k5zyq5mcj7c8s2f63w8"
	yamlKeys, found, ok := readMasterKeys(writeDoc(t, "app.enc.yaml", yamlDoc, r))
	want := masterKeys{
		Age: []string{r},
		PGP: []string{"85D77543B3D624B63CEA9E6DBC17301B491B3F21"},
		KMS: []kmsKey{{ARN: "arn:aws:kms:eu-central-1:111122223333:key/abc", Profile: "kms-owner"}},
	}
	if !ok || !found || !reflect.DeepEqual(yamlKeys, want) {
		t.Errorf("yaml = %+v (found %v ok %v)", yamlKeys, found, ok)
	}
	for name, doc := range map[string]string{"app.json": jsonDoc, "app.env": envDoc, "app.ini": iniDoc} {
		keys, found, ok := readMasterKeys(writeDoc(t, name, doc, r))
		if !ok || !found || !reflect.DeepEqual(keys.Age, []string{r}) || len(keys.KMS)+len(keys.PGP) != 0 {
			t.Errorf("%s = %+v (found %v ok %v)", name, keys, found, ok)
		}
	}
	groups, _, _ := readMasterKeys(writeDoc(t, "groups.yaml", keyGroupsDoc, r))
	if !reflect.DeepEqual(groups.Age, []string{r}) || len(groups.PGP) != 1 {
		t.Errorf("key_groups = %+v", groups)
	}

	if _, found, ok := readMasterKeys(writeDoc(t, "plain.yaml", "db_password: hunter2\n", "")); !ok || found {
		t.Errorf("a plain YAML file parses but has no metadata: found %v ok %v", found, ok)
	}
	if _, _, ok := readMasterKeys(writeDoc(t, "garbage.yaml", "- [unclosed", "")); ok {
		t.Error("an unparsable file must leave the verdict to sops")
	}
}

// ageHome isolates every place sops looks for age keys and returns a fresh
// identity.
func ageHome(t *testing.T) ageKey {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	for _, k := range []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_FILE", "SOPS_AGE_SSH_PRIVATE_KEY_CMD"} {
		t.Setenv(k, "")
	}
	fakeSOPS(t, "exit 0")
	return newAgeKey(t)
}

func TestProbeAgeKeys(t *testing.T) {
	id := ageHome(t)
	other := newAgeKey(t)
	mine := New(config.SOPSSecretSpec{Name: "mine", File: writeDoc(t, "mine.enc.json", jsonDoc, id.recipient)}, nil)
	theirs := New(config.SOPSSecretSpec{Name: "theirs", File: writeDoc(t, "theirs.enc.json", jsonDoc, other.recipient)}, nil)

	st := mine.Probe()
	if st.Ready || !strings.HasPrefix(st.Reason, "no age key for age1") || !strings.Contains(st.Reason, "keys.txt") {
		t.Fatalf("no key anywhere: %+v", st)
	}

	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("# created: now\n"+id.secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOPS_AGE_KEY_FILE", keyFile)
	if st := mine.Probe(); !st.Ready {
		t.Errorf("SOPS_AGE_KEY_FILE holds the key: %+v", st)
	}
	if st := theirs.Probe(); st.Ready || !strings.Contains(st.Reason, "no age key") {
		t.Errorf("a key for someone else's recipient must not count: %+v", st)
	}

	t.Setenv("SOPS_AGE_KEY_FILE", "")
	t.Setenv("SOPS_AGE_KEY", other.secret)
	if st := theirs.Probe(); !st.Ready {
		t.Errorf("SOPS_AGE_KEY holds the key: %+v", st)
	}

	// den cannot see what a key command or a plugin would produce.
	t.Setenv("SOPS_AGE_KEY", "")
	t.Setenv("SOPS_AGE_KEY_CMD", "op read op://vault/age/key")
	if st := mine.Probe(); !st.Ready {
		t.Errorf("a key command may produce the key: %+v", st)
	}
	t.Setenv("SOPS_AGE_KEY_CMD", "")
	t.Setenv("SOPS_AGE_KEY", "AGE-PLUGIN-YUBIKEY-1QQQQQQQQQQQQQQQQQQ")
	if st := mine.Probe(); !st.Ready {
		t.Errorf("a plugin identity may match: %+v", st)
	}
	t.Setenv("SOPS_AGE_KEY", "AGE-SECRET-KEY-PQ-1QQQQQQQQQQQQQQQQQQ")
	if st := mine.Probe(); !st.Ready {
		t.Errorf("a post-quantum identity may match: %+v", st)
	}
}

func TestProbeSSHRecipient(t *testing.T) {
	ageHome(t)
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMine me@laptop"
	it := New(config.SOPSSecretSpec{Name: "ssh", File: writeDoc(t, "ssh.enc.yaml", yamlDoc, pub)}, nil)
	// yamlDoc also lists a KMS key and a PGP key; drop them for this test.
	if err := os.WriteFile(it.path, []byte("a: ENC[x]\nsops:\n    age:\n        - recipient: "+pub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := it.Probe(); st.Ready || !strings.Contains(st.Reason, "ssh-ed25519 key me@laptop") {
		t.Fatalf("no ssh key: %+v", st)
	}
	ssh := filepath.Join(os.Getenv("HOME"), ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(ssh, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("id_ed25519", "private")
	write("id_ed25519.pub", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOther other@laptop\n")
	if st := it.Probe(); st.Ready {
		t.Errorf("a different ssh key must not count: %+v", st)
	}
	write("id_ed25519.pub", pub+"\n")
	if st := it.Probe(); !st.Ready {
		t.Errorf("the matching ssh key: %+v", st)
	}
}

// fakeGPG puts a gpg on PATH that knows the secret key fp only.
func fakeGPG(t *testing.T, fp string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = \"" + fp + "\" ] && exit 0; done\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "gpg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProbePGP(t *testing.T) {
	ageHome(t)
	fp := "85D77543B3D624B63CEA9E6DBC17301B491B3F21"
	doc := "a: ENC[x]\nsops:\n    pgp:\n        - fp: " + fp + "\n"
	it := New(config.SOPSSecretSpec{Name: "pgp", File: writeDoc(t, "pgp.enc.yaml", doc, "")}, nil)

	// Only sops is on PATH: gpg is what is missing, and doctor must check it.
	t.Setenv("PATH", filepath.Dir(mustLookPath(t, "sops")))
	if st := it.Probe(); st.Missing != "gpg" || !strings.HasPrefix(st.Reason, "gpg not installed") {
		t.Errorf("no gpg: %+v", st)
	}
	if got := it.Tools(); !reflect.DeepEqual(got, []string{"sops", "gpg"}) {
		t.Errorf("tools = %v, want sops and gpg", got)
	}

	fakeGPG(t, "0000000000000000000000000000000000000000")
	if st := it.Probe(); st.Ready || st.Reason != "no PGP secret key for BC17301B491B3F21" {
		t.Errorf("someone else's key: %+v", st)
	}
	fakeGPG(t, fp)
	if st := it.Probe(); !st.Ready {
		t.Errorf("the secret key is in the keyring: %+v", st)
	}
}

func TestProbeKMSUsesTheProfile(t *testing.T) {
	ageHome(t)
	awsConfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(awsConfig, []byte("[profile kms-owner]\nregion = eu-central-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := "a: ENC[x]\nsops:\n    kms:\n        - arn: arn:aws:kms:eu-central-1:111122223333:key/abc\n          aws_profile: kms-owner\n"
	file := writeDoc(t, "kms.enc.yaml", doc, "")
	if st := New(config.SOPSSecretSpec{Name: "k", File: file}, secrets.NewAWSLogins(awsConfig)).Probe(); !st.Ready {
		t.Errorf("the key's own aws_profile exists: %+v", st)
	}
	if st := New(config.SOPSSecretSpec{Name: "k", File: file, AWSProfile: "typo"}, secrets.NewAWSLogins(awsConfig)).Probe(); st.Ready || st.Reason != "unknown AWS profile typo" {
		t.Errorf("aws_profile in den.yaml wins, and it does not exist: %+v", st)
	}
	if got := New(config.SOPSSecretSpec{Name: "k", File: file}, secrets.NewAWSLogins(awsConfig)).Tools(); !reflect.DeepEqual(got, []string{"sops"}) {
		t.Errorf("a KMS file needs no gpg: %v", got)
	}
}

const kmsDoc = "a: ENC[x]\nsops:\n    kms:\n        - arn: arn:aws:kms:eu-central-1:111122223333:key/abc\n"

// Probing again after an AWS login must not parse the file again: the login
// changed, the file did not.
func TestKeysAreReadOncePerStateOfTheFile(t *testing.T) {
	id := ageHome(t)
	it := New(config.SOPSSecretSpec{Name: "k", File: writeDoc(t, "kms.enc.yaml", kmsDoc, "")}, nil)
	for range 5 {
		if st := it.Probe(); !st.Ready {
			t.Fatalf("Probe = %+v", st)
		}
	}
	it.Tools()
	if !it.UsesAWS() {
		t.Error("a file with a KMS key is read through AWS")
	}
	if it.keyReads != 1 {
		t.Fatalf("file parsed %d times, want once", it.keyReads)
	}

	// Re-encrypted to an age key only: the new keys must be seen.
	if err := os.WriteFile(it.path, []byte(strings.ReplaceAll(jsonDoc, "{R}", id.recipient)), 0o600); err != nil {
		t.Fatal(err)
	}
	if it.UsesAWS() {
		t.Error("an age-only file does not depend on an AWS login")
	}
	if it.keyReads != 2 {
		t.Errorf("file parsed %d times, want once more after it changed", it.keyReads)
	}
}

func TestUsesAWS(t *testing.T) {
	ageHome(t)
	garbage := writeDoc(t, "garbage.yaml", "- [unclosed", "")
	for _, tc := range []struct {
		name string
		spec config.SOPSSecretSpec
		want bool
	}{
		{"kms key", config.SOPSSecretSpec{File: writeDoc(t, "kms.enc.yaml", kmsDoc, "")}, true},
		{"age and pgp only, profile set anyway", config.SOPSSecretSpec{File: writeDoc(t, "groups.yaml", keyGroupsDoc, "age1x"), AWSProfile: "p"}, false},
		{"unparsable, profile checked", config.SOPSSecretSpec{File: garbage, AWSProfile: "p"}, true},
		{"unparsable, no profile", config.SOPSSecretSpec{File: garbage}, false},
		{"missing file, profile checked", config.SOPSSecretSpec{File: filepath.Join(t.TempDir(), "none.yaml"), AWSProfile: "p"}, true},
	} {
		if got := New(tc.spec, nil).UsesAWS(); got != tc.want {
			t.Errorf("%s: UsesAWS = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProbeNotASopsFile(t *testing.T) {
	ageHome(t)
	it := New(config.SOPSSecretSpec{Name: "plain", File: writeDoc(t, "plain.yaml", "db_password: hunter2\n", "")}, nil)
	if st := it.Probe(); st.Ready || !strings.Contains(st.Reason, "not encrypted with sops") {
		t.Errorf("plain YAML: %+v", st)
	}
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return filepath.Join(dir, name)
		}
	}
	t.Fatalf("%s is not on PATH", name)
	return ""
}
