package runbook

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// found is one runbook a scan turned up, with where it comes from when that
// is a runbook source.
type found struct {
	spec   config.RunbookSpec
	remote *Remote
}

// sourceRunbooks lists the runbooks of every source in cfg, from the tree
// den.lock pins each one to.
//
// They are someone else's code, run with your credentials, so they are held
// to stricter rules than your own: each is named after its source, so it can
// never take a name you use; its env must be mapped to one of yours, because
// the author's "prod" need not be your prod; it cannot set what t and e
// start, nor change den's context through env_vars; it runs inside the
// repository; and its text is stripped of terminal escape sequences.
func sourceRunbooks(cfg config.RunbooksConfig, envs map[string]config.EnvSpec, store *source.Store, lock source.Lock) ([]found, []Warning) {
	var (
		out   []found
		warns []Warning
	)
	for _, src := range cfg.Sources {
		warn := func(format string, args ...any) {
			warns = append(warns, Warning{Msg: fmt.Sprintf("source %s: ", src.Name) + fmt.Sprintf(format, args...)})
		}
		c := checkoutOf(src, store, lock)
		if c.problem != "" {
			warn("%s", c.problem)
		}
		if !c.usable {
			continue
		}
		repo, tree, idx, locked := c.repo, c.tree, c.index, *c.locked
		base := filepath.Join(tree, filepath.FromSlash(source.RepoPath(src.Path)))
		folders := source.Folders(src, idx)
		for _, rel := range slices.Sorted(maps.Keys(folders)) {
			if strings.HasPrefix(rel, ".") || !Selected(src, folderLabel(src, rel)) {
				continue
			}
			dir := filepath.Join(base, filepath.FromSlash(rel))
			spec, named, err := loadManifestNamed(dir)
			if err != nil {
				msg := err.Error()
				if strings.Contains(msg, "not found in type") {
					msg += " — it may be written for a newer den"
				}
				warns = append(warns, Warning{Path: filepath.Join(dir, ManifestName), Msg: msg + " — skipped"})
				continue
			}
			spec.Name = qualifiedName(src, rel, spec.Name, named)
			skip, notes := harden(&spec, src, tree)
			for _, n := range notes {
				warns = append(warns, Warning{Path: spec.Source, Msg: n})
			}
			if skip != "" {
				warns = append(warns, Warning{Path: spec.Source, Msg: skip + " — skipped"})
				continue
			}
			if err := config.ValidateRunbook(spec, envs); err != nil {
				warns = append(warns, Warning{Path: spec.Source, Msg: err.Error() + " — skipped"})
				continue
			}
			out = append(out, found{spec: spec, remote: &Remote{
				Source: src.Name, Folder: rel, Repo: repo.ID, Commit: locked.Commit, Tree: folders[rel],
			}})
		}
	}
	return out, warns
}

// checkout is where one source stands on this machine.
type checkout struct {
	repo    config.Repo
	locked  *source.Locked // its den.lock entry; nil when it has none
	tree    string
	index   source.Index
	usable  bool   // its tree is here: its runbooks can be listed
	problem string // what is wrong, or off, with it, and the command that fixes it
}

func checkoutOf(src config.RunbookSource, store *source.Store, lock source.Lock) checkout {
	var c checkout
	repo, err := config.ParseRepo(src.URL)
	if err != nil {
		c.problem = err.Error()
		return c
	}
	c.repo = repo
	locked, ok := lock.Sources[src.Name]
	if !ok {
		c.problem = fmt.Sprintf("not checked out yet: den runbook source update %s", src.Name)
		return c
	}
	c.locked = &locked
	if was, err := config.ParseRepo(locked.URL); err != nil || was.ID != repo.ID {
		c.problem = fmt.Sprintf("den.yaml points at %s now, but %s is checked out: den runbook source update %s", repo.ID, was.ID, src.Name)
		return c
	}
	c.tree = store.Tree(src, locked.Commit)
	c.index, ok = store.Index(src, locked.Commit)
	if c.tree == "" || !ok {
		c.problem = fmt.Sprintf("den.lock pins %s, which is not checked out on this machine: den runbook source sync", source.Short(locked.Commit))
		return c
	}
	c.usable = true
	if locked.Ref != src.Ref {
		c.problem = fmt.Sprintf("den.yaml asks for ref %q, but %s (ref %q) is still checked out: den runbook source update %s",
			src.Ref, source.Short(locked.Commit), locked.Ref, src.Name)
	}
	return c
}

// folderLabel is how include and exclude name a runbook folder: its folder's
// name, or the source's for a runbook at the source's path itself.
func folderLabel(src config.RunbookSource, rel string) string {
	if rel == "" {
		return src.Name
	}
	return rel
}

// qualifiedName is the name a source runbook is listed under: its source's,
// a slash, and its own (its folder's when the manifest names none). A
// runbook at the source's path itself is the source's name alone, unless its
// manifest names it.
func qualifiedName(src config.RunbookSource, rel, name string, named bool) string {
	if rel == "" && !named {
		return src.Name
	}
	return src.Name + "/" + CleanText(name)
}

// harden applies a source's rules to spec. skip says why the runbook cannot
// be listed; notes are what was changed on the way.
func harden(spec *config.RunbookSpec, src config.RunbookSource, tree string) (skip string, notes []string) {
	// What t, e and E start is yours to choose: a runbook's own shell,
	// editor or agent would run its code without anyone pressing r. How many
	// runs are kept on your disk is yours too.
	spec.Shell, spec.Editor, spec.Agent, spec.History = "", "", "", nil

	// The confirmation shows the command; control characters in it, an
	// escape sequence that conceals text above all, could hide what runs.
	if strings.ContainsFunc(spec.Command, func(r rune) bool { return r != '\n' && r != '\t' && unicode.IsControl(r) }) {
		return fmt.Sprintf("%s: its command holds control characters, which could hide part of it in the confirmation", spec.Name), notes
	}

	spec.Description = CleanText(spec.Description)
	for i := range spec.Params {
		p := &spec.Params[i]
		p.Name, p.Description, p.Default = CleanText(p.Name), CleanText(p.Description), CleanText(p.Default)
		for j := range p.Options {
			p.Options[j] = CleanText(p.Options[j])
		}
	}

	if spec.Env != "" {
		mapped, ok := src.EnvMap[spec.Env]
		if !ok {
			return fmt.Sprintf("%s uses env %q: map it to one of your environments in runbooks.sources (%s) env_map", spec.Name, spec.Env, src.Name), notes
		}
		spec.Env = mapped
	}

	var dropped []string
	for k := range spec.EnvVars {
		if k == "PATH" || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "DEN_") {
			dropped = append(dropped, k)
		}
	}
	if len(dropped) > 0 {
		slices.Sort(dropped)
		for _, k := range dropped {
			delete(spec.EnvVars, k)
		}
		notes = append(notes, fmt.Sprintf("%s: env_vars %s ignored: a runbook from a source cannot change den's context", spec.Name, strings.Join(dropped, ", ")))
	}

	if rel, err := filepath.Rel(tree, spec.Cwd); err != nil || (rel != "." && !filepath.IsLocal(rel)) {
		return fmt.Sprintf("%s: cwd leaves the repository", spec.Name), notes
	}
	return "", notes
}

// Selected reports whether src's include and exclude let the runbook folder
// called folder show.
func Selected(src config.RunbookSource, folder string) bool {
	if len(src.Include) > 0 && matchAny(src.Include, folder) == "" {
		return false
	}
	return matchAny(src.Exclude, folder) == ""
}

// matchAny is the first of patterns that matches name, or "".
func matchAny(patterns []string, name string) string {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return p
		}
	}
	return ""
}

// SelectFolder is src's include and exclude after installing (on) or
// uninstalling one runbook folder, changing as little as it can. Uninstalling
// never empties include, which would show every runbook.
func SelectFolder(src config.RunbookSource, folder string, on bool) (include, exclude []string, err error) {
	include, exclude = slices.Clone(src.Include), slices.Clone(src.Exclude)
	shown := func() bool {
		return Selected(config.RunbookSource{Include: include, Exclude: exclude}, folder)
	}
	if on {
		exclude = slices.DeleteFunc(exclude, func(p string) bool { return p == folder })
		if len(include) > 0 && matchAny(include, folder) == "" {
			include = append(include, folder)
		}
		if p := matchAny(exclude, folder); p != "" {
			return nil, nil, fmt.Errorf("%s stays hidden by the exclude pattern %q: change it in den.yaml", folder, p)
		}
		return include, exclude, nil
	}
	if !shown() {
		return include, exclude, nil
	}
	if i := slices.Index(include, folder); i >= 0 && len(include) > 1 {
		include = slices.Delete(include, i, i+1)
	}
	if shown() {
		exclude = append(exclude, folder)
	}
	return include, exclude, nil
}

// CleanText strips terminal escape sequences and other control characters
// from text someone else wrote, keeping newlines and tabs: a description that
// could set the window title or write the clipboard (OSC 52) is not text.
func CleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
