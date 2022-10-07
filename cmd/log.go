package main

import (
	"fmt"
	"io"
)

type stderrLogger struct {
	verbose bool
	stderr  io.Writer
}

func (l stderrLogger) Debugf(format string, a ...interface{}) {
	if l.verbose {
		fmt.Fprintln(l.stderr, `>`, fmt.Sprintf(format, a...))
	}
}

func (l stderrLogger) Failuref(format string, a ...interface{}) {
	fmt.Fprintln(l.stderr, `✗`, fmt.Sprintf(format, a...))
}
