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
	if hasFlag(re) {
		return nil, fmt.Errorf("%q sets a flag; the class must hold without one", pattern)
	}
	seg := "(?:" + part.String() + ")"
	if regexp.MustCompile("^" + seg + "$").MatchString("") {
		return nil, fmt.Errorf("%q matches an empty part, so it would accept agent:///", pattern)
	}
	return regexp.Compile("^agent://" + seg + "/" + seg + "$")
}

// refusedFlags change what a part admits or how it repeats: case folding
// adds upper case and two non-ASCII letters, DotNL lets a dot match a newline,
// and NonGreedy makes repeats lazy.
const refusedFlags = syntax.FoldCase | syntax.DotNL | syntax.NonGreedy

// hasFlag reports whether any node of re was parsed under one of
// refusedFlags. Multi-line mode needs no check here: it turns the outer ^ and
// $ into line anchors, which the anchor checks refuse.
func hasFlag(re *syntax.Regexp) bool {
	if re.Flags&refusedFlags != 0 {
		return true
	}
	for _, sub := range re.Sub {
		if hasFlag(sub) {
			return true
		}
	}
	return false
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
