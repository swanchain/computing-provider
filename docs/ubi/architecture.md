# UBI Integration Architecture

## Overview

This document describes how the Computing Provider integrates with the UBI (Universal Benchmark Infrastructure) system to execute ZK proof tasks.

## Ecosystem Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     UBI ECOSYSTEM ARCHITECTURE                   │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ubi-benchmark     ubi-engine        ubi-server    computing-   │
│  (Generator)       (Orchestrator)    (Economics)   provider     │
│       │                 │                │              │       │
│  Generate C1/C2    Task lifecycle    UBI formula    Execute     │
│  proofs from       Routes to CPs     Reward calc    ZK proofs   │
│  Filecoin sectors  Verify proofs     Collateral     Submit via  │
│       │                 │              checks       Sequencer   │
│       └─────────────────┼──────────────┼────────────────┘       │
│                         │              │                         │
│              On-Chain Settlement (SWAN Token)                    │
└─────────────────────────────────────────────────────────────────┘
```

| Component | Role | Layer |
|-----------|------|-------|
| **ubi-benchmark** | Task Generator | Generation Layer |
| **ubi-engine** | Task Orchestrator | Technical Layer |
| **ubi-server** | UBI Economics | Economic Layer |
| **computing-provider** | Task Executor | **Execution Layer** |

## Computing Provider Role

The Computing Provider is the **Execution Layer** responsible for:

1. **Task Reception** - Receives ZK tasks from ubi-engine
2. **Task Execution** - Runs proof computation in Docker containers
3. **Proof Submission** - Submits proofs via Sequencer
4. **Resource Reporting** - Reports available hardware to network

## Task Flow in Computing Provider

```
┌─ COMPUTING PROVIDER (Task Flow) ────────────────────────────────┐
│                                                                  │
│  1. TASK RECEPTION                                              │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ POST /api/v1/computing/cp/ubi                        │     │
│     │                                                      │     │
│     │ ZkTaskReq:                                           │     │
│     │   - id: Task ID from ubi-engine                     │     │
│     │   - name: Task identifier                           │     │
│     │   - task_type: 1-5 (FIL_C2_*, Mining)              │     │
│     │   - input_param: URL to download task input         │     │
│     │   - verify_param: URL for verification params       │     │
│     │   - signature: Signed by ubi-engine                 │     │
│     │   - resource: CPU, GPU, Memory requirements         │     │
│     │   - deadline: Task completion deadline              │     │
│     │                                                      │     │
│     │ Validation:                                          │     │
│     │   - Check GPU utilization threshold                 │     │
│     │   - Verify signature (optional)                     │     │
│     │   - Save to database with TASK_RECEIVED status     │     │
│     └─────────────────────────────────────────────────────┘     │
│                              │                                   │
│                              ▼                                   │
│  2. TASK EXECUTION (Docker)                                     │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ doFilC2TaskForDocker()                              │     │
│     │                                                      │     │
│     │ Steps:                                               │     │
│     │   a. Download input from InputParam URL             │     │
│     │   b. Create Docker container with GPU resources     │     │
│     │   c. Mount Filecoin v28 parameters                  │     │
│     │   d. Execute: ubi-bench c2 <input.json>            │     │
│     │   e. Collect proof output                           │     │
│     │                                                      │     │
│     │ Environment:                                         │     │
│     │   - FIL_PROOFS_PARAMETER_CACHE                      │     │
│     │   - RUST_GPU_TOOLS_CUSTOM_GPU                       │     │
│     └─────────────────────────────────────────────────────┘     │
│                              │                                   │
│                              ▼                                   │
│  3. PROOF SUBMISSION                                            │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ Sequencer Mode (EnableSequencer = true):            │     │
│     │   a. Get authentication token from Sequencer        │     │
│     │   b. POST proof to Sequencer                        │     │
│     │   c. Sequencer batches proofs for on-chain submit   │     │
│     │                                                      │     │
│     │ Direct Mode (AutoChainProof = true):                │     │
│     │   a. Sign transaction with CP wallet                │     │
│     │   b. Submit proof directly to blockchain            │     │
│     │                                                      │     │
│     │ Update task status to TASK_SUBMITTED                │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/computing/ubi_service.go` | UBI task reception and management |
| `internal/computing/sequence_service.go` | Sequencer communication |
| `internal/computing/ecp_image_service.go` | ECP task execution |
| `internal/models/entitys.go` | Task entity definitions |
| `internal/models/cp.go` | ZkTaskReq model |
| `cmd/computing-provider/ubi.go` | UBI CLI commands |

## Task States

```
Computing Provider Task States:
  0: TASK_REJECTED_STATUS    - Task rejected by CP
  1: TASK_RECEIVED_STATUS    - Task received, validation passed
  2: TASK_RUNNING_STATUS     - Task executing in container
  3: TASK_SUBMITTED_STATUS   - Proof submitted to sequencer
  4: TASK_FAILED_STATUS      - Task execution failed
  5: TASK_VERIFIED_STATUS    - Proof verified by engine
  8: TASK_REWARDED_STATUS    - Reward received

State Transitions:
  Received ──execute──> Running ──complete──> Submitted ──verify──> Verified ──reward──> Rewarded
      │                    │                      │
      ▼                    ▼                      ▼
   Rejected             Failed                 Failed
```

## Sequencer Integration

The Sequencer batches proof submissions to reduce gas costs:

```
┌─────────────────────────────────────────────────────────────────┐
│                    SEQUENCER BATCHING                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  CP1 ──┐                                                        │
│  CP2 ──┼──> Sequencer ──> Blockchain (1 tx for N proofs)       │
│  CP3 ──┘                                                        │
│                                                                  │
│  Gas Cost: O(1) instead of O(N)                                 │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Authentication Flow

```go
// 1. Get current block number
blockNumber := client.BlockNumber()

// 2. Sign authentication message
signMsg := Sign(contractAddr + blockNumber)

// 3. Get token from Sequencer
token := POST /v1/token {
    cp_addr: cpAddress,
    block_number: blockNumber,
    sign: signMsg
}

// 4. Submit proof with token
response := POST /v1/tasks {
    headers: { "Authorization": token },
    body: proofData
}

// 5. Update token from response header
newToken := response.Header.Get("new-token")
```

### Token Caching

Tokens are cached at `$CP_PATH/token` and refreshed automatically when expired.

## Resource Reporting

The `resource-exporter` container collects and reports hardware resources:

```
┌─────────────────────────────────────────────────────────────────┐
│                    RESOURCE EXPORTER                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Container: swanhub/resource-exporter:v13.0.0                   │
│  Port: 9000                                                      │
│                                                                  │
│  Collects:                                                       │
│    - CPU cores (free/total)                                     │
│    - Memory (free/total GiB)                                    │
│    - Storage (free/total GiB)                                   │
│    - GPU models and availability                                │
│                                                                  │
│  Reports every 3 minutes to ubi-scanner                         │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

Example output:
```
freeCpu: 16
freeMemory: 38.99 GiB
freeStorage: 1665.12 GiB
freeGpu: map[NVIDIA 3070:1]
useGpu: map[]
```

## Configuration

### config.toml

```toml
[API]
Port = 9085                                    # ECP service port
MultiAddress = "/ip4/<PUBLIC_IP>/tcp/<PORT>"   # Public multiAddress
Pricing = "true"                               # Accept smart pricing

[UBI]
UbiEnginePk = "0x594A4c..."                    # UBI Engine public key
EnableSequencer = true                          # Use batched submissions
AutoChainProof = false                          # Direct chain fallback
SequencerUrl = "https://sequencer.swanchain.io"
EdgeUrl = "https://edge-api.swanchain.io/v1"
VerifySign = true                               # Verify task signatures
```

### Environment Variables

```bash
# Required for ZK proof tasks
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"
```

## CLI Commands

### Start ECP Daemon
```bash
computing-provider run
```

### List UBI Tasks
```bash
computing-provider ubi list
computing-provider ubi list --show-failed
```

### List ECP Tasks (Inference/Mining)
```bash
computing-provider task list
```

### Sequencer Management
```bash
# Add funds to sequencer account
computing-provider sequencer add --from <WALLET> <AMOUNT>

# Withdraw from sequencer
computing-provider sequencer withdraw --owner <WALLET> <AMOUNT>
```

## Task Types

| Type | ID | Description | Resource |
|------|-----|-------------|----------|
| Fil-C2 | 1 | Filecoin C2 ZK proof | CPU/GPU |
| Mining | 2 | Mining workloads | GPU |
| Inference | 4 | AI inference (ECP2) | Docker |

For ECP (ZK proofs), set task-types to `1,2,4`.
For ECP2 (inference only), set task-types to `4`.

## Troubleshooting

### Common Issues

1. **Docker Permission Denied**
   ```bash
   sudo usermod -aG docker $USER
   # Or use sg docker
   sg docker -c "computing-provider run"
   ```

2. **NVIDIA Container Toolkit Missing**
   ```
   Error: could not select device driver "nvidia"
   ```
   Install NVIDIA Container Toolkit:
   ```bash
   sudo apt-get install -y nvidia-container-toolkit
   sudo nvidia-ctk runtime configure --runtime=docker
   sudo systemctl restart docker
   ```

3. **resource-exporter Conflict**
   ```bash
   docker rm -f resource-exporter
   ```

4. **CP Account Not Created**
   ```bash
   computing-provider account create \
       --ownerAddress <OWNER> \
       --workerAddress <WORKER> \
       --beneficiaryAddress <BENEFICIARY> \
       --task-types 1,2,4
   ```

5. **Sequencer Token Expired**
   Tokens auto-refresh. If issues persist, delete `$CP_PATH/token`.

## Related Documentation

- [ECP Setup Guide](../ecp/README.md)
- [ubi-engine Architecture](https://github.com/swanchain/ubi-engine/blob/main/docs/SYSTEM_ARCHITECTURE.md)
- [ubi-server Architecture](https://github.com/swanchain/ubi-server/blob/main/docs/architecture.md)
- [ubi-benchmark Usage](https://github.com/swanchain/ubi-benchmark)
