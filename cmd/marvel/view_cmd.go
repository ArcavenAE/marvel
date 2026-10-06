package main

import "github.com/spf13/cobra"

func viewCmd() *cobra.Command {
	return &cobra.Command{Use: "view"}
}

func viewRefreshParams(args []string) []byte { return nil }
