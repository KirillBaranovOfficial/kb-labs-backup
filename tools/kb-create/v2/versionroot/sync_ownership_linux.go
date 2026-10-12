package versionroot

import "os"

func syncOwnershipFile(file *os.File) error { return file.Sync() }
