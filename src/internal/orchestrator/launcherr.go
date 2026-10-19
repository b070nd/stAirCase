package orchestrator

import "github.com/b070nd/stAirCase/src/internal/history"

// LaunchError is an agent's own account of why its launch failed, with the lifecycle class the chain records for it (V11-C01
// scope 8). An agent that returns a plain error ended with a host exit; one that was refused, or never ran governed, returns
// Rejected. The error reaches the caller unchanged: the class only decides what launch_ended says.
type LaunchError struct {
	Class string // one of the history.Class constants a failed launch can have
	Err   error
}

func (e *LaunchError) Error() string { return e.Err.Error() }
func (e *LaunchError) Unwrap() error { return e.Err }

// Rejected marks err as a launch the agent refused or could not start governed: it did not start, or its hooks never arrived.
func Rejected(err error) error {
	if err == nil {
		return nil
	}
	return &LaunchError{Class: history.ClassRejected, Err: err}
}
