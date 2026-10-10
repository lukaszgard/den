package sigv4proxy

import (
	"testing"

	"github.com/lukaszgard/den/internal/leakcheck"
)

func TestMain(m *testing.M) { leakcheck.Main(m) }
