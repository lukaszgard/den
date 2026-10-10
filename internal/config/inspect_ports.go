package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// portUse is one local port den.yaml has something listen on.
type portUse struct {
	port int
	user string // the entry's name
	key  string
}

// portProblems checks the ports services and tunnels listen on: out of range,
// below 1024, and the same port twice. Two entries on one port cannot both be
// connected, and the default ports make that easy to do without writing it.
func portProblems(cfg *Config, root *yaml.Node) Problems {
	var uses []portUse
	for i, svc := range cfg.Services {
		if t := svc.Target(); t != nil && t.LocalPort != 0 {
			key := firstKey(root, fmt.Sprintf("services[%d].%s.local_port", i, svc.Type), fmt.Sprintf("services[%d]", i))
			uses = append(uses, portUse{t.LocalPort, svc.Name, key})
		}
	}
	for i, t := range cfg.Tunnels {
		for _, port := range tunnelPorts(t.Command) {
			uses = append(uses, portUse{port, t.Name, fmt.Sprintf("tunnels[%d].command", i)})
		}
	}

	var ps Problems
	byPort := map[int][]portUse{}
	var order []int
	for _, u := range uses {
		switch {
		case u.port < 1 || u.port > 65535:
			ps = append(ps, Problem{
				Severity: SeverityWarning, Line: lineAt(root, u.key), Key: u.key, Code: "port-range",
				Msg:  fmt.Sprintf("%q: local port %d is not a port (1-65535)", u.user, u.port),
				Hint: "use a port between 1024 and 65535",
			})
			continue
		case u.port < 1024:
			ps = append(ps, Problem{
				Severity: SeverityWarning, Line: lineAt(root, u.key), Key: u.key, Code: "port-privileged",
				Msg:  fmt.Sprintf("%q: local port %d is below 1024, binding it needs root", u.user, u.port),
				Hint: "use a port above 1024",
			})
		}
		if _, seen := byPort[u.port]; !seen {
			order = append(order, u.port)
		}
		byPort[u.port] = append(byPort[u.port], u)
	}
	for _, port := range order {
		us := byPort[port]
		// One entry may hold a port twice (a tunnel with two -L on it): that
		// is its own mistake, but not a conflict between entries.
		var names []string
		for _, u := range us {
			if !slices.Contains(names, strconv.Quote(u.user)) {
				names = append(names, strconv.Quote(u.user))
			}
		}
		if len(names) < 2 {
			continue
		}
		ps = append(ps, Problem{
			Severity: SeverityWarning, Line: lineAt(root, us[0].key), Key: us[0].key, Code: "port-shared",
			Msg:  fmt.Sprintf("local port %d is used by %s: only one can be connected at a time", port, joinNames(names)),
			Hint: "give each its own local_port",
		})
	}
	return ps
}

// joinNames is "a", "a and b" or "a, b and c".
func joinNames(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// tunnelPorts are the local ports an ssh command listens on: the port of every
// -L [bind:]port:host:hostport and -D [bind:]port, written apart from or
// joined to the flag.
func tunnelPorts(command string) []int {
	fields := strings.Fields(command)
	var ports []int
	for i, f := range fields {
		var spec string
		switch {
		case (f == "-L" || f == "-D") && i+1 < len(fields):
			spec = fields[i+1]
		case strings.HasPrefix(f, "-L") || strings.HasPrefix(f, "-D"):
			spec = f[2:]
		default:
			continue
		}
		if p, ok := forwardPort(f[:2] == "-D", spec); ok {
			ports = append(ports, p)
		}
	}
	return ports
}

// forwardPort is the listening port of an ssh forward spec. A socket path or
// anything else that is not a port gives false.
func forwardPort(dynamic bool, spec string) (int, bool) {
	// [::1]:8080:... — the bind address is bracketed because it has colons.
	if strings.HasPrefix(spec, "[") {
		end := strings.Index(spec, "]:")
		if end < 0 {
			return 0, false
		}
		spec = spec[end+2:]
	}
	parts := strings.Split(spec, ":")
	var port string
	switch {
	case dynamic && len(parts) == 1:
		port = parts[0]
	case dynamic && len(parts) == 2:
		port = parts[1]
	case dynamic:
		return 0, false
	case len(parts) >= 4:
		port = parts[1]
	case len(parts) == 2:
		port = parts[0] // port:remote_socket
	case len(parts) == 3:
		// port:host:hostport, or bind:port:remote_socket.
		if _, err := strconv.Atoi(parts[0]); err == nil {
			port = parts[0]
		} else {
			port = parts[1]
		}
	default:
		return 0, false
	}
	n, err := strconv.Atoi(port)
	return n, err == nil
}
