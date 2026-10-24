//go:build !linux && !darwin

package patterns

import "fmt"

func renameRecoveryNoReplace(_, _ string) error {
	return fmt.Errorf("safe recovery generation publication is unsupported on this platform")
}
