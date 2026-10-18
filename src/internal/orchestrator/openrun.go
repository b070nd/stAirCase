package orchestrator

import (
	"errors"
	"fmt"
	"os"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
)

// openedRun is an existing run read from its records: what it is, what its verified chain says, and what it approved, with each
// approval's request from the journal. Opening reads and changes nothing, and touches no repository; what a caller requires of the
// run (its status, its owner lock, a recovery under way) is the caller's to check.
type openedRun struct {
	run       *domain.Run
	cs        *domain.Case
	project   *domain.Project
	hist      history.History
	journaled bool             // the run has an approval journal; without one, nothing was approved
	kept      []LedgerProposal // the approvals, in chain order, with their requests
}

// openRun reads run: its case and project, its history from the verified chain, and its approvals reconciled with the journal.
func (r *Runner) openRun(run *domain.Run) (*openedRun, error) {
	o := &openedRun{run: run}
	var err error
	if o.cs, err = r.store.GetCase(run.CaseID); err != nil || o.cs == nil {
		return nil, fmt.Errorf("case of run #%d: %w", run.ID, err)
	}
	if o.project, err = r.store.GetProject(o.cs.ProjectID); err != nil || o.project == nil {
		return nil, fmt.Errorf("project of run #%d: %w", run.ID, err)
	}
	if o.hist, err = history.Read(r.store, run.ID); err != nil {
		return nil, err
	}
	journal, err := readJournalFile(r.wsDir, run.ID)
	switch {
	case errors.Is(err, os.ErrNotExist): // nothing was approved: a run that only asked, or never got that far
	case err != nil:
		return nil, fmt.Errorf("the approval journal of run #%d cannot be trusted: %w", run.ID, err)
	default:
		o.journaled = true
	}
	if o.kept, err = reconcile(o.hist, journal); err != nil {
		return nil, fmt.Errorf("run #%d: %w", run.ID, err)
	}
	return o, nil
}

// repo is the run's repository.
func (o *openedRun) repo() (*GitRepo, error) { return OpenGitRepo(o.project.SourcePath) }

// derive is the run's approved state, derived again in an empty directory from the base commit in repo's objects, proposal by
// proposal, each checked against the digest the chain recorded (deriveApproved). cleanup removes the directory.
func (o *openedRun) derive(repo *GitRepo) (*approvals, func(), error) {
	return deriveApproved(repo, o.hist.Bound.BaseSHA, o.kept, o.hist.Approvals())
}
