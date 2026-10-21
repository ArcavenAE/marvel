package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"regexp/syntax"

	"github.com/arcavenae/marvel/contracts/go/envelopeschema"
)

// agentAddress is agent://{team}/{id}, each part in the envelope schema's
// agent_id character class. The class is read from the embedded schema, not
// copied, so marvel's matcher and director's envelope cannot drift apart: no
// wildcard, separator or upper case reaches a seat address. It reads the
// schema's bytes from the envelopeschema leaf, so internal/api does not
// compile the schema.
var agentAddress = mustAgentAddress()

func mustAgentAddress() *regexp.Regexp {
	var doc struct {
		Properties struct {
			Sender struct {
				Properties struct {
					AgentID struct {
						Pattern string `json:"pattern"`
					} `json:"agent_id"`
				} `json:"properties"`
			} `json:"sender"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(envelopeschema.JSON, &doc); err != nil {
		panic(fmt.Sprintf("api: the envelope schema is not JSON: %v", err))
	}
	re, err := agentAddressFrom(doc.Properties.Sender.Properties.AgentID.Pattern)
	if err != nil {
		panic(fmt.Sprintf("api: the envelope schema's agent_id pattern: %v", err))
	}
	return re
}

// agentAddressFrom builds the agent://{team}/{id} matcher from the agent_id
// pattern, using the pattern between its anchors as each part. The pattern is
// parsed, not cut as text, and must be ^, one expression with no anchor of
// its own, then $; the part is grouped before it is spliced. Anything else
// (an alternation of anchored branches, an inner anchor) would splice into an
// expression that matches other addresses, so it is refused.
func agentAddressFrom(pattern string) (*regexp.Regexp, error) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	n := len(re.Sub)
	if re.Op != syntax.OpConcat || n < 3 || re.Sub[0].Op != syntax.OpBeginText || re.Sub[n-1].Op != syntax.OpEndText {
		return nil, fmt.Errorf("%q is not ^, one expression, then $", pattern)
	}
	part := &syntax.Regexp{Op: syntax.OpConcat, Flags: re.Flags, Sub: re.Sub[1 : n-1]}
	if hasAnchor(part) {
		return nil, fmt.Errorf("%q has an anchor between its outer ^ and $", pattern)
	}
	seg := "(?:" + part.String() + ")"
	return regexp.Compile("^agent://" + seg + "/" + seg + "$")
}

// hasAnchor reports whether re holds a line or text anchor anywhere.
func hasAnchor(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText:
		return true
	}
	for _, sub := range re.Sub {
		if hasAnchor(sub) {
			return true
		}
	}
	return false
}
