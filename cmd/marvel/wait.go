package main

import "fmt"

// exitError carries a process exit code out of a command. main exits with
// Code; Msg is what the command prints first.
type exitError struct {
	Code int
	Msg  string
}

func (e *exitError) Error() string { return fmt.Sprintf("%s (exit %d)", e.Msg, e.Code) }
