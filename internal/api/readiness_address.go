package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/arcavenae/marvel/contracts/go/envelope"
)

// agentAddress is agent://{team}/{id}, each part in the envelope schema's
// agent_id character class. The class is read from the embedded schema, not
// copied, so marvel's matcher and director's envelope cannot drift apart: no
// wildcard, separator or upper case reaches a seat address.
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
	if err := json.Unmarshal(envelope.SchemaJSON, &doc); err != nil {
		panic(fmt.Sprintf("api: the envelope schema is not JSON: %v", err))
	}
	seg, ok := strings.CutPrefix(doc.Properties.Sender.Properties.AgentID.Pattern, "^")
	if ok {
		seg, ok = strings.CutSuffix(seg, "$")
	}
	if !ok || seg == "" {
		panic("api: the envelope schema's agent_id pattern is not an anchored class")
	}
	return regexp.MustCompile("^agent://" + seg + "/" + seg + "$")
}
