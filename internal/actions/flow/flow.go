package flow

// Stub package for future water flow sensor support.
// Will provide a "flow.detected" trigger for the override system.
//
// Planned implementation:
// - GPIO interrupt-based pulse counting (e.g., YF-S201 hall-effect sensor)
// - Exposes a channel or callback when flow is detected
// - Used by the override engine to temporarily re-enable loads during curtailment
