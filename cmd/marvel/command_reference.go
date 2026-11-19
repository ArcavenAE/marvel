package main

import "github.com/spf13/cobra"

// commandReference renders one markdown page per visible command of the cobra
// tree rooted at root. The key is the page file name, the value is its
// content. The output depends on the tree alone: no date, no host, no
// generator stamp.
//
// Stub: the generator lands in the next commit, after the tests below have
// been run red by a second seat.
func commandReference(root *cobra.Command) map[string]string {
	return nil
}
