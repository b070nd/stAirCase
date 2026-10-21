package orchestrator

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
)

// Delivery by policy (ADR 0012): an away run delivers its approved change without a person only when seven conditions hold. They are
// judged from the run's verified history and the terms its chain binds, never from live state alone, and anything doubtful sends the
// run to its owner's review, naming the condition. A condition that cannot be read is a condition that fails.

// deliveryCondition is one condition: what it is called in a refusal, and the check that returns why it does not hold, or "".
type deliveryCondition struct {
	name  string
	check func(deliveryCase) string
}

// deliveryConditions are the seven conditions, in the order they are judged; the first to fail is the one a refusal names.
var deliveryConditions = [7]deliveryCondition{
	{"a signed policy", deliveryCase.signedPolicy},
	{"only rules of your policy approved", deliveryCase.onlyRules},
	{"nothing was parked", deliveryCase.nothingParked},
	{"inside the delivery scope, clear of the sensitive and protected paths", deliveryCase.insideBars},
	{"the checks passed on exactly the approved tree", deliveryCase.checkedTree},
	{"the protected paths are declared", deliveryCase.protectedDeclared},
	{"the initiator signed", deliveryCase.initiatorSigned},
}

// deliveryCase is what the conditions are judged on: the run's verified history, the terms its chain binds, and the state the run
// has now (the approved tree, the digest of the approved files, what it counts parked, whether something was approved on the task or
// left unreviewed by a reviewer model, whether the run branch is still at the base).
type deliveryCase struct {
	terms                    Terms
	hist                     history.History
	tree                     string
	files                    map[string]string // path -> digest of the approved content, as the run holds it
	parked                   int
	taskApproved, unreviewed bool
	atBase                   bool
	unchanged                bool // the approved files leave the base tree as it was: there is nothing to commit
}

// refusal is why the change is not delivered without a person, naming the first condition that does not hold; "" when all seven do.
func (c deliveryCase) refusal() string {
	for i, cond := range deliveryConditions {
		if why := cond.check(c); why != "" {
			return fmt.Sprintf("condition %d (%s): %s", i+1, cond.name, why)
		}
	}
	return ""
}

// deliveryAttempt is what a run that delivers by policy tells awaitReview about itself: the live facts the conditions add to the
// verified history.
type deliveryAttempt struct {
	taskApproved, unreviewed bool        // something was approved as the task or left unreviewed by a reviewer model
	atBase                   func() bool // the run branch still points at the base
}

// judgeDelivery judges the seven conditions for run runID, whose phase_checked entry for tree was just written: from its verified
// history, the terms its chain binds (saved options checked against the digest on run_bound) and the state it holds. It returns the
// authorization to write when every condition holds, or why the owner decides.
func (r *Runner) judgeDelivery(runID int64, appr *approvals, tree string, parked int, at *deliveryAttempt) (history.DeliveryAuthorized, string) {
	h, err := history.Read(r.store, runID)
	if err != nil {
		return history.DeliveryAuthorized{}, fmt.Sprintf("condition 1 (%s): the audit history cannot be read, so nothing in it is believed: %v", deliveryConditions[0].name, err)
	}
	terms, err := loadTerms(r.wsDir, runID, h, h.Bound.Harness)
	if err != nil {
		return history.DeliveryAuthorized{}, fmt.Sprintf("condition 1 (%s): the terms the run was bound to cannot be confirmed: %v", deliveryConditions[0].name, err)
	}
	c := deliveryCase{terms: terms, hist: h, tree: tree, files: digest(appr.files), parked: parked,
		taskApproved: at.taskApproved, unreviewed: at.unreviewed, atBase: at.atBase(), unchanged: tree == appr.base.TreeHash.String()}
	if why := c.refusal(); why != "" {
		return history.DeliveryAuthorized{}, why
	}
	entry, _, _ := h.LastChecked()
	return history.DeliveryAuthorized{PhaseChecked: entry, Tree: tree, Files: c.files, TermsSHA256: h.Bound.TermsSHA256, Policy: h.Policy.Digest,
		Mode: history.ModePolicy}, ""
}

// 1: the bound terms say policy and the run's policy snapshot was signed.
func (c deliveryCase) signedPolicy() string {
	switch {
	case c.terms.Deliver != DeliverPolicy:
		return fmt.Sprintf("the terms bound to the run say %s delivery, not policy", orDefault(c.terms.Deliver, DeliverReview))
	case c.hist.Bound.TermsSHA256 == "":
		return "the run's terms are not bound to its audit chain by digest"
	case c.hist.Policy == nil || !c.hist.Policy.Signed:
		return "the policy the run decided under was not signed (staircase policy sign)"
	}
	return ""
}

// 2: every file approval has the source policy; the source declared is allowed for commands and tools only, which carry no files;
// no operator, task, evidence or reviewer-model approval exists.
func (c deliveryCase) onlyRules() string {
	for _, d := range c.hist.Decisions {
		if !d.Approved {
			continue
		}
		switch {
		case d.Source == "policy":
		case d.Source == "declared" && (d.ActionType == domain.ActionShellExec || d.ActionType == domain.ActionToolCall):
		default:
			return fmt.Sprintf("proposal %d (%s) was approved by %q, not by a rule of your policy", d.Seq, d.ActionType, d.Source)
		}
	}
	if c.taskApproved {
		return "something was approved as part of the agreed task or on evidence, not by a rule of your policy"
	}
	if c.unreviewed {
		return "a reviewer model approved something that no rule of your policy approved"
	}
	if len(c.hist.Approvals()) == 0 || len(c.files) == 0 {
		return "nothing was approved by a rule of your policy"
	}
	return ""
}

// 3: nothing was parked. One parked proposal sends the whole run to the owner: it may be what the approved part depends on.
func (c deliveryCase) nothingParked() string {
	if n := max(c.parked, len(c.hist.Parked())); n > 0 {
		return fmt.Sprintf("%d proposal(s) were parked for you, and what is approved may depend on them", n)
	}
	return ""
}

// 4: every approved path is inside the delivery scope and matches neither the sensitive list nor the run's protected paths. The
// paths are those the chain records for the approvals and those of the approved tree; a path in either counts.
func (c deliveryCase) insideBars() string {
	paths := map[string]bool{}
	for _, d := range c.hist.Approvals() {
		if len(d.Paths) == 0 {
			return fmt.Sprintf("proposal %d approved a file change but the chain records no path for it", d.Seq)
		}
		for _, p := range d.Paths {
			paths[p] = true
		}
	}
	for p := range c.files {
		paths[p] = true
	}
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		if !matchesFold(c.terms.DeliverPaths, p) {
			return fmt.Sprintf("%s is outside the delivery scope (%s)", p, strings.Join(c.terms.DeliverPaths, ", "))
		}
		if bar := c.terms.PersonBar([]string{p}); bar != "" { // the deciders' own wording: the bars are repeated, not restated
			return bar + " (a person's to decide, so never delivered by policy)"
		}
	}
	return ""
}

// 5: the checks the terms name passed on exactly the approved tree, the approved bytes are the ones the chain records, and the run
// branch is still at the base.
func (c deliveryCase) checkedTree() string {
	_, checked, ok := c.hist.LastChecked()
	switch {
	case !ok:
		return "no checks of the approved tree are on the chain"
	case checked.Tree != c.tree:
		return fmt.Sprintf("the checks ran on tree %.12s, not on the approved tree %.12s", checked.Tree, c.tree)
	case !checked.Passed:
		return "the checks did not pass on the approved tree: " + checked.Failed
	case len(checked.Checks) == 0 || len(checked.Checks) != len(c.terms.Checks):
		return "the checks that ran are not the checks the terms name"
	}
	for i, ch := range checked.Checks {
		if ch.Command != c.terms.Checks[i] || ch.ExitCode != 0 {
			return fmt.Sprintf("the check %q did not pass as the terms name it", c.terms.Checks[i])
		}
	}
	recorded := map[string]string{}
	for _, d := range c.hist.Approvals() {
		maps.Copy(recorded, d.Files)
	}
	for _, p := range slices.Sorted(maps.Keys(c.files)) {
		if recorded[p] != c.files[p] {
			return fmt.Sprintf("the approved bytes of %s are not the ones the audit chain records", p)
		}
	}
	if c.unchanged {
		return "the approved files leave the tree as it was: there is nothing to commit"
	}
	if !c.atBase {
		return "the run branch no longer points at the commit the run started from"
	}
	return ""
}

// 6: the protected-paths declaration is complete: the checks name a file of their own or the owner named protected paths.
func (c deliveryCase) protectedDeclared() string {
	if len(c.terms.ProtectedPaths) == 0 {
		return "no path that holds your checks' definition is protected, so an agent could weaken the final check under a rule"
	}
	return ""
}

// 7: the initiator signed, so the certificate names who armed unattended delivery.
func (c deliveryCase) initiatorSigned() string {
	if c.hist.Initiator == nil {
		return "the person who started the run did not sign it, so nobody is named as having armed delivery without review"
	}
	return ""
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
