// Package barrier lets a test hold a process at a named point, so a kill drill
// can wait until the process has really reached a boundary and only then hard-kill it.
//
// In a normal build [Hit] does nothing at all. A binary built with -tags barriers
// reads STAIRCASE_BARRIER (name, or name@n for the n-th time it is reached) and
// STAIRCASE_BARRIER_DIR; when the point is reached it writes <dir>/<name>.reached
// and then blocks until it is killed.
package barrier

// Points, in the order a run reaches them. A drill kills at one and checks what a fresh process finds.
const (
	DecisionMade      = "decision-made"       // a proposal has been decided; nothing of it is journaled or audited yet
	JournalSynced     = "journal-synced"      // an approval is in the journal, flushed; not yet on the audit chain
	AuditCommitted    = "audit-committed"     // the decision is on the audit chain; the agent has not been answered
	Consumed          = "consumed"            // the approval is recorded in memory and released to the agent
	CommitPrepared    = "commit-prepared"     // the commit exists and is named on the chain; no branch holds it yet
	GitCAS            = "git-cas"             // the branch moved to the commit; no evidence yet
	EvidencePublished = "evidence-published"  // ledger and certificate written; the run record not yet completed
	DBCompleted       = "db-completed"        // the run record says it finished
	RecoverCommitted  = "recover-committed"   // recovery made its commit; its evidence is not yet written
	RecoverOpRecorded = "recover-op-recorded" // recovery wrote its operation record, before the commit
)
