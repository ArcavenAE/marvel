package runtime

// Build-phase refusal. This file is a stopgap and goes away whole with the change
// that lands the watchdog wrapper (v1.1 C3); it is not one of the seven posture
// faults.
//
// Until the wrapper exists a headless goose run has no bound. goose streams
// without limit (v1.1 L4, one run streamed for 27m49s), and a run seen
// only through its exit status can report refused work as done: a refused shell
// call exited 0 and the model said it ran (v1.1 L6, rule C4). So every headless
// goose launch gets the exit-78 stub with this slug, goose never starts, and
// interactive goose is unaffected.
//
// The stream parser (v1 ticket B) has landed; what still holds this refusal in
// place is the watchdog wrapper (C3), since a headless run has no bound until it
// exists. To remove it: delete this file, the headlessBuilt field of Goose and
// its check in Prepare, and the tests named TestGooseBuildPhase*.
//
// A headless role with an empty prompt no longer depends on this file: Prepare
// refuses it with its own slug, gooseRefuseNoPrompt, before this check
// (ruling I-51:goose-empty-prompt-durable), and apply refuses it earlier still.

// gooseRefuseStreamNotBuilt is the slug of the build-phase refusal.
const gooseRefuseStreamNotBuilt = "stream-not-built"

// gooseBuildPhaseRefusal returns the build-phase reason for a launch, or "" when
// the launch may go on to the posture rules. headlessBuilt is the Goose field a
// test sets to reach the headless launch line; nothing else sets it.
func gooseBuildPhaseRefusal(headless, headlessBuilt bool) string {
	if headless && !headlessBuilt {
		return gooseRefuseStreamNotBuilt
	}
	return ""
}
