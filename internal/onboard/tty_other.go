//go:build !linux && !darwin

package onboard

import "os"

func isTerminal(f *os.File) bool { return false }

func writable(path string) bool { return true }
