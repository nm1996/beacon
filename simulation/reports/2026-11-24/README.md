# Simulation runs on 2026-11-24

29 checks: 29 passed, 0 failed. Written by `runner -summarize` from the JSON reports in this folder; see [the simulation](../../README.md) for what each scenario does.

## Run 214319Z: all 29 checks passed

| Started (UTC) | run.sh scenario | Check | Result | Measured | Took |
|---|---|---|---|---|---|
| 21:43:39 | normal | [normal](214339Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:43:41 | instance-killed | [normal](214341Z-normal.json) | ✅ pass | settle 0.01s | 100ms |
| 21:43:42 | instance-killed | [gone](214342Z-gone.json) | ✅ pass | gone beacon-a 8.21s, gone beacon-b 8.21s | 17.7s |
| 21:44:01 | instance-stopped | [normal](214401Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:44:02 | instance-stopped | [gone](214402Z-gone.json) | ✅ pass | gone beacon-a 0.26s, gone beacon-b 0.26s | 9.7s |
| 21:44:13 | instance-back | [normal](214413Z-normal.json) | ✅ pass | settle 0.00s | 100ms |
| 21:44:14 | instance-back | [gone](214414Z-gone.json) | ✅ pass | gone beacon-a 7.42s, gone beacon-b 7.42s | 16.9s |
| 21:44:32 | instance-back | [back](214432Z-back.json) | ✅ pass | back beacon-a 0.52s, back beacon-b 0.52s | 10.8s |
| 21:44:56 | instance-back-same-id | [normal](214456Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:44:57 | instance-back-same-id | [back](214457Z-back.json) | ✅ pass | back beacon-a 0.02s, back beacon-b 0.02s | 10.4s |
| 21:45:19 | node-stopped | [normal](214519Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:45:20 | node-stopped | [node-down](214520Z-node-down.json) | ✅ pass | unlisted beacon-b 0.26s | 39.4s |
| 21:46:06 | node-stopped | [normal](214606Z-normal.json) | ✅ pass | settle 0.01s | 100ms |
| 21:46:07 | node-killed | [normal](214607Z-normal.json) | ✅ pass | settle 0.00s | 100ms |
| 21:46:08 | node-killed | [node-down](214608Z-node-down.json) | ✅ pass | unlisted beacon-b 12.98s | 39.4s |
| 21:46:54 | node-killed | [normal](214654Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:46:58 | node-stopped-retry-post | [normal](214658Z-normal.json) | ✅ pass | settle 0.00s | 100ms |
| 21:46:59 | node-stopped-retry-post | [node-down](214659Z-node-down.json) | ✅ pass | unlisted beacon-b 0.26s | 39.4s |
| 21:47:45 | node-stopped-retry-post | [normal](214745Z-normal.json) | ✅ pass | settle 0.01s | 100ms |
| 21:47:53 | node-killed-retry-post | [normal](214753Z-normal.json) | ✅ pass | settle 0.00s | 100ms |
| 21:47:54 | node-killed-retry-post | [node-down](214754Z-node-down.json) | ✅ pass | unlisted beacon-b 11.45s | 39.3s |
| 21:48:40 | node-killed-retry-post | [normal](214840Z-normal.json) | ✅ pass | settle 0.01s | 100ms |
| 21:48:44 | storage-down | [normal](214844Z-normal.json) | ✅ pass | settle 0.01s | 100ms |
| 21:48:45 | storage-down | [storage-down](214845Z-storage-down.json) | ✅ pass | instances back after serving 1.86s, lookups failing after 0.26s, served again beacon-a 1.38s, served again beacon-b 1.40s | 32.9s |
| 21:49:33 | ownership | [normal](214933Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:49:34 | ownership | [ownership](214934Z-ownership.json) | ✅ pass | settle 0.00s | 200ms |
| 21:49:41 | ownership | [ownership](214941Z-ownership.json) | ✅ pass | settle 0.01s | 100ms |
| 21:49:54 | load-large | [normal](214954Z-normal.json) | ✅ pass | settle 0.01s | 200ms |
| 21:49:56 | load-large | [load](214956Z-load.json) | ✅ pass | duration 120.01s | 2m0s |

## Resources: [214953Z-load-100-stats.jsonl](214953Z-load-100-stats.jsonl)

| Container | Peak CPU | Peak memory |
|---|---|---|
| beacon-sim-beacon-a-1 | 10.0% | 11.26MiB |
| beacon-sim-beacon-b-1 | 10.6% | 10.87MiB |
| beacon-sim-catalog-1 | 22.3% | 13.91MiB |
| beacon-sim-inventory-1-1 | 0.1% | 2.41MiB |
| beacon-sim-lb-1 | 8.1% | 8.863MiB |
| beacon-sim-orders-1-1 | 0.1% | 2.414MiB |
| beacon-sim-orders-2-1 | 0.1% | 2.402MiB |
| beacon-sim-payments-1-1 | 0.1% | 2.43MiB |
| beacon-sim-postgres-1 | 14.3% | 63.82MiB |
