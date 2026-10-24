//go:build darwin

package patterns

import "golang.org/x/sys/unix"

func renameRecoveryNoReplace(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
