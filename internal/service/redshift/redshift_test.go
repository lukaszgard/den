package redshift

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	rs "github.com/aws/aws-sdk-go-v2/service/redshift"
	rss "github.com/aws/aws-sdk-go-v2/service/redshiftserverless"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/pgpass"
)

var expiry = time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC)

// fakeAPI records which call was made and with what input.
type fakeAPI struct {
	called  string
	withIAM *rs.GetClusterCredentialsWithIAMInput
	byUser  *rs.GetClusterCredentialsInput
	sls     *rss.GetCredentialsInput
}

func (f *fakeAPI) GetClusterCredentialsWithIAM(_ context.Context, in *rs.GetClusterCredentialsWithIAMInput, _ ...func(*rs.Options)) (*rs.GetClusterCredentialsWithIAMOutput, error) {
	f.called, f.withIAM = "GetClusterCredentialsWithIAM", in
	return &rs.GetClusterCredentialsWithIAMOutput{DbUser: aws.String("IAMR:dev-role"), DbPassword: aws.String("pw"), Expiration: &expiry}, nil
}

func (f *fakeAPI) GetClusterCredentials(_ context.Context, in *rs.GetClusterCredentialsInput, _ ...func(*rs.Options)) (*rs.GetClusterCredentialsOutput, error) {
	f.called, f.byUser = "GetClusterCredentials", in
	return &rs.GetClusterCredentialsOutput{DbUser: aws.String("IAM:" + aws.ToString(in.DbUser)), DbPassword: aws.String("pw"), Expiration: &expiry}, nil
}

func (f *fakeAPI) GetCredentials(_ context.Context, in *rss.GetCredentialsInput, _ ...func(*rss.Options)) (*rss.GetCredentialsOutput, error) {
	f.called, f.sls = "GetCredentials", in
	return &rss.GetCredentialsOutput{DbUser: aws.String("IAMR:dev-role"), DbPassword: aws.String("pw"), Expiration: &expiry}, nil
}

func TestFetchCredentialsPicksAPI(t *testing.T) {
	cases := []struct {
		name     string
		spec     config.RedshiftSpec
		wantCall string
		wantUser string
	}{
		{"provisioned, IAM identity", config.RedshiftSpec{ClusterIdentifier: "c1", DBName: "dev"}, "GetClusterCredentialsWithIAM", "IAMR:dev-role"},
		{"provisioned, named user", config.RedshiftSpec{ClusterIdentifier: "c1", DBName: "dev", DBUser: "analyst"}, "GetClusterCredentials", "IAM:analyst"},
		{"serverless", config.RedshiftSpec{WorkgroupName: "wg", DBName: "dev"}, "GetCredentials", "IAMR:dev-role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &fakeAPI{}
			got, err := fetchCredentials(context.Background(), &c.spec, 30*time.Minute, api, api)
			if err != nil {
				t.Fatal(err)
			}
			if api.called != c.wantCall {
				t.Fatalf("called %s, want %s", api.called, c.wantCall)
			}
			if got.User != c.wantUser || got.Password != "pw" || !got.Expires.Equal(expiry) {
				t.Fatalf("creds = %+v", got)
			}
		})
	}
}

func TestFetchCredentialsInputs(t *testing.T) {
	api := &fakeAPI{}
	spec := config.RedshiftSpec{ClusterIdentifier: "c1", DBName: "sales", DBUser: "analyst"}
	if _, err := fetchCredentials(context.Background(), &spec, 30*time.Minute, api, api); err != nil {
		t.Fatal(err)
	}
	in := api.byUser
	if aws.ToString(in.ClusterIdentifier) != "c1" || aws.ToString(in.DbName) != "sales" || aws.ToInt32(in.DurationSeconds) != 1800 {
		t.Errorf("input = cluster %q db %q duration %d", aws.ToString(in.ClusterIdentifier), aws.ToString(in.DbName), aws.ToInt32(in.DurationSeconds))
	}
	if in.AutoCreate == nil || *in.AutoCreate {
		t.Error("AutoCreate must be explicitly false — den must not create database users")
	}

	spec = config.RedshiftSpec{WorkgroupName: "wg", DBName: "dev"}
	if _, err := fetchCredentials(context.Background(), &spec, 15*time.Minute, api, api); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(api.sls.WorkgroupName) != "wg" || aws.ToInt32(api.sls.DurationSeconds) != 900 {
		t.Errorf("serverless input = workgroup %q duration %d", aws.ToString(api.sls.WorkgroupName), aws.ToInt32(api.sls.DurationSeconds))
	}
}

func TestCommandsUseReturnedUser(t *testing.T) {
	e := &engine{spec: &config.RedshiftSpec{LocalPort: 55431, DBName: "dev", DBUser: "analyst"}}
	if got, want := e.ConnectCommand(), `psql "host=localhost hostaddr=127.0.0.1 port=55431 dbname=dev user=IAM:analyst sslmode=require"`; got != want {
		t.Errorf("before minting:\n%s\nwant\n%s", got, want)
	}

	e.user = "IAMR:dev-role"
	if got, want := e.ConnectCommandWithSecret("pw"), `psql "host=localhost hostaddr=127.0.0.1 port=55431 dbname=dev user=IAMR:dev-role password=pw sslmode=require"`; got != want {
		t.Errorf("with secret:\n%s\nwant\n%s", got, want)
	}
	// The returned user carries a colon, which pgpass must escape.
	if got, want := pgpass.Line(55431, "dev", e.dbUser(), "pw"), `localhost:55431:dev:IAMR\:dev-role:pw`; got != want {
		t.Errorf("pgpass line = %q, want %q", got, want)
	}
}

// The terminal command is run by den, so the password must not sit where other
// users of the machine can read it: the argument list.
func TestTerminalCommandKeepsThePasswordOutOfTheArguments(t *testing.T) {
	e := &engine{spec: &config.RedshiftSpec{LocalPort: 55431, DBName: "dev", DBUser: "analyst"}}
	e.user = "IAMR:dev-role"

	got := e.TerminalCommand("pw-1")
	want := service.ClientCommand{
		Tool: "psql",
		Args: []string{"host=localhost hostaddr=127.0.0.1 port=55431 dbname=dev user=IAMR:dev-role sslmode=require"},
		Env:  []string{"PGPASSWORD=pw-1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
	// The command den shows and the one it runs must reach the same database.
	if shown := e.ConnectCommand(); shown != `psql "`+got.Args[0]+`"` {
		t.Fatalf("the shown command %s does not match the run one %q", shown, got.Args)
	}
}

// The check logs in as a tab would, plus what makes psql answer once and exit;
// options first, as BSD getopt stops at the first argument that is not one.
func TestLoginProbe(t *testing.T) {
	e := &engine{spec: &config.RedshiftSpec{LocalPort: 55431, DBName: "dev", ClusterIdentifier: "c1"}}
	e.user = "IAMR:dev-role"

	p := e.LoginProbe("pw-1")
	tab := e.TerminalCommand("pw-1")
	want := service.ClientCommand{
		Tool: "psql",
		Args: []string{"-X", "-w", "-A", "-t", "-c", "select 1", tab.Args[0]},
		Env:  tab.Env,
	}
	if !reflect.DeepEqual(p.Command, want) {
		t.Fatalf("probe =\n%+v\nwant\n%+v", p.Command, want)
	}
	if p.User != "IAMR:dev-role" || p.Succeeded != nil {
		t.Errorf("User = %q, Succeeded set: %v", p.User, p.Succeeded != nil)
	}
}

func TestLoginHint(t *testing.T) {
	e := &engine{
		spec: &config.RedshiftSpec{LocalPort: 55431, DBName: "sales", ClusterIdentifier: "c1", DBUser: "analyst"},
		env:  config.EnvMapping{CredentialProfile: "acme-int"},
		ttl:  15 * time.Minute,
	}
	e.user = "IAM:analyst"
	hint := e.LoginProbe("pw").Hint
	for _, want := range []string{`AWS profile "acme-int"`, `"IAM:analyst"`, `cluster "c1"`, `database "sales"`, `"analyst" exists`, "aws sso login --profile acme-int"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint has no %q:\n%s", want, hint)
		}
	}
	sls := &engine{spec: &config.RedshiftSpec{LocalPort: 55431, DBName: "dev", WorkgroupName: "wg"}}
	if hint := sls.LoginProbe("pw").Hint; !strings.Contains(hint, `workgroup "wg"`) || strings.Contains(hint, "exists in") {
		t.Errorf("serverless hint:\n%s", hint)
	}
}

// The hint is read in the detail view's log, which shows 104 runes of a line
// at 120 columns and cuts the rest.
func TestLoginHintLinesFitTheLog(t *testing.T) {
	e := &engine{
		spec: &config.RedshiftSpec{LocalPort: 55431, DBName: "analytics", WorkgroupName: "cn-e2e-analytics"},
		env:  config.EnvMapping{CredentialProfile: "acme-cn-e2e-analytics-reader"},
	}
	e.user = "IAMR:AWSReservedSSO_AnalyticsReader_0123456789abcdef"
	for line := range strings.Lines(e.LoginProbe("pw").Hint) {
		if n := len([]rune(strings.TrimRight(line, "\n"))); n > 104 {
			t.Errorf("a line of %d runes:\n%s", n, line)
		}
	}
}
