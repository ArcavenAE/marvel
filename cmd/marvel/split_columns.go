package main

// accountNames is the client config's display.account_names, set by the
// command that loaded the config. Nil means no names.
var accountNames map[string]string

// fanOutLead is the lead column of a cluster-set view. With no column list the
// set view still leads with KEY, so the default stays unambiguous; once the
// operator names a list, the list alone decides whether KEY prints.
func fanOutLead(explicit bool) []sessionColumn {
	if explicit {
		return nil
	}
	return []sessionColumn{fanOutKeyColumn()}
}
