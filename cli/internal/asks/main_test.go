package asks_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/testenv"
)

// the store records ask rows in the ledger, which must not be the user's
func TestMain(m *testing.M) { testenv.Main(m) }
