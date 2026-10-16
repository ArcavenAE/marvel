package main

import (
	"errors"
	"io"

	"github.com/arcavenae/marvel/internal/api"
)

// attachTarget is what the plan needs to know about the daemon it resolved
// the desk against.
type attachTarget struct {
	Socket string
	Remote bool
	Host   string
}

// attachPlan is what `marvel attach` will do for one desk.
type attachPlan struct {
	Argv []string
	Exec bool
	Note string
}

var errAttachTodo = errors.New("attach: not implemented")

func planAttach(desk string, sessions []api.Session, tgt attachTarget, tmuxEnv string, printOnly bool) (attachPlan, error) {
	return attachPlan{}, errAttachTodo
}

func remoteAttach(desk, host, socket string) (argv []string, printOnly bool) {
	return nil, false
}

func quoteArgv(argv []string) string { return "" }

func runAttach(w io.Writer, desk string, printOnly bool, env attachEnv) error {
	return errAttachTodo
}

type attachEnv struct {
	Getenv func(string) string
	Exec   func(argv []string) error
}
