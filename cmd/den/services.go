package main

import (
	"context"
	"errors"
	"fmt"

	"charm.land/log/v2"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	awssvc "github.com/lukaszgard/den/internal/service/aws"
	"github.com/lukaszgard/den/internal/service/docker"
	documentdbsvc "github.com/lukaszgard/den/internal/service/documentdb"
	"github.com/lukaszgard/den/internal/service/kafka"
	neptunesvc "github.com/lukaszgard/den/internal/service/neptune"
	opensearchsvc "github.com/lukaszgard/den/internal/service/opensearch"
	rdssvc "github.com/lukaszgard/den/internal/service/rds"
	"github.com/lukaszgard/den/internal/service/redis"
	redshiftsvc "github.com/lukaszgard/den/internal/service/redshift"
	tunnelsvc "github.com/lukaszgard/den/internal/service/tunnel"
	vpnsvc "github.com/lukaszgard/den/internal/service/vpn"
)

// serviceFactories builds a service for each `services:` type. Adding a type
// means an entry here, next to its config section and its package.
var serviceFactories = map[config.ServiceType]func(config.ServiceSpec) (service.Service, error){
	config.ServiceTypeRDS: func(s config.ServiceSpec) (service.Service, error) { return rdssvc.New(s.Name, s.RDS) },
	config.ServiceTypeKafka: func(s config.ServiceSpec) (service.Service, error) {
		return kafka.New(s.Name, s.Kafka), nil
	},
	config.ServiceTypeRedis: func(s config.ServiceSpec) (service.Service, error) { return redis.New(s.Name, s.Redis) },
	config.ServiceTypeDocumentDB: func(s config.ServiceSpec) (service.Service, error) {
		return documentdbsvc.New(s.Name, s.DocumentDB)
	},
	config.ServiceTypeRedshift: func(s config.ServiceSpec) (service.Service, error) {
		return redshiftsvc.New(s.Name, s.Redshift)
	},
	config.ServiceTypeOpenSearch: func(s config.ServiceSpec) (service.Service, error) {
		return opensearchsvc.New(s.Name, s.OpenSearch)
	},
	config.ServiceTypeNeptune: func(s config.ServiceSpec) (service.Service, error) {
		return neptunesvc.New(s.Name, s.Neptune)
	},
	config.ServiceTypeDocker: func(s config.ServiceSpec) (service.Service, error) {
		return docker.New(s.Name, s.Docker), nil
	},
}

// awsSessionSpec is what an AWS SSO session service is built from, so a
// reload keeps the instance only while none of it changed.
type awsSessionSpec struct {
	Session   config.SSOSession
	AWSConfig string
	CacheDir  string
}

// serviceEntries lists every service den.yaml asks for: `services:`, then
// tunnels, VPNs, and one login entry per AWS SSO session discovered in the AWS
// config. Problems that leave a service out are returned as warnings.
func serviceEntries(cfg *config.Config) (entries []service.Entry, warnings []error) {
	if cfg == nil {
		return nil, nil
	}
	seen := map[string]int{}
	key := func(typ, name string) string {
		k := typ + ":" + name
		seen[k]++
		if n := seen[k]; n > 1 {
			k = fmt.Sprintf("%s#%d", k, n)
		}
		return k
	}

	for _, spec := range cfg.Services {
		build, ok := serviceFactories[spec.Type]
		if !ok {
			warnings = append(warnings, fmt.Errorf("service %q: unknown type %q", spec.Name, spec.Type))
			continue
		}
		entries = append(entries, service.Entry{
			Key:   key(string(spec.Type), spec.Name),
			Spec:  spec,
			Build: func() (service.Service, error) { return build(spec) },
		})
	}
	for _, spec := range cfg.Tunnels {
		entries = append(entries, service.Entry{
			Key:   key(service.TypeTunnel, spec.Name),
			Spec:  spec,
			Build: func() (service.Service, error) { return tunnelsvc.New(spec), nil },
		})
	}
	for _, spec := range cfg.VPNs {
		entries = append(entries, service.Entry{
			Key:   key(service.TypeVPN, spec.Name),
			Spec:  spec,
			Build: func() (service.Service, error) { return vpnsvc.New(spec.Name, &spec) },
		})
	}

	// The session→profile mapping lives in config.ResolveSSOSessions so the AWS
	// pane sees exactly the same list.
	awsCfg := cfg.AWSConfigPath()
	profiles, err := config.LoadAWSProfiles(awsCfg)
	if err != nil {
		// A missing or unreadable AWS config must not stop den from starting.
		return entries, warnings
	}
	sessions, ws := config.ResolveSSOSessions(profiles, cfg.AWSSessions())
	for _, w := range ws {
		warnings = append(warnings, errors.New(w))
	}
	cacheDir := config.SSOCacheDir(awsCfg)
	for _, s := range sessions {
		spec := awsSessionSpec{Session: s, AWSConfig: awsCfg, CacheDir: cacheDir}
		entries = append(entries, service.Entry{
			Key:  key(service.TypeAWS, s.DisplayName),
			Spec: spec,
			Build: func() (service.Service, error) {
				return awssvc.New(s.DisplayName, s.Name, s.Description, s.StartURL, s.Region,
					spec.AWSConfig, spec.CacheDir, s.Profile), nil
			},
		})
	}
	return entries, warnings
}

// applyConfig makes mgr hold exactly the services cfg describes, keeping the
// running instances of those that did not change.
func applyConfig(ctx context.Context, mgr *service.Manager, cfg *config.Config) service.ReconcileResult {
	entries, warnings := serviceEntries(cfg)
	res := mgr.Reconcile(ctx, entries)
	res.Errors = append(warnings, res.Errors...)
	return res
}

// loadConfig finds, loads and validates the config file. With no explicit
// path and no file found it returns an empty path and a nil config: den then
// shows an empty dashboard. A missing explicit path is an error, and so is a
// file with errors, each of them on a line of its own; warnings are logged.
func loadConfig(explicit string) (string, *config.Config, error) {
	path, cfg, problems, err := checkConfig(explicit)
	if err != nil {
		return path, nil, err
	}
	if err := problems.Err(path); err != nil {
		return path, nil, err
	}
	for _, p := range problems {
		log.Warn(p.Located(path))
	}
	return path, cfg, nil
}

// errNoConfig is the error for a command that needs a den.yaml when none is
// found: it names every place that was tried, as this machine resolves them.
func errNoConfig() error {
	_, err := config.Discover("")
	if err == nil {
		err = errors.New("no config file found")
	}
	return fmt.Errorf("%w; pass -c, or run `den paths`", err)
}

// checkConfig finds the config file and checks it. Unlike loadConfig it hands
// back a file den cannot use as its problems, with a nil config, rather than
// as an error: the TUI opens anyway, so the file can be fixed from den.
func checkConfig(explicit string) (string, *config.Config, config.Problems, error) {
	path, err := config.Discover(explicit)
	if err != nil {
		if explicit != "" {
			return "", nil, nil, err
		}
		return "", nil, nil, nil
	}
	cfg, problems, err := config.LoadChecked(path)
	if err != nil {
		return path, nil, nil, fmt.Errorf("load config: %w", err)
	}
	return path, cfg, problems, nil
}

// logSkipped reports services a config could not produce, before the TUI takes
// over the terminal.
func logSkipped(res service.ReconcileResult) {
	for _, err := range res.Errors {
		log.Warn("skipping", "err", err)
	}
}
