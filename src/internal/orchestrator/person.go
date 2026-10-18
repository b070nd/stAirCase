package orchestrator

import (
	"context"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/tui"
)

// person is how a run puts a proposal to someone and gets their answer, until wait ends. signText is what a signed decision
// signs (decisionSigning). An answer that comes after wait ended counts for nothing; the run checks that, not the adapter.
type person interface {
	ask(wait context.Context, req domain.YieldRequest, signText string) domain.YieldResponse
}

// The three ways a run reaches a person: the local approval API (a page, another terminal, a script), the project's webhook, or
// the terminal the run was started in.
type (
	approvalAPI struct{ srv *approvalhttp.Server }
	webhook     struct {
		url    string
		secret []byte // the project's HMAC secret: its answers are verified with it
	}
	terminal struct{}
)

func (p approvalAPI) ask(wait context.Context, req domain.YieldRequest, signText string) domain.YieldResponse {
	id, ch := p.srv.PendSigned(req, signText)
	select {
	case resp := <-ch:
		return resp
	case <-wait.Done(): // withdrawn: a late answer is refused (409)
		p.srv.Withdraw(id)
		return domain.Decide(false, "the run ended while this waited for a decision")
	}
}

func (p webhook) ask(wait context.Context, req domain.YieldRequest, _ string) domain.YieldResponse {
	return sendWebhookYield(wait, p.url, p.secret, req)
}

func (terminal) ask(wait context.Context, req domain.YieldRequest, _ string) domain.YieldResponse {
	return tui.RunYieldTUI(wait, req) // ends with the run, or on Ctrl-C
}

// personFor is the way this run reaches a person: the approval API when it serves one, else the project's webhook, else the terminal.
func personFor(srv *approvalhttp.Server, webhookURL string, secret []byte) person {
	switch {
	case srv != nil:
		return approvalAPI{srv}
	case webhookURL != "":
		return webhook{webhookURL, secret}
	default:
		return terminal{}
	}
}
