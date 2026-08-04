---
id: token-benchmark
title: Token benchmark against a real repository
doc-type: reference
status: current
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-08-04
---

# Token benchmark against a real repository

TerraGraph exists to spend fewer tokens than `grep` / `glob` / `read` while giving a better
answer. That is a measurable claim, so it is measured rather than asserted.

**Result: 81.1% fewer tokens across ten questions, cheaper on 9 of 10, and correct on 10 of
10 where the grep baseline is correct on 3.**

## Subject

[`terraform-google-modules/terraform-example-foundation`](https://github.com/terraform-google-modules/terraform-example-foundation)
— Google's published reference architecture. 265 `.tf` files across 48 directories, 735 KB,
**29 root modules**. TerraGraph indexes it into 1,952 nodes and 4,217 edges in 0.10s of CPU.

Reading the whole repository costs **170,566 tokens**. That is the ceiling this tool exists
to stay far below.

## Method

Tokens are counted with `tiktoken` `cl100k_base`. That is an approximation of any given
model's tokenizer, but it is applied identically to both sides, so the ratio holds even
where an absolute number would not.

What is counted is **what enters the model's context** — the thing that costs money and
crowds out reasoning.

Three rules, all chosen to favour the baseline:

1. The grep baseline is what a competent agent would actually run — a targeted
   `grep -rn --include="*.tf"` — never `cat` of the repository.
2. Where grep output alone cannot answer, the baseline additionally pays for the **minimum**
   files an agent must open, determined by finding the real answer first.
3. Where the grep path cannot answer correctly at all, its tokens are still counted and it
   is marked wrong. Excluding those questions would flatter TerraGraph.

## Results

| Question | grep | + reads | correct? | TerraGraph | saving |
|---|---:|---:|:---:|---:|---:|
| Where is the Cloud Build artifacts bucket defined? | 354 | 1,697 | yes | 330 | 80.6% |
| What actually references `var.remote_state_bucket`? | 1,438 | 1,438 | no | 1,033 | 28.2% |
| Which stacks call the `vpn_ha` module, at which versions? | 986 | 986 | no | 277 | 71.9% |
| What breaks if I change `var.environment_code`? | 1,504 | 3,007 | no | 1,059 | 64.8% |
| Are there unused variables or outputs? | 26,784 | 26,784 | no | 979 | 96.3% |
| Where is organization audit logging configured? | 1,298 | 3,937 | yes | 956 | 75.7% |
| What does `module.base_env` depend on? | 138 | 2,440 | yes | 974 | 60.1% |
| Which resources use `random_string`? | 845 | 845 | no | 1,037 | −22.7% |
| Where is `var.project_budget` consumed? | 1,506 | 1,506 | no | 889 | 41.0% |
| What root modules does this repo have? | 813 | 813 | no | 664 | 18.3% |
| **Total** | | **43,453** | **3/10** | **8,198** | **81.1%** |

The one loss is `random_string`, and TerraGraph is *correct* there while grep is not: grep
returns the declarations, the references and every string mention undifferentiated, and
cannot say which is which.

Correctness is the axis that does not show up in the token column. Grep genuinely cannot
answer seven of these — not slowly, at all. It cannot separate a declaration from a use,
correlate a module `source` with a `version` on another line of another file, compute a
transitive closure, or prove a negative like "nothing references this".

## What the benchmark found

It was not a validation exercise. The first run came back **50.9% saving and cheaper on
only 5 of 10** — TerraGraph *lost* to grep on half the questions.

The cause was a real defect: **only `terra_explore` was ever budgeted.** `terra_for_address`,
`terra_impact`, `terra_modules` and `terra_orphans` emitted output that scaled with the
repository. On a 29-stack repo, `var.remote_state_bucket` is declared 25 times, and printing
every declaration with every reference cost 3,814 tokens against grep's 1,438 — a retrieval
tool more expensive than the search it replaces.

Every tool is now bounded, and the omission is always named so a caller can escalate
deliberately. That took the suite to 63.7%.

## Calibrating the default

The remaining gap was the default budget itself. `DefaultCharBudget = 12000` was inherited
from DocGraph, where it was sized for Markdown documents with a median of ~5,100 characters.
Terraform blocks are an order of magnitude smaller, so the number was never right here — it
was carried over.

Sweeping it, with `explore` held at 12000:

| List-tool budget | Total tokens | Saving | Cheaper than grep |
|---:|---:|---:|:---:|
| 3,000 | 6,781 | 84.4% | 10/10 |
| **4,000** | **8,198** | **81.1%** | **9/10** |
| 5,000 | 9,254 | 78.7% | 9/10 |
| 12,000 | 15,754 | 63.7% | 6/10 |

**4,000 is the chosen default** for the relationship tools, and retrieval keeps 12,000.
The two have genuinely different shapes: retrieval returns source and needs room, while a
relationship answer is a list whose header already carries the true total, so truncating it
costs a caller much less. 3,000 is cheaper still, but starts showing only two of
twenty-five declarations, which is thinner than an answer should be by default when the
caller has not asked for brevity.

At 4,000 an answer still carries its correct total, several real examples, and a named
omission:

```
25 declaration(s) of "var.remote_state_bucket".
…
[61 orphan(s) omitted for space — ask again with a larger charBudget]
```

## Reproducing

The harness is not committed — it depends on a cloned third-party repository and a Python
tokenizer. To repeat it:

```bash
git clone --depth 1 https://github.com/terraform-google-modules/terraform-example-foundation
terragraph status --repo terraform-example-foundation
```

Then compare any question's `terragraph` output against the `grep` an agent would otherwise
run, counting tokens on both.

## Caveats

- **One repository.** A repo with few roots and large modules would shift these numbers; the
  orphans question in particular wins by 96% here because 29 stacks make the grep baseline
  enormous. Re-run the sweep against your own repositories before trusting the default.
- **`cl100k_base`, not a Claude tokenizer.** The ratio is sound; treat the absolute counts as
  indicative.
- **The `reads` column is a judgment call.** It is the minimum an agent must open, chosen
  after finding the real answer — which is generous to the baseline, since a real agent does
  not know in advance which file is the right one.

## Related

- [The TerraGraph plan](plans/terragraph-v1.md) — where this benchmark was specified as the
  success criterion
- [README](../README.md) — what the tools do
