package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/daemon"
)

func viewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Work with a seat's read-only views",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "refresh <session> [<name>]",
		Short: "Follow a seat's view branch now",
		Long: `Follow the branch of one view of a seat now, or of every view the seat
declares when no name is given, without waiting for the refresh_every tick.
The seat reads the new tree through the same path once the swap lands.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := send(daemon.Request{Method: "view.refresh", Params: viewRefreshParams(args)})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			var res struct {
				Lines []string `json:"lines"`
			}
			_ = json.Unmarshal(resp.Result, &res)
			for _, l := range res.Lines {
				fmt.Println(l)
			}
			return nil
		},
	})
	return cmd
}

// viewRefreshParams is the request body for marvel view refresh: the session
// and, when given, the view name.
func viewRefreshParams(args []string) []byte {
	p := map[string]string{"session": args[0]}
	if len(args) > 1 {
		p["name"] = args[1]
	}
	b, _ := json.Marshal(p)
	return b
}
