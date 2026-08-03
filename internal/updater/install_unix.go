//go:build !windows

package updater

import "os"

func installUpgrade(staged, destination string) (bool, error) {
	return false, os.Rename(staged, destination)
}

func runReplacement(string) error { return nil }

func CleanupStaged() {}
