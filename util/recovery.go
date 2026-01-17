package util

import (
	"runtime/debug"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// SafeGo runs a function in a goroutine with panic recovery.
// If a panic occurs, it logs the error and stack trace but does not crash the program.
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("[%s] panic recovered: %v\n%s", name, err, debug.Stack())
			}
		}()
		fn()
	}()
}

// SafeGoWithRestart runs a function in a goroutine with panic recovery and automatic restart.
// If a panic occurs, it logs the error and restarts the function.
func SafeGoWithRestart(name string, fn func()) {
	go func() {
		for {
			func() {
				defer func() {
					if err := recover(); err != nil {
						logs.GetLogger().Errorf("[%s] panic recovered, restarting: %v\n%s", name, err, debug.Stack())
					}
				}()
				fn()
			}()
		}
	}()
}
