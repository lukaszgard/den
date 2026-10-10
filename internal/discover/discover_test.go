package discover

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	ostypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	rstypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/aws-sdk-go-v2/service/redshiftserverless"
	rsstypes "github.com/aws/aws-sdk-go-v2/service/redshiftserverless/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/lukaszgard/den/internal/config"
)

type fakeSSM struct{}

func (fakeSSM) DescribeInstanceInformation(context.Context, *ssm.DescribeInstanceInformationInput, ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return &ssm.DescribeInstanceInformationOutput{InstanceInformationList: []ssmtypes.InstanceInformation{
		{InstanceId: aws.String("i-lost"), ComputerName: aws.String("old"), PingStatus: ssmtypes.PingStatusConnectionLost, ResourceType: ssmtypes.ResourceTypeEc2Instance},
		{InstanceId: aws.String("i-bastion"), ComputerName: aws.String("bastion.internal"), PingStatus: ssmtypes.PingStatusOnline, ResourceType: ssmtypes.ResourceTypeEc2Instance},
		{InstanceId: aws.String("mi-onprem"), PingStatus: ssmtypes.PingStatusOnline, ResourceType: ssmtypes.ResourceTypeManagedInstance},
	}}, nil
}

type fakeRDS struct{}

func (fakeRDS) DescribeDBClusters(context.Context, *rds.DescribeDBClustersInput, ...func(*rds.Options)) (*rds.DescribeDBClustersOutput, error) {
	return &rds.DescribeDBClustersOutput{DBClusters: []rdstypes.DBCluster{
		{DBClusterIdentifier: aws.String("orders"), Engine: aws.String("aurora-postgresql"), Endpoint: aws.String("orders.cluster.rds"), Port: aws.Int32(5432), IAMDatabaseAuthenticationEnabled: aws.Bool(true), DatabaseName: aws.String("orders")},
		{DBClusterIdentifier: aws.String("legacy"), Engine: aws.String("aurora-mysql"), Endpoint: aws.String("legacy.cluster.rds"), Port: aws.Int32(3306), IAMDatabaseAuthenticationEnabled: aws.Bool(false)},
		{DBClusterIdentifier: aws.String("docs"), Engine: aws.String("docdb"), Endpoint: aws.String("docs.cluster.docdb"), Port: aws.Int32(27017)},
		{DBClusterIdentifier: aws.String("graph"), Engine: aws.String("neptune"), Endpoint: aws.String("graph.cluster.neptune"), Port: aws.Int32(8182), IAMDatabaseAuthenticationEnabled: aws.Bool(true)},
	}}, nil
}

func (fakeRDS) DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	return &rds.DescribeDBInstancesOutput{DBInstances: []rdstypes.DBInstance{
		{DBInstanceIdentifier: aws.String("orders-1"), DBClusterIdentifier: aws.String("orders"), Engine: aws.String("aurora-postgresql"), Endpoint: &rdstypes.Endpoint{Address: aws.String("orders-1.rds")}},
		{DBInstanceIdentifier: aws.String("billing"), Engine: aws.String("postgres"), Endpoint: &rdstypes.Endpoint{Address: aws.String("billing.rds"), Port: aws.Int32(5432)}, IAMDatabaseAuthenticationEnabled: aws.Bool(true)},
		{DBInstanceIdentifier: aws.String("erp"), Engine: aws.String("oracle-ee"), Endpoint: &rdstypes.Endpoint{Address: aws.String("erp.rds")}},
	}}, nil
}

type fakeElastiCache struct{}

func (fakeElastiCache) DescribeReplicationGroups(context.Context, *elasticache.DescribeReplicationGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeReplicationGroupsOutput, error) {
	return &elasticache.DescribeReplicationGroupsOutput{ReplicationGroups: []ectypes.ReplicationGroup{
		{ReplicationGroupId: aws.String("sessions"), TransitEncryptionEnabled: aws.Bool(true), NodeGroups: []ectypes.NodeGroup{{PrimaryEndpoint: &ectypes.Endpoint{Address: aws.String("master.sessions.cache"), Port: aws.Int32(6379)}}}},
		{ReplicationGroupId: aws.String("plain"), NodeGroups: []ectypes.NodeGroup{{PrimaryEndpoint: &ectypes.Endpoint{Address: aws.String("master.plain.cache"), Port: aws.Int32(6379)}}}},
	}}, nil
}

func (fakeElastiCache) DescribeServerlessCaches(context.Context, *elasticache.DescribeServerlessCachesInput, ...func(*elasticache.Options)) (*elasticache.DescribeServerlessCachesOutput, error) {
	return &elasticache.DescribeServerlessCachesOutput{}, nil
}

// deniedMemoryDB fails the way an account without MemoryDB permissions does.
type deniedMemoryDB struct{}

func (deniedMemoryDB) DescribeClusters(context.Context, *memorydb.DescribeClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeClustersOutput, error) {
	return nil, errors.New("AccessDeniedException")
}

type fakeRedshift struct{}

func (fakeRedshift) DescribeClusters(context.Context, *redshift.DescribeClustersInput, ...func(*redshift.Options)) (*redshift.DescribeClustersOutput, error) {
	return &redshift.DescribeClustersOutput{Clusters: []rstypes.Cluster{
		{ClusterIdentifier: aws.String("warehouse"), DBName: aws.String("dev"), Endpoint: &rstypes.Endpoint{Address: aws.String("warehouse.redshift"), Port: aws.Int32(5439)}},
	}}, nil
}

type fakeRedshiftServerless struct{}

func (fakeRedshiftServerless) ListWorkgroups(context.Context, *redshiftserverless.ListWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListWorkgroupsOutput, error) {
	return &redshiftserverless.ListWorkgroupsOutput{Workgroups: []rsstypes.Workgroup{
		{WorkgroupName: aws.String("analytics"), Endpoint: &rsstypes.Endpoint{Address: aws.String("analytics.redshift-serverless"), Port: aws.Int32(5439)}},
	}}, nil
}

type fakeOpenSearch struct{}

func (fakeOpenSearch) ListDomainNames(context.Context, *opensearch.ListDomainNamesInput, ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error) {
	return &opensearch.ListDomainNamesOutput{DomainNames: []ostypes.DomainInfo{{DomainName: aws.String("logs")}, {DomainName: aws.String("public")}}}, nil
}

func (fakeOpenSearch) DescribeDomains(context.Context, *opensearch.DescribeDomainsInput, ...func(*opensearch.Options)) (*opensearch.DescribeDomainsOutput, error) {
	return &opensearch.DescribeDomainsOutput{DomainStatusList: []ostypes.DomainStatus{
		{DomainName: aws.String("logs"), Endpoints: map[string]string{"vpc": "vpc-logs.es.amazonaws.com"}},
		{DomainName: aws.String("public"), Endpoint: aws.String("search-public.es.amazonaws.com")},
	}}, nil
}

func fakeClients() Clients {
	return Clients{SSM: fakeSSM{}, RDS: fakeRDS{}, ElastiCache: fakeElastiCache{}, MemoryDB: deniedMemoryDB{},
		Redshift: fakeRedshift{}, RedshiftServerless: fakeRedshiftServerless{}, OpenSearch: fakeOpenSearch{}}
}

func TestRunFindsReachableStores(t *testing.T) {
	inv, warnings := Run(context.Background(), fakeClients())

	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "MemoryDB") {
		t.Errorf("warnings = %v, want only the denied MemoryDB", warnings)
	}
	if online := inv.Online(); len(online) != 1 || online[0].InstanceID != "i-bastion" {
		t.Errorf("online bastions = %+v, want only i-bastion (EC2, online)", online)
	}
	var names []string
	for _, r := range inv.Resources {
		names = append(names, string(r.Type)+":"+r.Name)
	}
	want := "rds:billing rds:legacy rds:orders redis:plain redis:sessions documentdb:docs redshift:analytics redshift:warehouse opensearch:logs neptune:graph"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("resources:\n got %s\nwant %s", got, want)
	}
	if got := inv.Summary(); got != "3 rds, 2 redis, 1 documentdb, 2 redshift, 1 opensearch, 1 neptune" {
		t.Errorf("summary = %q", got)
	}
}

// The generated file is only useful if den loads it: every service must pass
// the same validation a hand-written config does.
func TestGeneratedConfigLoadsAndValidates(t *testing.T) {
	inv, _ := Run(context.Background(), fakeClients())
	out := Generate(inv, GenerateOptions{
		Profile: "dev", Region: "eu-central-1", EnvName: "dev",
		Bastion: inv.Online()[0], Generated: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC),
	})
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load:\n%s\n%v", out, err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("validate:\n%s\n%v", out, err)
	}

	// legacy has IAM auth off: written, but commented out.
	if len(cfg.Services) != 9 {
		t.Fatalf("%d services loaded, want 9 (legacy commented out):\n%s", len(cfg.Services), out)
	}
	if !strings.Contains(out, "# legacy: IAM database authentication is off") {
		t.Errorf("the skipped cluster must say why:\n%s", out)
	}
	ports := map[int]string{}
	for _, s := range cfg.Services {
		target := s.Target()
		if target.EC2InstanceID != "i-bastion" || target.AWSProfile != "dev" || target.AWSRegionCode != "eu-central-1" {
			t.Errorf("%s did not inherit the environment: %+v", s.Name, target)
		}
		if other, dup := ports[target.LocalPort]; dup {
			t.Errorf("%s and %s share local port %d", s.Name, other, target.LocalPort)
		}
		ports[target.LocalPort] = s.Name
	}
	for _, want := range []string{
		`cluster_identifier: "warehouse"`, `workgroup_name: "analytics"`,
		`opensearch_host: "vpc-logs.es.amazonaws.com"`, `neptune_host: "graph.cluster.neptune"`,
		"note: in-transit encryption is off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated config lacks %q", want)
		}
	}
	if strings.Contains(out, "search-public") {
		t.Error("a public OpenSearch domain needs no tunnel and must not be listed")
	}
}

func TestGenerateWithNothingFound(t *testing.T) {
	out := Generate(Inventory{}, GenerateOptions{Profile: "p", Region: "r", EnvName: "e", Bastion: Bastion{InstanceID: "i-1"}})
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || config.Validate(cfg) != nil || len(cfg.Services) != 0 {
		t.Fatalf("empty inventory should still load: %v\n%s", err, out)
	}
}
