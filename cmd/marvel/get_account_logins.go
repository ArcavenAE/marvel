package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// getAccountLogins is `marvel get account-logins`: each login's expiry state as
// the daemon's login expiry service last read it (docs/design/native-checks.md
// section 2.5). It reads the daemon's memory only; nothing asks a harness and
// nothing gates on it.
func getAccountLogins(w io.Writer, output string) error {
	if output != "" && output != "json" {
		return fmt.Errorf("get account-logins prints json with -o json; -o %s is not supported", output)
	}
	params, err := json.Marshal(daemonrpc.GetParams{ResourceType: "account-logins"})
	if err != nil {
		return err
	}
	resp, err := sendTimed(daemonrpc.Request{Method: "get", Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	var doc api.AccountLogins
	if err := json.Unmarshal(resp.Result, &doc); err != nil {
		return fmt.Errorf("decode account logins: %w", err)
	}
	if output == "json" {
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	return printAccountLogins(w, doc)
}

// printAccountLogins writes one row per login. A login with no expiry shows a
// dash, and an unreadable one says why and since when its reads have failed.
func printAccountLogins(w io.Writer, doc api.AccountLogins) error {
	if !doc.Enabled {
		_, err := fmt.Fprintln(w, "login expiry is off on this daemon: set login_expiry: true on its cluster in the marvel config to turn it on (docs/design/native-checks.md)")
		return err
	}
	if len(doc.Logins) == 0 {
		_, err := fmt.Fprintln(w, "no logins read yet")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ACCOUNT\tSTATE\tEXPIRES\tLEFT\tREAD\tWHY")
	for _, l := range doc.Logins {
		expires, left, read, why := "-", "-", "-", ""
		if l.ExpiresAt != nil {
			expires = l.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if l.HoursLeft != nil {
			left = fmt.Sprintf("%dh", *l.HoursLeft)
		}
		if !l.ReadAt.IsZero() {
			read = l.ReadAt.UTC().Format(time.RFC3339)
		}
		if l.Why != "" {
			why = l.Why
			if l.FailingSince != nil {
				why += " since " + l.FailingSince.UTC().Format(time.RFC3339)
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", l.Account, l.State, expires, left, read, dashIfEmpty(why))
	}
	return tw.Flush()
}
