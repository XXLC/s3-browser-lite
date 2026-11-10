//go:build !windows

package transfers

import "os"

func reparse(os.FileInfo) bool { return false }
