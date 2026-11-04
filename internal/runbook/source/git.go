package source

import (
	"context"
	"io"

	"github.com/lukaszgard/den/internal/gitcli"
)

// runner runs git for one source: gitcli never lets git wait on a person.
func (s *Store) runner(source string) gitcli.Runner {
	// core.autocrlf off: git for Windows converts text files to CRLF by
	// default, and a run.sh with CRLF fails its shebang (bash\r).
	return gitcli.Runner{Bin: s.gitBin, Label: "source " + source, Args: []string{"-c", "core.autocrlf=false"}}
}

// git runs one git command for a source and returns its output.
func (s *Store) git(ctx context.Context, source string, args ...string) ([]byte, error) {
	return s.runner(source).Run(ctx, args...)
}

// gitStream runs git with its output going to consume, for output too big to
// hold: an archive of the repository.
func (s *Store) gitStream(ctx context.Context, source string, consume func(io.Reader) error, args ...string) error {
	return s.runner(source).Stream(ctx, consume, args...)
}
