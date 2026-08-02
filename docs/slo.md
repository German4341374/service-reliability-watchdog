# SLO and error-budget model

For the configured rolling window:

```text
eligible = all checks except Maintenance
good = Healthy + Degraded
availability = good / eligible
latency compliance = successful checks at or below latency target / eligible
allowed bad fraction = 1 - availability target
actual bad fraction = 1 - availability
burn rate = actual bad fraction / allowed bad fraction
remaining budget = max(0, 1 - burn rate)
```

The remaining-budget field is a normalized fraction for the current window. For a 99.9% target,
one failed check out of 1,000 consumes the entire sampled budget. A burn rate of `2x` means the
observed bad fraction is twice the sustainable fraction.

## Limitations

- Samples are equally weighted even if intervals change.
- Availability between check instants is unknown.
- Sparse samples make ratios volatile.
- Degraded counts as available but can fail latency compliance.
- The simple rolling ratio does not implement multi-window multi-burn-rate paging.
- Maintenance exclusion is based on the state stored at check time.

Use the calculation as an operational indicator, not a billing or contractual measurement without
additional validation.

