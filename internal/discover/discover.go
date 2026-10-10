// Package discover lists what den could connect to in one AWS account and
// region — SSM-managed instances to use as the bastion, and the private data
// stores behind them — and writes a starter den.yaml from it.
//
// It only reads (Describe*/List* calls). The generated file is a static
// config like any hand-written one: den never discovers at run time.
package discover

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/redshiftserverless"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/lukaszgard/den/internal/config"
)

// The AWS calls discovery makes, narrowed to interfaces so tests can fake them.
type (
	SSMAPI interface {
		DescribeInstanceInformation(context.Context, *ssm.DescribeInstanceInformationInput, ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
	}
	RDSAPI interface {
		DescribeDBClusters(context.Context, *rds.DescribeDBClustersInput, ...func(*rds.Options)) (*rds.DescribeDBClustersOutput, error)
		DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
	}
	ElastiCacheAPI interface {
		DescribeReplicationGroups(context.Context, *elasticache.DescribeReplicationGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeReplicationGroupsOutput, error)
		DescribeServerlessCaches(context.Context, *elasticache.DescribeServerlessCachesInput, ...func(*elasticache.Options)) (*elasticache.DescribeServerlessCachesOutput, error)
	}
	MemoryDBAPI interface {
		DescribeClusters(context.Context, *memorydb.DescribeClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeClustersOutput, error)
	}
	RedshiftAPI interface {
		DescribeClusters(context.Context, *redshift.DescribeClustersInput, ...func(*redshift.Options)) (*redshift.DescribeClustersOutput, error)
	}
	RedshiftServerlessAPI interface {
		ListWorkgroups(context.Context, *redshiftserverless.ListWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListWorkgroupsOutput, error)
	}
	OpenSearchAPI interface {
		ListDomainNames(context.Context, *opensearch.ListDomainNamesInput, ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error)
		DescribeDomains(context.Context, *opensearch.DescribeDomainsInput, ...func(*opensearch.Options)) (*opensearch.DescribeDomainsOutput, error)
	}
)

// Clients are the AWS APIs discovery reads.
type Clients struct {
	SSM                SSMAPI
	RDS                RDSAPI
	ElastiCache        ElastiCacheAPI
	MemoryDB           MemoryDBAPI
	Redshift           RedshiftAPI
	RedshiftServerless RedshiftServerlessAPI
	OpenSearch         OpenSearchAPI
}

// NewClients builds every client from one AWS config.
func NewClients(cfg aws.Config) Clients {
	return Clients{
		SSM:                ssm.NewFromConfig(cfg),
		RDS:                rds.NewFromConfig(cfg),
		ElastiCache:        elasticache.NewFromConfig(cfg),
		MemoryDB:           memorydb.NewFromConfig(cfg),
		Redshift:           redshift.NewFromConfig(cfg),
		RedshiftServerless: redshiftserverless.NewFromConfig(cfg),
		OpenSearch:         opensearch.NewFromConfig(cfg),
	}
}

// Bastion is an SSM-managed instance that could forward to the data stores.
type Bastion struct {
	InstanceID string
	Name       string // the host name SSM reports
	Online     bool
}

// Resource is one data store den can reach through a bastion.
type Resource struct {
	Type    config.ServiceType
	Name    string // identifier in AWS; also the service name
	Host    string
	Port    int
	IAMAuth bool     // false means den cannot mint a credential for it yet
	Notes   []string // caveats written next to the service

	Engine            string // rds: postgres | mysql
	DBName            string
	CacheName         string // redis / memorydb
	Serverless        bool   // elasticache serverless, opensearch serverless
	MemoryDB          bool
	ClusterIdentifier string // redshift provisioned
	WorkgroupName     string // redshift serverless
}

// Inventory is everything found.
type Inventory struct {
	Bastions  []Bastion
	Resources []Resource
}

// SourceError is a discovery source that could not be read.
type SourceError struct {
	Source string // e.g. "MemoryDB"
	Err    error
}

func (e *SourceError) Error() string { return e.Source + ": " + e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }

// Run lists bastions and data stores. A data-store source that fails (no
// permission, the service unused in the region) is reported in warnings and
// skipped, so one denied API does not hide everything else. Listing the SSM
// instances comes first: when that fails — typically an expired login — there
// is no bastion to generate for, and Run stops.
func Run(ctx context.Context, c Clients) (Inventory, []error) {
	var inv Inventory
	var warnings []error
	step := func(what string, f func() error) {
		if err := f(); err != nil {
			warnings = append(warnings, &SourceError{Source: what, Err: err})
		}
	}
	step("SSM instances", func() (err error) { inv.Bastions, err = bastions(ctx, c.SSM); return })
	if len(warnings) > 0 {
		return inv, warnings
	}
	step("RDS", func() error { return rdsResources(ctx, c.RDS, &inv) })
	step("ElastiCache", func() error { return elastiCacheResources(ctx, c.ElastiCache, &inv) })
	step("MemoryDB", func() error { return memoryDBResources(ctx, c.MemoryDB, &inv) })
	step("Redshift", func() error { return redshiftResources(ctx, c.Redshift, c.RedshiftServerless, &inv) })
	step("OpenSearch", func() error { return openSearchResources(ctx, c.OpenSearch, &inv) })
	sort.SliceStable(inv.Resources, func(i, j int) bool {
		a, b := inv.Resources[i], inv.Resources[j]
		if a.Type != b.Type {
			return typeOrder[a.Type] < typeOrder[b.Type]
		}
		return a.Name < b.Name
	})
	return inv, warnings
}

var typeOrder = map[config.ServiceType]int{
	config.ServiceTypeRDS: 0, config.ServiceTypeRedis: 1, config.ServiceTypeDocumentDB: 2,
	config.ServiceTypeRedshift: 3, config.ServiceTypeOpenSearch: 4, config.ServiceTypeNeptune: 5,
}

func bastions(ctx context.Context, api SSMAPI) ([]Bastion, error) {
	var out []Bastion
	p := ssm.NewDescribeInstanceInformationPaginator(api, &ssm.DescribeInstanceInformationInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return out, err
		}
		for _, info := range page.InstanceInformationList {
			if info.ResourceType != ssmtypes.ResourceTypeEc2Instance {
				continue // on-premises managed nodes cannot reach VPC endpoints
			}
			out = append(out, Bastion{
				InstanceID: aws.ToString(info.InstanceId),
				Name:       aws.ToString(info.ComputerName),
				Online:     info.PingStatus == ssmtypes.PingStatusOnline,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out, nil
}

// rdsEngine maps an RDS engine name onto the service type and, for RDS
// itself, the protocol family; ok is false for engines den does not handle.
func rdsEngine(engine string) (typ config.ServiceType, family string, ok bool) {
	switch engine {
	case "postgres", "aurora-postgresql":
		return config.ServiceTypeRDS, config.RDSEnginePostgres, true
	case "mysql", "mariadb", "aurora-mysql", "aurora":
		return config.ServiceTypeRDS, config.RDSEngineMySQL, true
	case "docdb":
		return config.ServiceTypeDocumentDB, "", true
	case "neptune":
		return config.ServiceTypeNeptune, "", true
	}
	return "", "", false
}

func rdsResources(ctx context.Context, api RDSAPI, inv *Inventory) error {
	clusters := rds.NewDescribeDBClustersPaginator(api, &rds.DescribeDBClustersInput{})
	for clusters.HasMorePages() {
		page, err := clusters.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, c := range page.DBClusters {
			typ, family, ok := rdsEngine(aws.ToString(c.Engine))
			if !ok || aws.ToString(c.Endpoint) == "" {
				continue
			}
			r := Resource{
				Type: typ, Name: aws.ToString(c.DBClusterIdentifier), Host: aws.ToString(c.Endpoint),
				Port: int(aws.ToInt32(c.Port)), Engine: family, DBName: aws.ToString(c.DatabaseName),
				IAMAuth: aws.ToBool(c.IAMDatabaseAuthenticationEnabled) || typ == config.ServiceTypeDocumentDB,
			}
			if typ == config.ServiceTypeDocumentDB {
				r.Notes = append(r.Notes, "DocumentDB IAM auth is per database user: create one for your IAM role")
			}
			inv.Resources = append(inv.Resources, r)
		}
	}
	instances := rds.NewDescribeDBInstancesPaginator(api, &rds.DescribeDBInstancesInput{})
	for instances.HasMorePages() {
		page, err := instances.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, in := range page.DBInstances {
			// Cluster members are reached through their cluster's endpoint.
			if aws.ToString(in.DBClusterIdentifier) != "" || in.Endpoint == nil {
				continue
			}
			typ, family, ok := rdsEngine(aws.ToString(in.Engine))
			if !ok || typ != config.ServiceTypeRDS {
				continue
			}
			inv.Resources = append(inv.Resources, Resource{
				Type: typ, Name: aws.ToString(in.DBInstanceIdentifier), Host: aws.ToString(in.Endpoint.Address),
				Port: int(aws.ToInt32(in.Endpoint.Port)), Engine: family, DBName: aws.ToString(in.DBName),
				IAMAuth: aws.ToBool(in.IAMDatabaseAuthenticationEnabled),
			})
		}
	}
	return nil
}

func elastiCacheResources(ctx context.Context, api ElastiCacheAPI, inv *Inventory) error {
	groups := elasticache.NewDescribeReplicationGroupsPaginator(api, &elasticache.DescribeReplicationGroupsInput{})
	for groups.HasMorePages() {
		page, err := groups.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, g := range page.ReplicationGroups {
			r := Resource{Type: config.ServiceTypeRedis, Name: aws.ToString(g.ReplicationGroupId), IAMAuth: true, CacheName: aws.ToString(g.ReplicationGroupId)}
			switch {
			case g.ConfigurationEndpoint != nil:
				r.Host, r.Port = aws.ToString(g.ConfigurationEndpoint.Address), int(aws.ToInt32(g.ConfigurationEndpoint.Port))
				r.Notes = append(r.Notes, "cluster mode: through one tunnel only single-shard clusters work")
			case len(g.NodeGroups) > 0 && g.NodeGroups[0].PrimaryEndpoint != nil:
				ep := g.NodeGroups[0].PrimaryEndpoint
				r.Host, r.Port = aws.ToString(ep.Address), int(aws.ToInt32(ep.Port))
			default:
				continue
			}
			if !aws.ToBool(g.TransitEncryptionEnabled) {
				r.Notes = append(r.Notes, "in-transit encryption is off, but den's redis-cli command uses --tls")
			}
			inv.Resources = append(inv.Resources, r)
		}
	}
	caches := elasticache.NewDescribeServerlessCachesPaginator(api, &elasticache.DescribeServerlessCachesInput{})
	for caches.HasMorePages() {
		page, err := caches.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, c := range page.ServerlessCaches {
			if c.Endpoint == nil {
				continue
			}
			inv.Resources = append(inv.Resources, Resource{
				Type: config.ServiceTypeRedis, Name: aws.ToString(c.ServerlessCacheName),
				Host: aws.ToString(c.Endpoint.Address), Port: int(aws.ToInt32(c.Endpoint.Port)),
				IAMAuth: true, CacheName: aws.ToString(c.ServerlessCacheName), Serverless: true,
			})
		}
	}
	return nil
}

func memoryDBResources(ctx context.Context, api MemoryDBAPI, inv *Inventory) error {
	p := memorydb.NewDescribeClustersPaginator(api, &memorydb.DescribeClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, c := range page.Clusters {
			if c.ClusterEndpoint == nil {
				continue
			}
			r := Resource{
				Type: config.ServiceTypeRedis, Name: aws.ToString(c.Name),
				Host: aws.ToString(c.ClusterEndpoint.Address), Port: int(c.ClusterEndpoint.Port),
				IAMAuth: true, CacheName: aws.ToString(c.Name), MemoryDB: true,
			}
			if aws.ToInt32(c.NumberOfShards) > 1 {
				r.Notes = append(r.Notes, "more than one shard: through one tunnel only single-shard clusters work")
			}
			inv.Resources = append(inv.Resources, r)
		}
	}
	return nil
}

func redshiftResources(ctx context.Context, prov RedshiftAPI, sls RedshiftServerlessAPI, inv *Inventory) error {
	p := redshift.NewDescribeClustersPaginator(prov, &redshift.DescribeClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, c := range page.Clusters {
			if c.Endpoint == nil {
				continue
			}
			inv.Resources = append(inv.Resources, Resource{
				Type: config.ServiceTypeRedshift, Name: aws.ToString(c.ClusterIdentifier),
				Host: aws.ToString(c.Endpoint.Address), Port: int(aws.ToInt32(c.Endpoint.Port)),
				IAMAuth: true, ClusterIdentifier: aws.ToString(c.ClusterIdentifier), DBName: aws.ToString(c.DBName),
			})
		}
	}
	wp := redshiftserverless.NewListWorkgroupsPaginator(sls, &redshiftserverless.ListWorkgroupsInput{})
	for wp.HasMorePages() {
		page, err := wp.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, w := range page.Workgroups {
			if w.Endpoint == nil {
				continue
			}
			inv.Resources = append(inv.Resources, Resource{
				Type: config.ServiceTypeRedshift, Name: aws.ToString(w.WorkgroupName),
				Host: aws.ToString(w.Endpoint.Address), Port: int(aws.ToInt32(w.Endpoint.Port)),
				IAMAuth: true, WorkgroupName: aws.ToString(w.WorkgroupName), Serverless: true,
			})
		}
	}
	return nil
}

func openSearchResources(ctx context.Context, api OpenSearchAPI, inv *Inventory) error {
	names, err := api.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if err != nil {
		return err
	}
	var all []string
	for _, d := range names.DomainNames {
		all = append(all, aws.ToString(d.DomainName))
	}
	// DescribeDomains takes at most five names per call.
	for len(all) > 0 {
		batch := all[:min(5, len(all))]
		all = all[len(batch):]
		out, err := api.DescribeDomains(ctx, &opensearch.DescribeDomainsInput{DomainNames: batch})
		if err != nil {
			return err
		}
		for _, d := range out.DomainStatusList {
			host := d.Endpoints["vpc"]
			if host == "" {
				continue // a public domain needs no tunnel
			}
			inv.Resources = append(inv.Resources, Resource{
				Type: config.ServiceTypeOpenSearch, Name: aws.ToString(d.DomainName), Host: host, Port: 443, IAMAuth: true,
			})
		}
	}
	return nil
}

// Online returns the bastions whose SSM agent is online.
func (inv Inventory) Online() []Bastion {
	var out []Bastion
	for _, b := range inv.Bastions {
		if b.Online {
			out = append(out, b)
		}
	}
	return out
}

// Summary is a one-line count per service type, for the command's output.
func (inv Inventory) Summary() string {
	counts := map[config.ServiceType]int{}
	for _, r := range inv.Resources {
		counts[r.Type]++
	}
	var parts []string
	for _, t := range []config.ServiceType{config.ServiceTypeRDS, config.ServiceTypeRedis, config.ServiceTypeDocumentDB,
		config.ServiceTypeRedshift, config.ServiceTypeOpenSearch, config.ServiceTypeNeptune} {
		if counts[t] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[t], t))
		}
	}
	if len(parts) == 0 {
		return "no data stores"
	}
	return strings.Join(parts, ", ")
}
