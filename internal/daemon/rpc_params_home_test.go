package daemon

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// The request types the daemon decodes live in internal/daemonrpc, exported, so
// a client and the daemon share one definition of each payload and not two
// hand-kept copies. Package daemon declares none of its own: the one name that
// ends in Params here is heartbeatParams, an alias of api.HeartbeatRequest,
// which stays in api (I-458 row 6).
func TestRequestTypesAreDeclaredInDaemonrpcNotInTheDaemon(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	var own []string
	p.each(func(file string, n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			return true
		}
		for _, s := range gd.Specs {
			ts := s.(*ast.TypeSpec)
			if !strings.HasSuffix(ts.Name.Name, "Params") {
				continue
			}
			if ts.Assign.IsValid() && ts.Name.Name == "heartbeatParams" {
				continue
			}
			own = append(own, file+": "+ts.Name.Name)
		}
		return true
	})
	sort.Strings(own)
	if len(own) > 0 {
		t.Errorf("package daemon declares %d request types that belong in daemonrpc: %v", len(own), own)
	}
}
