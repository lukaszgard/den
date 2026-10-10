package ssmfwd

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// Transport starts the process that exposes one Forward on localhost. The
// Tunnel runs it under a supervisor and owns everything else: readiness,
// retries, credentials, monitoring and reconnects are the same whichever way
// the network is reached.
type Transport interface {
	// Name labels the process in the service log, e.g. "SSM session".
	Name() string
	// Command returns the program and arguments that forward f.
	Command(ctx context.Context, f Forward) (name string, args []string, err error)
	// ReadyTimeout bounds how long the local port may take to accept
	// connections before the start counts as failed.
	ReadyTimeout() time.Duration
}

// NewTransport returns the transport env names: SSM through its bastion
// instance unless env.Transport says otherwise.
func NewTransport(env config.EnvMapping) (Transport, error) {
	spec := env.Transport
	switch spec.TypeOrDefault() {
	case config.TransportSSM:
		return ssmTransport{env: env}, nil
	case config.TransportSSH:
		return sshTransport{host: spec.Host, args: spec.Args}, nil
	case config.TransportEICE:
		return eiceTransport{env: env, endpointID: spec.EndpointID, resolve: net.DefaultResolver.LookupHost}, nil
	case config.TransportKubectl:
		return kubectlTransport{resource: spec.Resource, context: spec.Context, namespace: spec.Namespace}, nil
	}
	return nil, fmt.Errorf("unknown transport %q", spec.Type)
}

// ── SSM ──────────────────────────────────────────────────────────────────────

// ssmTransport forwards through AWS-StartPortForwardingSessionToRemoteHost on
// the environment's bastion instance.
type ssmTransport struct{ env config.EnvMapping }

func (ssmTransport) Name() string { return "SSM session" }

func (t ssmTransport) Command(_ context.Context, f Forward) (string, []string, error) {
	return "aws", ssmArgs(t.env, f), nil
}

func (ssmTransport) ReadyTimeout() time.Duration { return 10 * time.Second }

// ssmArgs builds the aws CLI arguments that open forward f through env's bastion.
func ssmArgs(env config.EnvMapping, f Forward) []string {
	args := []string{"ssm", "start-session"}
	// An empty flag would make the CLI fail on "", where leaving it out lets
	// AWS_PROFILE, AWS_REGION and the default chain decide.
	if env.AWSRegionCode != "" {
		args = append(args, "--region", env.AWSRegionCode)
	}
	args = append(args,
		"--target", env.EC2InstanceID,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", f.parameters(),
	)
	if env.AWSProfile != "" {
		args = append(args, "--profile", env.AWSProfile)
	}
	return args
}

// ── SSH ──────────────────────────────────────────────────────────────────────

// sshTransport forwards with ssh -L through a bastion host.
type sshTransport struct {
	host string
	args []string
}

func (sshTransport) Name() string { return "ssh" }

// Command binds the local end to loopback only. BatchMode makes ssh fail
// instead of prompting — there is no terminal to answer — and
// ExitOnForwardFailure makes a taken port an exit rather than a live ssh
// forwarding nothing.
func (t sshTransport) Command(_ context.Context, f Forward) (string, []string, error) {
	args := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=30",
		"-L", fmt.Sprintf("127.0.0.1:%d:%s:%d", f.LocalPort, f.RemoteHost, f.RemotePort),
	}
	args = append(args, t.args...)
	return "ssh", append(args, t.host), nil
}

func (sshTransport) ReadyTimeout() time.Duration { return 30 * time.Second }

// ── EC2 Instance Connect Endpoint ────────────────────────────────────────────

// eiceTransport forwards through an EC2 Instance Connect Endpoint. The
// endpoint tunnels to private IPs only, so the host is resolved first — RDS
// and ElastiCache endpoints resolve to their private addresses from anywhere.
type eiceTransport struct {
	env        config.EnvMapping
	endpointID string
	resolve    func(ctx context.Context, host string) ([]string, error)
}

func (eiceTransport) Name() string { return "EC2 Instance Connect tunnel" }

func (t eiceTransport) Command(ctx context.Context, f Forward) (string, []string, error) {
	ip, err := t.privateIP(ctx, f.RemoteHost)
	if err != nil {
		return "", nil, err
	}
	args := []string{
		"ec2-instance-connect", "open-tunnel",
		"--instance-connect-endpoint-id", t.endpointID,
		"--private-ip-address", ip,
		"--remote-port", strconv.Itoa(f.RemotePort),
		"--local-port", strconv.Itoa(f.LocalPort),
	}
	if t.env.AWSRegionCode != "" {
		args = append(args, "--region", t.env.AWSRegionCode)
	}
	if t.env.AWSProfile != "" {
		args = append(args, "--profile", t.env.AWSProfile)
	}
	return "aws", args, nil
}

func (t eiceTransport) privateIP(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	addrs, err := t.resolve(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve %s for the EC2 Instance Connect Endpoint: %w", host, err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a, nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0], nil
	}
	return "", fmt.Errorf("%s resolves to no address", host)
}

func (eiceTransport) ReadyTimeout() time.Duration { return 15 * time.Second }

// ── kubectl ──────────────────────────────────────────────────────────────────

// kubectlTransport forwards to a resource inside a Kubernetes cluster, which
// must itself serve the remote port: kubectl cannot reach an arbitrary host,
// so the forward's host is only used by the credential, not the tunnel.
type kubectlTransport struct {
	resource, context, namespace string
}

func (kubectlTransport) Name() string { return "kubectl port-forward" }

func (t kubectlTransport) Command(_ context.Context, f Forward) (string, []string, error) {
	args := []string{"port-forward", "--address", "127.0.0.1"}
	if t.context != "" {
		args = append(args, "--context", t.context)
	}
	if t.namespace != "" {
		args = append(args, "--namespace", t.namespace)
	}
	return "kubectl", append(args, t.resource, fmt.Sprintf("%d:%d", f.LocalPort, f.RemotePort)), nil
}

func (kubectlTransport) ReadyTimeout() time.Duration { return 20 * time.Second }
