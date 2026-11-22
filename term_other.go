//go:build !linux && !darwin

package main

import "os"

func terminalWidth(*os.File) int { return 0 }
