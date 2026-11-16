package daemon

// servedMethods is every method name dispatchUntimed's switch serves. dispatchAs
// times a call under its own name only when the daemon serves it, so a name a
// client invents, which can be refused before the switch is reached, never mints
// a timing site of its own. TestServedMethodsAreTheSwitchCases keeps it equal to
// the switch.
var servedMethods = map[string]bool{}
