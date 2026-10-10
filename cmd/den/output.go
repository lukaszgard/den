package main

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/service"
)

// writeJSON prints v as indented JSON: the machine-readable form of every
// listing, for scripts and agents.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// serviceSummaries describes every registered service for --json output.
func serviceSummaries(mgr *service.Manager) []service.Summary {
	out := []service.Summary{}
	for _, svc := range mgr.Services() {
		s := service.Summarize(svc)
		out = append(out, s)
	}
	return out
}

// configJSON converts the resolved config to JSON with den.yaml's own key
// names: the structs only carry yaml tags, so it goes through YAML.
func configJSON(cfg *config.Config) (any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("convert config: %w", err)
	}
	return v, nil
}

// runbookEntry is one runbook or sequence in `den run --list --json`.
type runbookEntry struct {
	Name        string             `json:"name"`
	Kind        string             `json:"kind"` // runbook | sequence
	Description string             `json:"description,omitempty"`
	Confirm     bool               `json:"confirm"`
	Params      []config.ParamSpec `json:"params,omitempty"`
	Steps       []string           `json:"steps,omitempty"`
	Source      string             `json:"source,omitempty"` // file a discovered runbook came from
	Dir         string             `json:"dir,omitempty"`    // folder of a runbook with a runbook.yaml
	From        string             `json:"from,omitempty"`   // the runbook source it comes from
	Commit      string             `json:"commit,omitempty"` // the commit of that source checked out
	Note        string             `json:"note,omitempty"`   // why it asks first: new, or changed by an update
}

func runbookEntries(reg *runbook.Registry) []runbookEntry {
	out := []runbookEntry{}
	for _, r := range reg.Runbooks() {
		rb := r.Runbook()
		e := runbookEntry{
			Name: rb.Name, Kind: "runbook", Description: rb.Description,
			Confirm: rb.Confirm, Params: rb.Params, Source: rb.Source, Dir: rb.Dir,
		}
		if rb.Remote != nil {
			e.From, e.Commit, e.Note = rb.Remote.Source, rb.Remote.Commit, rb.Remote.Note
		}
		out = append(out, e)
	}
	for _, s := range reg.Sequences() {
		seq := s.Sequence()
		out = append(out, runbookEntry{
			Name: seq.Name, Kind: "sequence", Description: seq.Description,
			Confirm: seq.Confirm, Steps: seq.Steps,
		})
	}
	return out
}
