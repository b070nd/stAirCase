# Hosted-runner cost study (US-029)

A desk study of what it would cost for stAirCase to run disposable runners in the EU, so that users could skip connecting their own.
It answers PROTOCOL.md US-029 under the core 1.0 PRD. The beta still starts with **customer-owned runners**, and this study does not change
that.

**How it was made.** The prices come from each provider's own pricing and region pages, read on **2026-10-18** (the [sources](#sources)
list every URL). Nothing was signed up for, provisioned or paid. No runner was started, so every time, size and token count below is an
**assumption**, written down so that the next experiment can replace it with a measurement. Prices change. Read them again before acting
on them.

## In short

- **Infrastructure is cheap; the model costs most.** A 20-minute run on 2 vCPU and 4 GB costs **$0.014 to $0.053** of compute on the
  serverless and microVM candidates and **EUR 0.032** on a Hetzner server deleted after the run. On stAirCase's default model, the
  same run's model charges are **$1.94 to $4.95** (assumed tokens, below). Infrastructure comes to about 1% to 3% of a run.
- **Monthly infrastructure** at 200, 2,000 and 20,000 runs comes to roughly **$3 to $8**, **$29 to $112** and **$290 to $1,170**,
  depending on the provider (GitHub's larger runners cost $24, $240 and $2,400). On the customer's key, model charges for the same loads
  would be roughly $390 to $990, $3,900 to $9,900 and $39,000 to $99,000.
- **Cost will not decide this.** Isolation will, along with whether stAirCase's own command sandbox works inside the runner, and who holds
  the model key and the signing key. FR-6 keeps both keys customer-side today, so a hosted runner that holds them is a decision for the
  owner, not a pricing question.
- **Next experiment:** run the same replayed run on **Fly.io Machines in Frankfurt** (the main candidate) and on a **Hetzner Cloud server
  in Nuremberg** (the EU-company comparison), 30 disposable runs each, with no model charges (`--replay-llm`). Hard spending caps of
  **about $36 in all** (details [below](#recommendation-the-next-experiment-and-its-budget)).

## What a hosted runner would have to be

These requirements come from the PRD's privacy and operations defaults, the "Supabase Free and EU beta obligations" research and
[the safety boundary](../safety.md):

- **Executes in the EU.** The PRD promises EU storage and execution, plus international processing documented under the law. It does not
  promise that all processing stays in the EU. A provider's EU region satisfies "executes in the EU". The provider's own control plane,
  logs and support still need the same transfer inventory that the research section requires for Supabase.
- **Disposable.** Each run gets a fresh machine that is destroyed afterwards. No state carries over from one customer's run to the next,
  so a warm pool may hold only fresh, unused machines.
- **Isolated by a kernel boundary, not just a container.** Runs from different customers must not share a kernel. This is the
  "Contained" profile in [the safety boundary](../safety.md#confidentiality-pick-a-profile): stAirCase inside a VM, egress limited to
  the model provider (and, for a hosted runner, the Git host).
- **Room for stAirCase's own sandbox.** `--sandbox required` needs Landlock ABI 3 or later (Linux 6.2) or bubblewrap, which relies on Linux
  namespaces. The docker sandbox needs a container runtime. A platform that removes these still runs stAirCase, but agent commands then
  run unsandboxed inside the runner.
- **Keys.** The model key and the private signing key stay customer-side today (FR-6). A hosted runner must either receive them for one
  run, which changes FR-6, or work without them. See [what this does not decide](#what-this-study-does-not-decide).

## Candidates, and why these

| Provider and product | EU locations used | Isolation per run (as the provider states it) | Billing unit | Verdict |
|---|---|---|---|---|
| Fly.io Machines | `fra` Frankfurt (also `ams`, `arn`, `cdg`) | Firecracker microVM, "strong hardware virtualization" | per second | **modelled, recommended to test** |
| Hetzner Cloud (Regular Performance, CPX) | `NBG1` Nuremberg, `HEL1` Helsinki | a virtual cloud server of its own | per started hour, monthly cap | **modelled, test as the EU-company comparison** |
| AWS Fargate (ECS) | `eu-central-1` Frankfurt | "isolated virtual environment" per task; no privileged containers, `CAP_SYS_ADMIN` restricted | per second, 1-minute minimum | modelled; inner sandbox doubtful |
| Google Cloud Run jobs | `europe-west4` Netherlands (Tier 1) | second-generation microVM, "full Linux compatibility" incl. namespaces and cgroups | per 100 ms, 1-minute minimum per instance | modelled; dearest serverless option, but a possible fallback |
| Scaleway Serverless Jobs | Paris | gVisor on shared hosts, all capabilities dropped, `/tmp` in memory | per resource-second (granularity not stated) | modelled for price only; sandbox unlikely to work |
| GitHub Actions larger runners | Azure `FranceCentral` or `SwedenCentral`, only with Azure private networking | a new VM per runner | per minute, rounded up per job | modelled for reference; a customer-owned path, not a hosted one |

Why each was kept or set aside:

- **Fly.io Machines** has a microVM with its own guest kernel, per-second billing, a REST API to create and delete Machines, and EU regions
  next to Supabase's Frankfurt. Fly.io is a US provider, so its transfers need documenting like the others.
- **Hetzner Cloud** is run by Hetzner Online GmbH in Gunzenhausen, Germany: an EU company, which keeps the transfer story shorter. Each run
  gets a server of its own. Two drawbacks: billing is per started hour (a 5-minute run costs a full hour), and creating a server has not been
  timed here. The Cost-Optimized (CX, CAX) plans were all marked "not available" on 2026-10-18, so the model uses
  Regular Performance CPX22. That plan is offered in `NBG1` and `HEL1`, not `FSN1`.
- **AWS Fargate** isolates each task, but its own security page rules out privileged containers ("Docker in Docker") and restricts
  `CAP_SYS_ADMIN`. The docker sandbox cannot run there. Bubblewrap probably cannot either, and neither it nor Landlock was tried there. It is also
  dearer per run than Fly.io performance-2x.
- **Google Cloud Run jobs** run only on the second-generation microVM, which the page describes as supporting all system calls, namespaces
  and cgroups, so stAirCase's sandbox may work. Per run it is the dearest of the four serverless options, and internet egress costs more
  than at AWS or Fly.io. Frankfurt (`europe-west3`) is a Tier 2 region; its price was not read, and Tier 2 is the dearer tier.
- **Scaleway Serverless Jobs** is cheap and run by a French company (Scaleway SAS, Paris). Its limits page says jobs run under gVisor with
  every capability dropped and `/tmp` in memory, so bubblewrap, Landlock and the docker sandbox are all unlikely to work, and the limits page
  caps a job at 10 GB of ephemeral storage. A Scaleway VM product was not examined.
- **GitHub Actions larger runners** are a fresh VM per job, but the job runs in a GitHub organization against repositories that
  organization can reach. For stAirCase that is the customer running stAirCase in their own CI, which is a form of the customer-owned
  model (the verification Action already works that way), not a runner the operator hosts. The EU location is available only through
  Azure private networking ("GitHub deploys your runners in the same Azure region as the subnet you connect them to"), which needs the
  customer's own Azure network, and those Azure costs were not read. Included minutes do not cover larger runners.
- **Not examined:** Azure Container Instances, OVHcloud, IONOS, Kubernetes clusters, and a self-run Firecracker on Hetzner dedicated
  servers. They were left out to keep the study to one source read per provider. None was ruled out on its merits, and any of them can
  be added later with the same method.

## Assumptions

| What | Assumed | Why, and what replaces it |
|---|---|---|
| Runner size | 2 vCPU, 4 GB RAM (a sensitivity case: 4 vCPU, 8 GB) | enough for the agent CLI plus `go test` or a similar check; the experiment measures peak memory |
| Billed time per run | 20 minutes: ~2 to boot and clone, ~15 for the agent, ~3 for checks (sensitivity: 45) | most agent time waits on the model, but the machine is billed while it waits; the experiment measures wall time |
| Disk | up to 20 GB ephemeral, deleted with the machine | the repository, the image and tool caches; all inside each provider's default or included disk |
| Persistent storage | none on the runner | evidence goes to the hosted app (Supabase), costed in the Supabase research, not here |
| Egress | 50 MB per run | model requests (the conversation is re-sent each call, so ~40 calls of ~160 KB), `git push`, evidence upload; ingress (clone, image pull) is free or not charged on every candidate read |
| Concurrency | peaks of 3, 10 and 50 runs at once for the three loads (averages are 0.1, 0.9 and 9) | cost is linear in run-minutes on every per-second provider, so concurrency matters for quotas and start latency, not for price |
| Loads | small beta 200 runs/month (10 users x 20); larger: 2,000 and 20,000 runs/month | the two larger loads are 10x and 100x the beta |
| Model | `claude-sonnet-4-6` (stAirCase's default when none is set, [models](../models.md)); 1.5 M input and 30 k output tokens per run | illustrative, not measured; the experiment's recorded run gives real counts |
| Currency | EUR prices converted at the ECB reference rate of 2026-10-16, 1 EUR = 1.1253 USD | Hetzner and Scaleway price in EUR, before VAT |
| Not costed | container registry, logs and monitoring, a static egress IP for allow-listing (Fly.io: $0.005/hour), the operator's time | small or optional at these loads; listed so they are not forgotten |

## Prices read (2026-10-18)

| Provider | Price used | Notes from the same page |
|---|---|---|
| Fly.io Machines | shared vCPU $0.0000008465/s; performance vCPU $0.000012732/s; RAM above the included amount $0.000002316/GB-s; `fra` multiplier 1.153846154 (`ams` and `arn` 1.038461538, `cdg` 1.134615385) | performance includes 2 GB per vCPU, shared 0.25 GB; e.g. performance-2x 4 GB $66.00 per 30 days at the base rate; egress $0.02/GB in Europe, inbound free; stopped Machines billed for rootfs only at $0.15/GB per 30 days; no general free tier for new organizations |
| Hetzner Cloud | CPX22 (2 AMD vCPU, 4 GB, 80 GB NVMe): EUR 0.0320/hour, EUR 19.99/month cap; CPX32 (4, 8 GB): EUR 0.0577/hour, EUR 35.99/month | prices include an IPv4 address, shown with VAT country "All others" (no VAT); 20 TB traffic included in the EU, then EUR 1.00 per TB; usage rounded up to the full hour; billed until deleted, even when off; snapshots EUR 0.0143/GB per month |
| AWS Fargate | Linux/x86, Europe (Frankfurt): $0.04656 per vCPU-hour, $0.00511 per GB-hour | per second, 1-minute minimum; 20 GB ephemeral storage included per task; public IPv4 $0.005/hour; internet egress from Frankfurt $0.09/GB for the first 10 TB after 100 GB free per month across the whole account |
| Google Cloud Run jobs | `europe-west4`: $0.000018 per vCPU-second, $0.000002 per GiB-second | jobs billed for each instance's lifetime, 1-minute minimum; free tier 240,000 vCPU-seconds and 450,000 GiB-seconds per month, applied at Tier 1 prices; Premium Tier internet egress $0.12/GiB after 1 GiB free (the table read was for the default region shown, `us-central1`; at these volumes the difference would not matter) |
| Scaleway Serverless Jobs | Paris: EUR 0.00001 per vCPU-s, EUR 0.000002 per GB-s | free tier 200,000 vCPU-s and 400,000 GB-s per account per month; before tax; egress not stated; limits page: up to 6 vCPU, 16 GB, 10 GB ephemeral, 24 hours |
| GitHub Actions | larger runner Linux 2-core x64 $0.006/min, 4-core $0.012/min (arm64: $0.005, $0.008) | minutes rounded up per job; "Included minutes cannot be used for larger runners" |
| Anthropic (model) | Claude Sonnet 4.6: $3 input, $15 output, $0.30 cache hit, $3.75 five-minute cache write, per million tokens | same as `src/internal/monitor/pricing.go` (read 2026-10-05); first-party inference geography is "global" (standard price) or "us" (1.1x); the page lists no EU-only option |

## Per-run cost (infrastructure only)

A 2 vCPU, 4 GB run billed for 20 minutes, without egress:

| Provider and shape | How it is computed | Per run |
|---|---|---|
| Fly.io `fra`, shared-cpu-2x, 4 GB | (2 x 0.0000008465 + 3.5 x 0.000002316) x 1.1538 x 1,200 s | **$0.0136** |
| Fly.io `fra`, performance-2x, 4 GB | 2 x 0.000012732 x 1.1538 x 1,200 s | **$0.0353** |
| Hetzner CPX22, created and deleted per run | one started hour | **EUR 0.0320** ($0.0360) |
| Scaleway Serverless Jobs, Paris | (2 x 0.00001 + 4 x 0.000002) x 1,200 s | **EUR 0.0336** ($0.0378) |
| AWS Fargate, Frankfurt | (2 x 0.04656 + 4 x 0.00511) / 3 + public IPv4 0.005 / 3 | **$0.0395** |
| Google Cloud Run job, `europe-west4` | (2 x 0.000018 + 4 x 0.000002) x 1,200 s | **$0.0528** |
| GitHub larger runner, 2-core | 20 x $0.006 | $0.1200 |

The shared Fly.io CPU is the cheapest, but it is a shared core that can be throttled under sustained load, which may slow builds and tests.
Performance-2x is the like-for-like row against Fargate and Cloud Run, which bill full vCPUs. Fly.io's `ams` or `arn` would be 10% cheaper
than `fra`.

Sensitivity, 4 vCPU and 8 GB billed for 45 minutes: Fly.io performance-4x $0.159 (shared-cpu-4x $0.061), Scaleway EUR 0.151, Fargate
$0.174, Cloud Run $0.238, Hetzner CPX32 EUR 0.0577 (still one started hour), GitHub 4-core $0.54. Per-second providers scale linearly
with size and time. Hetzner costs the same for any run up to an hour, so it gets cheaper by comparison as runs get longer and dearer
as they get shorter.

## Monthly cost at three loads (infrastructure only)

Egress is 50 MB per run (10, 100 and 1,000 GB a month). Free tiers are applied where the page states one.

| Provider and shape | Small beta, 200 runs | 2,000 runs | 20,000 runs |
|---|---|---|---|
| Fly.io `fra`, shared-cpu-2x 4 GB | $2.91 | $29.14 | $291.36 |
| Scaleway Serverless Jobs (egress not priced) | EUR 3.92 ($4.41) | EUR 64.40 ($72.47) | EUR 669.20 ($753.05) |
| Google Cloud Run jobs | $6.42 (free tier covers about half) | $112.26 | $1,170.66 |
| Hetzner CPX22, one server per run | EUR 6.40 ($7.20) | EUR 64.00 ($72.02) | EUR 640.00 ($720.19) |
| Fly.io `fra`, performance-2x 4 GB | $7.25 | $72.52 | $725.16 |
| AWS Fargate, Frankfurt | $7.90 (egress inside the 100 GB) | $79.04 | $871.40 |
| GitHub larger runner, 2-core (Azure network not costed) | $24.00 | $240.00 | $2,400.00 |

Add the small fixed items that were not costed (registry, logs, an optional static IP). At these loads they are a few dollars a month.
Concurrency does not change these totals. It changes which quotas must be raised: at 50 runs at once, 100 vCPU in one region. Quota
defaults were not read for any provider.

## Model (LLM) charges, kept apart

The per-run assumption is 1.5 M input tokens and 30 k output tokens on `claude-sonnet-4-6`:

- **Ceiling, the way stAirCase's budget cap counts** (standard rates, no cache discount): 1.5 x $3 + 0.03 x $15 = **$4.95 per run**.
- **With prompt caching** (80% of input read from cache at $0.30, 20% written at $3.75): **$1.94 per run**.
- Monthly: **$390 to $990** at 200 runs, **$3,900 to $9,900** at 2,000, **$39,000 to $99,000** at 20,000.

Under FR-6 the model key is the customer's, so these charges land on the customer's provider bill, not the operator's. A hosted runner
does not change that bill, only where the key would have to be. EU-only model inference is a separate matter. The first-party API lists
only "global" and "us" inference geography. EU inference would need a partner cloud, whose regional prices were not read for this study.
The token counts are the weakest assumption here, and the experiment replaces them with measurements.

## What the numbers say

- Every candidate costs less than **$0.06 per run** for this shape except GitHub's larger runners. Infrastructure is 1% to 3% of a run's
  total, so the cheapest provider saves less than one token-heavy run costs.
- The operator's own bill would be small at the beta: **under $10 a month** for 200 runs on any modelled candidate except GitHub. At
  20,000 runs it is **$290 to $1,170 a month**.
- The real differences are in isolation and fit. Fly.io and Cloud Run give each run its own kernel with namespaces available. Hetzner
  gives a whole server. Fargate and Scaleway lock down the very features stAirCase's own sandbox uses.

## Recommendation: the next experiment and its budget

**Experiment H1: two providers, one replayed run, measured end to end.** The owner creates the accounts, sets the spending caps and runs
it. Nothing here is set up by this study.

1. Build one runner image: a pinned stAirCase release, `git`, and the agent CLI that recorded the fixture.
2. Get a recorded run: reuse an existing `--record-llm` file, or record one real run on the owner's key with
   `staircase project config set <id> --budget-cap 5`.
3. On **Fly.io `fra`, performance-2x 4 GB** and on **Hetzner CPX22 in `NBG1`**, start 30 disposable runs of that run each with
   `--replay-llm` (no model charges), three at a time, creating each machine fresh and deleting it at the end.
4. Measure for each: create-to-ready seconds, wall time, peak memory, egress bytes, the billed amount on the provider's usage page against
   this study's estimate, whether `--sandbox required` works (Landlock ABI or bubblewrap), whether the docker sandbox works, that nothing
   billable is left after deletion (volume, IP, snapshot), and where the provider keeps the machine's logs.
5. Write the result as a dated record in this directory next to this study.

**Budget.** The estimate for 60 runs plus about 10 hours of image building and debugging on each provider is **under $5** for
infrastructure (Fly.io performance-2x in `fra` costs about $0.106/hour; Hetzner CPX22 EUR 0.032/hour). Set hard caps anyway:
**$20 on Fly.io, EUR 10 on Hetzner**, and **$5** of model spending if a new recording is needed. That is **about $36 in hard caps**. Fly.io
has no general free tier for new organizations, only a trial of up to 2 hours of Machine runtime or 7 days, so the experiment will be
billed there.

**Decide from it:** if Fly.io's microVM runs `--sandbox required` and starts in seconds, it becomes the hosted-runner candidate. Hetzner
serves as the fallback with the simpler EU-company transfer story. If neither runs stAirCase's sandbox, try Cloud Run jobs next (its
microVM documents namespaces), not Fargate or Scaleway.

## What this study does not decide

- **Whether to offer hosted runners at all.** The PRD starts with customer-owned runners and lists hosted execution as later exploration.
  This study only prices the option.
- **Key custody.** A hosted runner needs the customer's model key for the run, and stAirCase's signing key to certify the commit. FR-6
  keeps both customer-side. Passing them in per run, holding them in a provider's secret store, or a hosted run that cannot reach the
  higher assurance levels: each is an owner decision with security and contract consequences.
- **The legal package.** Each provider's DPA, subprocessor list, transfer mechanism and log locations need the same review the research
  section did for Supabase. An EU region is not an all-EU-processing promise.
- **Who pays.** The operator would pay for infrastructure. Whether users pay, and how much, is billing, which the PRD leaves out of the
  beta.
- **Capacity and quotas.** The providers' default limits on machines, vCPUs or servers were not read, nor were start-up latencies
  measured.
- **Model choice and EU inference.** These are kept apart above and not compared here.

## Sources

Every page below was read on 2026-10-18 unless noted. Prices are as the page showed them that day.

- Fly.io pricing (Machines, regional multipliers, egress, rootfs, reservations, trial): https://docs.fly.io/about/pricing
- Fly.io architecture (Firecracker microVMs, isolation between customers): https://docs.fly.io/reference/architecture
- Fly.io Machines overview (create and delete through the API, start times): https://docs.fly.io/machines/overview/
- Hetzner Cloud Regular Performance (CPX prices, locations, traffic, snapshots): https://www.hetzner.com/cloud/regular-performance
- Hetzner Cloud Cost-Optimized (CX and CAX, all marked not available): https://www.hetzner.com/cloud/cost-optimized
- Hetzner Cloud General Purpose (CCX13 EUR 0.0697/hour, for reference): https://www.hetzner.com/cloud/general-purpose
- Hetzner Cloud billing FAQ (hourly rounding, monthly cap, billed while off): https://docs.hetzner.com/cloud/billing/faq
- Hetzner legal notice (Hetzner Online GmbH, Gunzenhausen, Germany): https://www.hetzner.com/legal/legal-notice
- AWS Fargate pricing (Europe (Frankfurt), Linux/x86; billing granularity; ephemeral storage): https://aws.amazon.com/fargate/pricing/
- AWS Fargate security considerations (isolation, no privileged containers, restricted capabilities): https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-security-considerations.html
- AWS EC2 on-demand pricing, data transfer (Europe (Frankfurt) internet egress, 100 GB free): https://aws.amazon.com/ec2/pricing/on-demand/
- AWS VPC pricing (public IPv4 $0.005/hour): https://aws.amazon.com/vpc/pricing/
- Google Cloud Run pricing (jobs, `europe-west4`, Tier 1 and Tier 2 regions, free tier, minimums): https://cloud.google.com/run/pricing
- Google Cloud Run execution environments (gVisor first generation; microVM second generation; jobs use the second): https://docs.cloud.google.com/run/docs/about-execution-environments
- Google Cloud network pricing (Premium Tier internet egress): https://cloud.google.com/vpc/network-pricing
- Scaleway serverless pricing (Serverless Jobs, Paris): https://www.scaleway.com/en/pricing/serverless/
- Scaleway Serverless Jobs limitations (gVisor, capabilities, limits): https://www.scaleway.com/en/docs/serverless-jobs/reference-content/jobs-limitations/
- Scaleway legal notice (Scaleway SAS, Paris, France): https://www.scaleway.com/en/legal-notice/
- GitHub Actions runner pricing (larger runners, rounding, included minutes): https://docs.github.com/en/billing/reference/actions-runner-pricing
- GitHub Actions billing (included minutes per plan, larger runners always charged): https://docs.github.com/en/billing/concepts/product-billing/github-actions
- GitHub-hosted runners (a new VM per runner, hosted in Azure): https://docs.github.com/en/actions/concepts/runners/github-hosted-runners
- GitHub Azure private networking (supported regions, runners deployed in the subnet's region): https://docs.github.com/en/enterprise-cloud@latest/admin/configuring-settings/configuring-private-networking-for-hosted-compute-products/about-azure-private-networking-for-github-hosted-runners-in-your-enterprise
- Anthropic pricing (Claude Sonnet 4.6, cache prices, inference geography): https://platform.claude.com/docs/en/about-claude/pricing
- ECB euro reference rates (USD 1.1253 on 2026-10-16, read 2026-10-18): https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html

Hetzner's and AWS's prices are filled in by the page's script, so they were read from the rendered page in a browser. Hetzner's were read
with its VAT country set to "All others".
