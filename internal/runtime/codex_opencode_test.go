package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// headlessContext builds the shape the manager hands a stream-capable
// launch: a headless runtime plus the sink path.
func headlessContext(name, command, streamPath string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{
		Name:    name,
		Command: command,
		Mode:    api.RuntimeModeHeadless,
		Prompt:  "say ok",
	}
	ctx.StreamPath = streamPath
	return ctx
}

func TestRegistryResolvesCodexAndOpenCode(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if got := r.Resolve("codex").Name(); got != "codex" {
		t.Errorf("Resolve(codex) = %q, want codex", got)
	}
	if got := r.Resolve("opencode").Name(); got != "opencode" {
		t.Errorf("Resolve(opencode) = %q, want opencode", got)
	}
}

func TestCodexSupportsStreamOnlyHeadless(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	interactive := testContext()
	interactive.Session.Runtime.Name = "codex"
	interactive.Session.Runtime.Command = "codex"
	if c.SupportsStream(interactive) {
		t.Error("interactive codex should not claim a stream")
	}
	if !c.SupportsStream(headlessContext("codex", "codex", "")) {
		t.Error("headless codex should claim a stream")
	}
}

func TestCodexPrepareHeadlessRedirectsToSink(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	result, err := c.Prepare(headlessContext("codex", "codex", "/tmp/streams/cx.jsonl"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, want := range []string{
		"codex exec --json", "--skip-git-repo-check",
		"< /dev/null", "> /tmp/streams/cx.jsonl",
	} {
		if !strings.Contains(result.Command, want) {
			t.Errorf("command missing %q: %s", want, result.Command)
		}
	}
	// The prompt is positional and must precede the redirections.
	if !strings.Contains(result.Command, "'say ok' < /dev/null > ") {
		t.Errorf("prompt not positioned before the redirects: %s", result.Command)
	}
	if result.Stream == nil || result.Stream.Format != StreamFormatCodexJSON {
		t.Errorf("stream spec = %+v, want format %q", result.Stream, StreamFormatCodexJSON)
	}
}

func TestCodexPrepareHeadlessWithoutSinkStillClosesStdin(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	result, err := c.Prepare(headlessContext("codex", "codex", ""))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// Even unobserved, stdin must be closed or codex hangs on the pane tty.
	if !strings.Contains(result.Command, "< /dev/null") {
		t.Errorf("headless codex must close stdin: %s", result.Command)
	}
	if strings.Contains(result.Command, ">") {
		t.Errorf("no sink offered, so no stdout redirect belongs: %s", result.Command)
	}
	if result.Stream != nil {
		t.Error("adapter reported a stream it was never given a sink for")
	}
}

func TestCodexPrepareHeadlessRequiresPrompt(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	ctx := headlessContext("codex", "codex", "/tmp/x.jsonl")
	ctx.Session.Runtime.Prompt = ""
	if _, err := c.Prepare(ctx); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("error = %v, want ErrNoPrompt", err)
	}
}

func TestCodexPrepareInteractiveIsPlain(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{Name: "codex", Command: "codex", Args: []string{"--model", "gpt-5"}}
	ctx.StreamPath = "/tmp/should-be-ignored.jsonl"
	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if result.Command != "codex --model gpt-5" {
		t.Errorf("interactive command = %q, want plain codex invocation", result.Command)
	}
	if result.Stream != nil {
		t.Error("interactive codex reported a stream")
	}
}

func TestOpenCodeSupportsStreamOnlyHeadless(t *testing.T) {
	t.Parallel()
	o := &OpenCode{}
	interactive := testContext()
	interactive.Session.Runtime.Name = "opencode"
	interactive.Session.Runtime.Command = "opencode"
	if o.SupportsStream(interactive) {
		t.Error("interactive opencode should not claim a stream")
	}
	if !o.SupportsStream(headlessContext("opencode", "opencode", "")) {
		t.Error("headless opencode should claim a stream")
	}
}

func TestOpenCodePrepareHeadlessRedirectsToSink(t *testing.T) {
	t.Parallel()
	o := &OpenCode{}
	result, err := o.Prepare(headlessContext("opencode", "opencode", "/tmp/streams/oc.json"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, want := range []string{
		"opencode run --format json",
		"< /dev/null", "> /tmp/streams/oc.json",
	} {
		if !strings.Contains(result.Command, want) {
			t.Errorf("command missing %q: %s", want, result.Command)
		}
	}
	if !strings.Contains(result.Command, "'say ok' < /dev/null > ") {
		t.Errorf("prompt not positioned before the redirects: %s", result.Command)
	}
	if result.Stream == nil || result.Stream.Format != StreamFormatOpenCodeJSON {
		t.Errorf("stream spec = %+v, want format %q", result.Stream, StreamFormatOpenCodeJSON)
	}
}

func TestOpenCodePrepareHeadlessRequiresPrompt(t *testing.T) {
	t.Parallel()
	o := &OpenCode{}
	ctx := headlessContext("opencode", "opencode", "/tmp/x.json")
	ctx.Session.Runtime.Prompt = ""
	if _, err := o.Prepare(ctx); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("error = %v, want ErrNoPrompt", err)
	}
}

func TestOpenCodePrepareInteractiveIsPlain(t *testing.T) {
	t.Parallel()
	o := &OpenCode{}
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{Name: "opencode", Command: "opencode"}
	result, err := o.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if result.Command != "opencode" {
		t.Errorf("interactive command = %q, want plain opencode", result.Command)
	}
	if result.Stream != nil {
		t.Error("interactive opencode reported a stream")
	}
}

func TestNewStreamParserResolvesCodexAndOpenCode(t *testing.T) {
	t.Parallel()
	for _, format := range []StreamFormat{StreamFormatCodexJSON, StreamFormatOpenCodeJSON} {
		p, err := NewStreamParser(format, StreamParserConfig{AgentID: "a"})
		if err != nil {
			t.Errorf("NewStreamParser(%q): %v", format, err)
		}
		if p == nil {
			t.Errorf("NewStreamParser(%q): nil parser", format)
		}
	}
}

func TestCodexAndOpenCodeNoCommandError(t *testing.T) {
	t.Parallel()
	for _, a := range []Adapter{&Codex{}, &OpenCode{}} {
		ctx := headlessContext(a.Name(), "", "")
		ctx.Session.Runtime.Name = ""
		if _, err := a.Prepare(ctx); !errors.Is(err, ErrNoCommand) {
			t.Errorf("%s.Prepare with no command: err = %v, want ErrNoCommand", a.Name(), err)
		}
	}
}
