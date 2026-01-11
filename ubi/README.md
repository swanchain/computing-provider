# ECP2 - Edge Computing Provider 2

**ECP2 (Edge Computing Provider 2)** is the default mode for Computing Provider v2, enabling you to deploy and run AI inference containers with GPU support. ECP2 specializes in processing data at the source of data generation, using minimal latency setups ideal for real-time AI inference applications.

## Provider Modes

| Mode | Task Type | Description |
|------|-----------|-------------|
| **ECP2** (Default) | 4 | AI inference containers |
| ECP (ZK-Proof) | 1, 2 | FIL-C2 and mining proofs |

Both modes run via `computing-provider ubi daemon` and do NOT require Kubernetes.

---

# Quick Start: ECP2 Mode

## Prerequisites

- Docker with NVIDIA Container Toolkit installed
- Map port to public network: `<Intranet_IP>:9085 <--> <Public_IP>:<PORT>`
- Run the setup script:
```bash
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/setup.sh | bash
```

## Install and Initialize

1. **Download computing-provider:**
```bash
wget https://github.com/swanchain/go-computing-provider/releases/download/v1.1.3/computing-provider
```

2. **Initialize repo:**
```bash
./computing-provider init --multi-address=/ip4/<YOUR_PUBLIC_IP>/tcp/<YOUR_PORT> --node-name=<YOUR_NODE_NAME>
```

> **Note:** Default repo is `~/.swan/computing`. Override with `export CP_PATH="<YOUR_CP_PATH>"`

3. **Create or import wallet:**
```bash
# Create new wallet
./computing-provider wallet new

# Or import existing wallet
./computing-provider wallet import private.key
```

4. **Create ECP2 account (task-types 4):**
```bash
./computing-provider account create \
    --ownerAddress <YOUR_OWNER_ADDRESS> \
    --workerAddress <YOUR_WORKER_ADDRESS> \
    --beneficiaryAddress <YOUR_BENEFICIARY_ADDRESS> \
    --task-types 4
```

> **Task Types:** 1=FIL-C2, 2=Mining, 3=AI, 4=ECP2/Inference, 5=NodePort, 100=Exit

5. **Add collateral:**
```bash
computing-provider collateral add --ecp --from <YOUR_WALLET_ADDRESS> <AMOUNT>
```

> To withdraw collateral:
> ```bash
> computing-provider collateral withdraw --ecp --owner <YOUR_WALLET_ADDRESS> --account <YOUR_CP_ACCOUNT> <amount>
> ```

## Configure ECP2

Configure in `$CP_PATH/config.toml`:

```toml
[API]
Domain = "*.example.com"                        # Domain for single-port services
AutoDeleteImage = false                         # Auto-delete unused images
PortRange = ["40000-40050", "40060", "40065"]   # Ports for multi-port containers
```

**Port Configuration:**
- **Single-port containers:** Use `traefik` with domain resolution. Configure a domain (*.example.com) to resolve to the CP's IP. Port 9000 must be open.
- **Multi-port containers:** Use `PortRange` with direct IP + port mapping (one-to-one mapping between host ports and public IP).

## Start ECP2 Service

```bash
nohup ./computing-provider ubi daemon >> cp.log 2>&1 &
```

## Check ECP2 Tasks

```bash
computing-provider task list --ecp
```

Example output:
```
TASK UUID                               TASK NAME       IMAGE NAME                              CONTAINER STATUS   REWARD    CREATE TIME
75f9df4e-b6a5-40b0-b7ac-02fb1840dafa    inference-01    mymodel/inference:latest                running            1.2500    2024-11-24 10:23:32
842dd7d3-e9f0-4795-af3b-104fa5527099    model-serve     swanchain/llm-serve:latest              running            0.6195    2024-11-15 03:49:44
```

---

# ECP Mode (ZK-Proof)

ECP mode generates ZK-Snark proofs (FIL-C2, Aleo, etc.). This requires additional v28 parameters (~200GB).

## Additional Prerequisites

Download v28 parameters:
```bash
# At least 200G storage is needed
export PARENT_PATH="<V28_PARAMS_PATH>"

# 512MiB parameters
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-512.sh | bash

# 32GiB parameters
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-32.sh | bash
```

## Create ECP Account

For ZK-proof tasks, set task-types to 1,2 (or 1,2,4 to also support ECP2):

```bash
./computing-provider account create \
    --ownerAddress <YOUR_OWNER_ADDRESS> \
    --workerAddress <YOUR_WORKER_ADDRESS> \
    --beneficiaryAddress <YOUR_BENEFICIARY_ADDRESS> \
    --task-types 1,2,4
```

## Configure Sequencer

The Sequencer batches proof submissions to reduce gas costs. We **strongly recommend** enabling it.

1. **Modify `config.toml`:**
```toml
[UBI]
EnableSequencer = true    # Submit proofs to Sequencer (default: true)
AutoChainProof = false    # Fallback to chain when sequencer unavailable
```

2. **Deposit to Sequencer account:**
```bash
computing-provider sequencer add --from <YOUR_WALLET_ADDRESS> <amount>
```

> To withdraw from Sequencer:
> ```bash
> computing-provider sequencer withdraw --owner <YOUR_OWNER_WALLET_ADDRESS> <amount>
> ```

> **Note:** Gas cost is decided by the [Dynamic Pricing Strategy](https://docs.swanchain.io/bulders/market-provider/web3-zk-computing-market/sequencer)

## Start ECP Service

```bash
#!/bin/bash
export FIL_PROOFS_PARAMETER_CACHE=$PARENT_PATH
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"

nohup ./computing-provider ubi daemon >> cp.log 2>&1 &
```

**Notes:**
- `FIL_PROOFS_PARAMETER_CACHE`: Path to v28 parameters
- `RUST_GPU_TOOLS_CUSTOM_GPU`: Your GPU model and cores. See [supported cards](https://github.com/filecoin-project/bellperson?tab=readme-ov-file#supported--tested-cards)

## Check ZK Tasks

```bash
computing-provider ubi list
```

---

# Resource Pricing

## Configure Pricing

1. **Generate default pricing config:**
```bash
computing-provider --repo <YOUR_CP_PATH> price generate
```

2. **Customize prices in `$CP_PATH/price.toml`:**
```toml
TARGET_CPU="0.2"            # SWAN/thread-hour
TARGET_MEMORY="0.1"         # SWAN/GB-hour
TARGET_HD_EPHEMERAL="0.005" # SWAN/GB-hour
TARGET_GPU_DEFAULT="1.6"    # SWAN/GPU-hour
TARGET_GPU_3080="2.0"       # SWAN/3080 GPU-hour
```

3. **View current prices:**
```bash
computing-provider --repo <YOUR_CP_PATH> price view
```

## Smart Pricing

Enable smart pricing in `$CP_PATH/config.toml`:
```toml
[API]
Pricing = "true"   # Accept smart pricing orders (default: true)
```

---

# About Sequencer

## Why Use Sequencer?

ECP ZK-proof tasks require frequent blockchain interactions to submit proofs, which incurs significant gas costs. The Sequencer is a Layer 3 solution that batches all proofs from the network over a period (**currently 24 hours**) into a single transaction.

Benefits:
- Dramatically reduced gas costs
- Only pay minimal fee to Sequencer
- Automatic batching and submission

For more details, see the [Sequencer documentation](https://docs.swanchain.io/swan-provider/market-provider-mp/zk-engine/sequencer).
