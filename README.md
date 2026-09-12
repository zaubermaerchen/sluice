# sluice

When a state transition races with bytes already in flight, those boundary bytes follow normal concurrent pipe behavior; `sluice` does not provide a strict transition-boundary cutoff.
