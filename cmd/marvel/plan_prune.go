package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// planPrune asks the daemon's read-only prune_plan for what marvel work --prune
// would delete for the manifest at path. It sends apply's params plus the
// manifest source, never the apply method, so a daemon without prune_plan
// answers "unknown method" and the preview fails closed with exit 1
// (docs/design/work-prune.md section 4).
func planPrune(cmd *cobra.Command, path string, detailed bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &exitError{Code: 1, Msg: "read manifest", Err: err}
	}
	src, err := manifestSource(path)
	if err != nil {
		return &exitError{Code: 1, Msg: fmt.Sprintf("could not compute manifest source (%v); nothing can be pruned without one", err), Err: err}
	}
	p := map[string]any{"manifest_data": data, "manifest_source": src}
	if root := workspaceRootFor(path, declaredWorkspaceRoot(data)); root != "" {
		p["workspace_root"] = root
	}
	params, err := json.Marshal(p)
	if err != nil {
		return &exitError{Code: 1, Msg: "encode prune plan params", Err: err}
	}
	resp, err := send(daemonrpc.Request{Method: "prune_plan", Params: params})
	if err != nil {
		return &exitError{Code: 1, Msg: "could not evaluate the prune plan", Err: err}
	}
	if resp.Error != "" {
		return &exitError{Code: 1, Msg: "could not evaluate the prune plan: " + resp.Error}
	}
	_ = detailed
	var v any
	if err := json.Unmarshal(resp.Result, &v); err != nil {
		return &exitError{Code: 1, Msg: "decode prune plan", Err: err}
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return &exitError{Code: 1, Msg: "encode prune plan", Err: err}
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return err
}
