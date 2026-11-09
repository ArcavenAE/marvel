package api

// FallbackEnv is the environment variable that marks a launch as a fallback:
// the daemon that could not start its successor, or whose successor could not
// start, execs the build it kept and names that build's path here. A launch
// that finds it set has already fallen back once and does not fall back again.
// It is a marker in the daemon's own environment and never reaches a seat.
//
// Owner reading, for review before merge: the name and the value (the kept
// path) are the builder's proposal, ruled in as written by the director.
const FallbackEnv = "MARVEL_UPGRADE_FALLBACK"
