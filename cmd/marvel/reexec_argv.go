package main

import "errors"

// parseCmdline splits /proc/<pid>/cmdline into the process's arguments.
func parseCmdline(b []byte) ([]string, error) { return nil, errors.New("not implemented") }

// parseProcargs2 reads the arguments out of the kern.procargs2 sysctl.
func parseProcargs2(b []byte) ([]string, error) { return nil, errors.New("not implemented") }
