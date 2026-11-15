# Mutation testing: residual mutants

Gate: `.gremlins.yaml` (`efficacy: 99`, `mutant-coverage: 97`), measured per package with
`gremlins unleash ./internal/domain/<pkg>/` (what the `mutation-fast` CI job runs).

Measured 2026-11-15:

| Package | Killed | Lived | Not covered | Efficacy | Mutator coverage |
|---|---|---|---|---|---|
| network | 2 | 0 | 0 | 100.00% | 100.00% |
| returnauthorization | 88 | 0 | 2 | 100.00% | 97.78% |
| shared | 40 | 0 | 0 | 100.00% | 100.00% |

Aggregate (`make mutation-full`, the weekly job): 130 killed, 0 lived, 2 not covered, efficacy 100.00%, mutator coverage 98.48%.

## Mutants that cannot be killed

| Package | File:line | Mutator | Why |
|---|---|---|---|
| returnauthorization | authorization.go:23:30 | ARITHMETIC_BASE | `ReturnAuthorizationTTL = 30 * 24 * time.Hour` is a `const` declaration. The Go coverage profile has no executable statement for it, so gremlins reports NOT COVERED however many tests read the value. |
| returnauthorization | authorization.go:23:35 | ARITHMETIC_BASE | Same declaration, second `*` operator. |

Two of 90 mutants make 97.78% the ceiling of mutator coverage for `returnauthorization`, which is why
`mutant-coverage` is 97 and not 99. The value itself is asserted by
`TestAuthorizeRaisesAuthorizedAndComputesExpiry` (expiry = authorizedAt + TTL).
