//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
)

func validatePrivateKeyPermissions(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect private key permissions: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("private key permissions must not grant group or other access")
	}
	return nil
}
