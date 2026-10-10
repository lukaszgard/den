package awssm

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

type fakeClient struct {
	out *secretsmanager.GetSecretValueOutput
	err error
	in  *secretsmanager.GetSecretValueInput
}

func (f *fakeClient) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.in = in
	return f.out, f.err
}

func item(spec config.AWSSecretsManagerSecretSpec, c *fakeClient) *Item {
	it := New(spec, nil)
	it.newClient = func(context.Context) (getter, error) { return c, nil }
	return it
}

func field(k, v string) secrets.Field { return secrets.Field{Key: k, Value: v} }

// keyValues drops each field's Path: what is read is checked here, the path
// once, in package secrets.
func keyValues(fields []secrets.Field) []secrets.Field {
	out := make([]secrets.Field, len(fields))
	for i, f := range fields {
		out[i] = field(f.Key, f.Value)
	}
	return out
}

func TestReadFlattensJSONSecret(t *testing.T) {
	c := &fakeClient{out: &secretsmanager.GetSecretValueOutput{
		SecretString: aws.String(`{"client_id":"m2m","private_key":"-----BEGIN KEY-----\nabc","ttl":3600}`),
	}}
	fields, err := item(config.AWSSecretsManagerSecretSpec{Name: "m2m", SecretID: "/acme/m2m"}, c).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []secrets.Field{
		field("client_id", "m2m"),
		field("private_key", "-----BEGIN KEY-----\nabc"),
		field("ttl", "3600"),
	}
	if !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
	if got := aws.ToString(c.in.SecretId); got != "/acme/m2m" {
		t.Errorf("SecretId = %q", got)
	}
	if c.in.VersionStage != nil {
		t.Errorf("VersionStage = %q, want unset so AWS uses AWSCURRENT", *c.in.VersionStage)
	}
}

func TestReadPlainStringAndVersionStage(t *testing.T) {
	c := &fakeClient{out: &secretsmanager.GetSecretValueOutput{SecretString: aws.String("{not json but a password")}}
	spec := config.AWSSecretsManagerSecretSpec{Name: "pw", SecretID: "db", VersionStage: "AWSPREVIOUS"}
	fields, err := item(spec, c).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []secrets.Field{field("value", "{not json but a password")}; !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v, want the string as one row", fields)
	}
	if got := aws.ToString(c.in.VersionStage); got != "AWSPREVIOUS" {
		t.Errorf("VersionStage = %q", got)
	}
}

func TestReadBinarySecret(t *testing.T) {
	c := &fakeClient{out: &secretsmanager.GetSecretValueOutput{SecretBinary: []byte{0xde, 0xad}}}
	fields, err := item(config.AWSSecretsManagerSecretSpec{Name: "b", SecretID: "b"}, c).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []secrets.Field{field("value (base64)", "3q0=")}; !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v", fields)
	}
}

func TestReadReportsAPIErrorConcisely(t *testing.T) {
	apiErr := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform secretsmanager:GetSecretValue"}
	wrapped := errors.Join(errors.New("operation error Secrets Manager: GetSecretValue, https response error StatusCode: 400, RequestID: 123"), apiErr)
	c := &fakeClient{err: wrapped}
	_, err := item(config.AWSSecretsManagerSecretSpec{Name: "x", SecretID: "x"}, c).Read(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "secrets manager: AccessDeniedException: not authorized to perform secretsmanager:GetSecretValue"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestReadReportsClientSetupError(t *testing.T) {
	it := New(config.AWSSecretsManagerSecretSpec{Name: "x", SecretID: "x"}, nil)
	it.newClient = func(context.Context) (getter, error) {
		return nil, errors.New("load aws config: failed to get shared config profile, typo")
	}
	if _, err := it.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "shared config profile, typo") {
		t.Errorf("err = %v", err)
	}
}

func TestProbeChecksProfile(t *testing.T) {
	// No AWS config to check against: nothing to report.
	if st := New(config.AWSSecretsManagerSecretSpec{Name: "x", SecretID: "x", AWSProfile: "p"}, nil).Probe(); !st.Ready {
		t.Errorf("Probe = %+v", st)
	}
	missing := t.TempDir() + "/config"
	if err := writeFile(missing, "[profile other]\nregion = eu-central-1\n"); err != nil {
		t.Fatal(err)
	}
	st := New(config.AWSSecretsManagerSecretSpec{Name: "x", SecretID: "x", AWSProfile: "typo"}, secrets.NewAWSLogins(missing)).Probe()
	if st.Ready || st.Reason != "unknown AWS profile typo" {
		t.Errorf("Probe = %+v", st)
	}
}

func TestUsesAWS(t *testing.T) {
	var it secrets.Item = New(config.AWSSecretsManagerSecretSpec{Name: "x", SecretID: "x"}, nil)
	if u, ok := it.(secrets.AWSUser); !ok || !u.UsesAWS() {
		t.Error("a Secrets Manager secret is always read through an AWS login")
	}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
