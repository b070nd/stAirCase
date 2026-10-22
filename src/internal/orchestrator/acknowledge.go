package orchestrator

import (
	"errors"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/history"
)

// ErrNoLostLaunch is the refusal of an acknowledgement when the run has no launch whose end is open or was never established:
// there is nothing for a person to have looked at. Nothing was written.
var ErrNoLostLaunch = errors.New("the run has no open or lost launch to acknowledge")

// AckResult says what Acknowledge did.
type AckResult struct {
	Attempt      int  // the launch acknowledged
	EndRecorded  bool // the launch was still open, and this call wrote its end (unknown)
	Acknowledged bool // this call wrote the acknowledgement; false when the launch was acknowledged already
}

// Acknowledge records a person's acknowledgement of the run's open or lost launch, without launching anything (ADR 0005,
// amended): it attests that the person looked at what the agent may have done. Under the run's owner lock it writes the launch's
// end (unknown) if the launch is still open, then launch_acknowledged naming the attempt and its generation. A launch already
// acknowledged is left alone. A run with no open or lost launch is refused (ErrNoLostLaunch), and so is one whose launches were
// lost MaxLostLaunches times: acknowledging never lifts that bound (the end of an open launch is still recorded). A resume
// treats the acknowledged launch as acknowledged; a hosted resume never acknowledges by itself.
func (r *Runner) Acknowledge(runID int64) (AckResult, error) {
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return AckResult{}, fmt.Errorf("run #%d not found", runID)
	}
	release, err := claimRun(r.wsDir, runID)
	if err != nil {
		return AckResult{}, fmt.Errorf("run #%d is still running: its process holds the run's lock, so its launch is not lost: %w", runID, err)
	}
	defer release()
	h, err := history.Read(r.store, runID)
	if err != nil {
		return AckResult{}, err
	}
	if h.Sealed != "" {
		return AckResult{}, fmt.Errorf("%w: run #%d already made its commit (%s is on its chain)", ErrNoLostLaunch, runID, h.Sealed)
	}
	if n := len(h.Launches); n == 0 || (h.OpenLaunch() == nil && !h.Launches[n-1].Lost()) {
		return AckResult{}, fmt.Errorf("%w: the last launch of run #%d ended in a way that is established, or the run launched nothing", ErrNoLostLaunch, runID)
	}
	res := AckResult{Attempt: h.Launches[len(h.Launches)-1].Attempt}
	h, res.EndRecorded, err = r.recordLostLaunchEnd(runID, h)
	if err != nil {
		return res, err
	}
	if lost := h.Lost(); lost >= history.MaxLostLaunches {
		err = fmt.Errorf("%w: %s", ErrLaunchLost, lostLimitText(runID, lost))
		if res.EndRecorded {
			err = launchRecorded{err}
		}
		return res, err
	}
	if h.LaunchParked() == 0 {
		return res, nil // acknowledged already: nothing to do
	}
	if _, err := r.writeAcknowledgement(runID, h); err != nil {
		return res, err
	}
	res.Acknowledged = true
	return res, nil
}
