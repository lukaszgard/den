package service

import "time"

// Summary is a service's state as plain data, for `den list --json` and for
// agents driving den through MCP. It never carries a credential.
type Summary struct {
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	Environment    string     `json:"environment,omitempty"`
	Description    string     `json:"description,omitempty"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	LocalPort      int        `json:"local_port,omitempty"`
	ConnectCommand string     `json:"connect_command,omitempty"`
	TokenExpires   *time.Time `json:"token_expires,omitempty"`
	Production     bool       `json:"production"`
}

// Summarize describes svc as it is now.
func Summarize(svc Service) Summary {
	info, st := svc.Info(), svc.State()
	s := Summary{
		Name:           info.Name,
		Type:           info.Type,
		Environment:    info.Environment,
		Production:     info.Production,
		Status:         st.Status.String(),
		LocalPort:      st.LocalPort,
		ConnectCommand: svc.ConnectCommand(),
		TokenExpires:   st.TokenExpires,
	}
	if st.Error != nil {
		s.Error = st.Error.Error()
	}
	if d, ok := svc.(Describer); ok {
		s.Description = d.Description()
	}
	return s
}
