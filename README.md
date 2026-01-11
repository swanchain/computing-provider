# Computing Provider v2

[![Discord](https://img.shields.io/discord/770382203782692945?label=Discord&logo=Discord)](https://discord.gg/Jd2BFSVCKw)
[![Twitter Follow](https://img.shields.io/twitter/follow/swan_chain)](https://twitter.com/swan_chain)
[![standard-readme compliant](https://img.shields.io/badge/readme%20style-standard-brightgreen.svg)](https://github.com/RichardLitt/standard-readme)

Computing Provider v2 is a CLI tool for the Swan Chain decentralized computing network. It enables operators to provide computational resources (CPU, GPU, memory, storage) to the network and earn rewards.

**ECP2 (Edge Computing Provider 2)** is the default and recommended mode for Computing Provider v2, allowing you to deploy and run AI inference containers with GPU support. ECP2 mode connects to **Swan Inference**, the decentralized inference marketplace.

## Provider Modes

| Mode | Description | Requirements | Command |
|------|-------------|--------------|---------|
| **ECP2** (Default) | Deploy AI inference containers | Docker + NVIDIA Container Toolkit | `computing-provider ubi daemon` |
| ECP (ZK-Proof) | Generate ZK-Snark proofs (FIL-C2, Aleo) | Docker + NVIDIA + v28 params | `computing-provider ubi daemon` |
| FCP | AI model training via Kubernetes | Kubernetes cluster | `computing-provider run` |

# Table of Contents

- [Quick Start: ECP2 Mode](#quick-start-ecp2-mode)
  - [Prerequisites](#prerequisites)
  - [Install NVIDIA Container Toolkit](#install-nvidia-container-toolkit)
  - [Build Computing Provider](#build-computing-provider)
  - [Initialize and Configure](#initialize-and-configure)
  - [Setup Wallet and Account](#setup-wallet-and-account)
  - [Start ECP2 Provider](#start-ecp2-provider)
- [Configuration Reference](#configuration-reference)
- [Additional Modes](#additional-modes)
  - [ECP Mode (ZK-Proof)](#ecp-mode-zk-proof)
  - [FCP Mode (Kubernetes)](#fcp-mode-kubernetes)
- [CLI Reference](#cli-reference)
- [Getting Help](#getting-help)

---

# Quick Start: ECP2 Mode

ECP2 (Edge Computing Provider 2) allows you to run AI inference containers on your GPU hardware and earn rewards from the Swan Chain network.

## Prerequisites

- Linux server with NVIDIA GPU
- Docker installed ([install guide](https://docs.docker.com/engine/install/))
- Public IP address
- Go 1.21+ for building from source

```bash
# Install Go if needed
wget -c https://golang.org/dl/go1.21.7.linux-amd64.tar.gz -O - | sudo tar -xz -C /usr/local
echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc && source ~/.bashrc
```

## Install NVIDIA Container Toolkit

Required for GPU access in Docker containers:

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

Verify installation:
```bash
docker run --rm --gpus all nvidia/cuda:11.0-base nvidia-smi
```

## Build Computing Provider

```bash
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider
git checkout releases

# Build for mainnet
make clean && make mainnet
make install

# Or for testnet
# make clean && make testnet
# make install
```

## Initialize and Configure

1. **Initialize the repository:**
```bash
computing-provider init --multi-address=/ip4/<YOUR_PUBLIC_IP>/tcp/<YOUR_PORT> --node-name=<YOUR_NODE_NAME>
```

> **Note:** Default repo location is `~/.swan/computing`. Override with `export CP_PATH="<YOUR_CP_PATH>"`

2. **Configure for ECP2** in `$CP_PATH/config.toml`:

```toml
[API]
Port = 8085                                    # Web server port
MultiAddress = "/ip4/<public_ip>/tcp/<port>"   # Your public address
Domain = "*.example.com"                       # Domain for single-port services (optional)
NodeName = "my-inference-node"                 # Your node name
PortRange = ["40000-40050", "40060"]           # Ports for multi-port containers

[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc-01.swanchain.org"
```

**Port Configuration:**
- Single-port containers: Use `traefik` with domain resolution (port 9000)
- Multi-port containers: Use `PortRange` with direct IP + port mapping

## Setup Wallet and Account

1. **Create or import wallet:**
```bash
# Create new wallet
computing-provider wallet new

# Or import existing wallet
computing-provider wallet import <YOUR_PRIVATE_KEY_FILE>
```

2. **Deposit SwanETH** to your wallet address. See the [getting started guide](https://docs.swanchain.io/swan-mainnet/getting-started-guide).

3. **Create CP account with ECP2 task type:**
```bash
computing-provider account create \
    --ownerAddress <YOUR_OWNER_ADDRESS> \
    --workerAddress <YOUR_WORKER_ADDRESS> \
    --beneficiaryAddress <YOUR_BENEFICIARY_ADDRESS> \
    --task-types 4
```

> **Task Type 4** = ECP2 (Inference)

4. **Add collateral:**
```bash
computing-provider collateral add --ecp --from <YOUR_WALLET_ADDRESS> <AMOUNT>
```

## Start ECP2 Provider

```bash
export CP_PATH=<YOUR_CP_PATH>
nohup computing-provider ubi daemon >> cp.log 2>&1 &
```

**Check running tasks:**
```bash
computing-provider task list --ecp
```

**Example output:**
```
TASK UUID                               TASK NAME       IMAGE NAME                              CONTAINER STATUS   REWARD    CREATE TIME
75f9df4e-b6a5-40b0-b7ac-02fb1840dafa    inference-01    mymodel/inference:latest                running            1.2500    2024-11-24 10:23:32
```

---

# Configuration Reference

## Resource Pricing

Configure pricing in `$CP_PATH/price.toml`:

```bash
# Generate default pricing config
computing-provider price generate

# View current prices
computing-provider price view
```

Example `price.toml`:
```toml
TARGET_CPU="0.2"            # SWAN/thread-hour
TARGET_MEMORY="0.1"         # SWAN/GB-hour
TARGET_HD_EPHEMERAL="0.005" # SWAN/GB-hour
TARGET_GPU_DEFAULT="1.6"    # SWAN/GPU-hour
TARGET_GPU_3080="2.0"       # SWAN/3080 GPU-hour
```

## Full config.toml Reference

```toml
[API]
Port = 8085                                    # Web server port
MultiAddress = "/ip4/<public_ip>/tcp/<port>"   # Public multiaddress
Domain = ""                                    # Domain for traefik routing
NodeName = ""                                  # Display name
Pricing = "true"                               # Accept smart pricing orders
AutoDeleteImage = false                        # Auto-delete unused images
PortRange = ["40000-40050"]                    # Ports for multi-port containers

[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc-01.swanchain.org"

[Registry]
ServerAddress = ""                             # Docker registry (multi-node only)
UserName = ""
Password = ""
```

---

# Additional Modes

## ECP Mode (ZK-Proof)

ECP (Edge Computing Provider) generates ZK-Snark proofs (Filecoin FIL-C2, Aleo, etc.). Requires additional v28 parameters (~200GB).

See [ECP/UBI Documentation](ubi/README.md) for full setup.

**Quick overview:**
```bash
# Download v28 parameters
export PARENT_PATH="<V28_PARAMS_PATH>"
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-512.sh | bash

# Set environment
export FIL_PROOFS_PARAMETER_CACHE=$PARENT_PATH
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"

# Create account with ZK task types
computing-provider account create \
    --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> \
    --task-types 1,2,4

# Enable sequencer (reduces gas costs)
# In config.toml:
# [UBI]
# EnableSequencer = true
# AutoChainProof = false

# Deposit to sequencer
computing-provider sequencer add --from <addr> <amount>

# Start daemon
nohup computing-provider ubi daemon >> cp.log 2>&1 &
```

## FCP Mode (Kubernetes)

For AI model training and deployment via Kubernetes clusters.

**Prerequisites:**
- Kubernetes v1.24+ cluster
- NVIDIA Device Plugin for K8s
- Ingress-nginx controller
- SSL certificate and domain

**Quick start:**
```bash
# Create account with AI task type
computing-provider account create \
    --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> \
    --task-types 3

# Add FCP collateral
computing-provider collateral add --fcp --from <addr> <amount>

# Start FCP
nohup computing-provider run >> cp.log 2>&1 &
```

For detailed Kubernetes setup, see [FCP Setup Guide](#install-the-kubernetes) below.

<details>
<summary><b>Full FCP/Kubernetes Setup Instructions</b></summary>

### Install the Kubernetes
The Kubernetes version should be `v1.24.0+`

###  Install Container Runtime Environment
If you plan to run a Kubernetes cluster, you need to install a container runtime into each node in the cluster so that Pods can run there, refer to [here](https://kubernetes.io/docs/setup/production-environment/container-runtimes/). And you just need to choose one option to install the `Container Runtime Environment`

#### Option 1: Install the `Docker` and `cri-dockerd` (**Recommended**)
To install the `Docker Container Runtime` and the `cri-dockerd`, follow the steps below:
* Install the `Docker`:
    - Please refer to the official documentation from [here](https://docs.docker.com/engine/install/).
* Install `cri-dockerd`:
    - `cri-dockerd` is a CRI (Container Runtime Interface) implementation for Docker. You can install it refer to [here](https://github.com/Mirantis/cri-dockerd).

#### Option 2: Install the `Docker` and the`Containerd`
* Install the `Docker`:
    - Please refer to the official documentation from [here](https://docs.docker.com/engine/install/).
* To install `Containerd` on your system:
  - `Containerd` is an industry-standard container runtime that can be used as an alternative to Docker. To install `containerd` on your system, follow the instructions on [getting started with containerd](https://github.com/containerd/containerd/blob/main/docs/getting-started.md).

### Optional-Setup a docker registry server
**If you are using the docker and you have only one node, the step can be skipped**.

If you have deployed a Kubernetes cluster with multiple nodes, it is recommended to set up a **private Docker Registry** to allow other nodes to quickly pull images within the intranet.

* Create a directory `/docker_repo` on your docker server. It will be mounted on the registry container as persistent storage for our docker registry.
```bash
sudo mkdir /docker_repo
sudo chmod -R 777 /docker_repo
```
* Launch the docker registry container:
```bash
sudo docker run --detach \
  --restart=always \
  --name registry \
  --volume /docker_repo:/docker_repo \
  --env REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY=/docker_repo \
  --publish 5000:5000 \
  registry:2
```

* Add the registry server to the node

 	- If you have installed the `Docker` and `cri-dockerd`(**Option 1**), you can update every node's configuration:


	```bash
	sudo vi /etc/docker/daemon.json
	```
	```
	## Add the following config
	"insecure-registries": ["<Your_registry_server_IP>:5000"]
	```
	Then restart the docker service
	```bash
	sudo systemctl restart docker
	```

 	- If you have installed the `containerd`(**Option 2**), you can update every node's configuration:

```bash
[plugins."io.containerd.grpc.v1.cri".registry]
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."<Your_registry_server_IP>:5000"]
      endpoint = ["http://<Your_registry_server_IP>:5000"]

[plugins."io.containerd.grpc.v1.cri".registry.configs]
  [plugins."io.containerd.grpc.v1.cri".registry.configs."<Your_registry_server_IP>:5000".tls]
      insecure_skip_verify = true
```

Then restart `containerd` service

```bash
sudo systemctl restart containerd
```
**<Your_registry_server_IP>:** the intranet IP address of your registry server.

Finally, you can check the installation by the command:
```bash
docker system info
```

### Create a Kubernetes Cluster
To create a Kubernetes cluster, you can use a container management tool like `kubeadm`. The below steps can be followed:

* [Install the kubeadm toolbox](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/).

* [Create a Kubernetes cluster with kubeadm](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/create-cluster-kubeadm/)


### Install the Network Plugin
Calico is an open-source **networking and network security solution for containers**, virtual machines, and native host-based workloads. Calico supports a broad range of platforms including **Kubernetes**, OpenShift, Mirantis Kubernetes Engine (MKE), OpenStack, and bare metal services.

To install Calico, you can follow the below steps, more information can be found [here](https://docs.tigera.io/calico/3.25/getting-started/kubernetes/quickstart).

**step 1**: Install the Tigera Calico operator and custom resource definitions
```
kubectl create -f https://raw.githubusercontent.com/projectcalico/calico/v3.25.1/manifests/tigera-operator.yaml
```

**step 2**: Install Calico by creating the necessary custom resource
```
kubectl create -f https://raw.githubusercontent.com/projectcalico/calico/v3.25.1/manifests/custom-resources.yaml
```
**step 3**: Confirm that all of the pods are running with the following command
```
watch kubectl get pods -n calico-system
```
**step 4**: Remove the taints on the control plane so that you can schedule pods on it.
```
kubectl taint nodes --all node-role.kubernetes.io/control-plane-
kubectl taint nodes --all node-role.kubernetes.io/master-
```

**Note:**
 - If you are a single-host Kubernetes cluster, remember to remove the taint mark, otherwise, the task can not be scheduled to it.
```bash
kubectl taint node ${nodeName}  node-role.kubernetes.io/control-plane:NoSchedule-
```

### Install the NVIDIA Plugin
If your computing provider wants to provide a GPU resource, the NVIDIA Plugin should be installed, please follow the steps:

* [Install NVIDIA Driver](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#nvidia-drivers).
>Recommend NVIDIA Linux drivers version should be 470.xx+

* [Install NVIDIA Device Plugin for Kubernetes](https://github.com/NVIDIA/k8s-device-plugin#quick-start).

### Install the Ingress-nginx Controller
The `ingress-nginx` is an ingress controller for Kubernetes using `NGINX` as a reverse proxy and load balancer. You can run the following command to install it:
```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.7.1/deploy/static/provider/cloud/deploy.yaml
```
**Note**
 - If you want to support the deployment of jobs with IP whitelists, you need to change the configuration of the configmap of the Ingress-nginx Controller and apply it. First download the `deploy.yaml` file, modify the `ConfigMap` resource object in the configuration file, and add a line under`data`:
```bash
use-forwarded-headers: "true"
```

### Install and config the Nginx
 -  Install `Nginx` service to the Server
```bash
sudo apt update
sudo apt install nginx
```
 -  Add a configuration for your Domain name
Assume your domain name is `*.example.com`
```
vi /etc/nginx/conf.d/example.conf
```

```bash
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
        listen 80;
        listen [::]:80;
        server_name *.example.com;                                           # need to your domain
        return 301 https://$host$request_uri;
        #client_max_body_size 1G;
}
server {
        listen 443 ssl;
        listen [::]:443 ssl;
        ssl_certificate  /etc/letsencrypt/live/example.com/fullchain.pem;     # need to config SSL certificate
        ssl_certificate_key  /etc/letsencrypt/live/example.com/privkey.pem;   # need to config SSL certificate

        server_name *.example.com;                                            # need to config your domain
        location / {
          proxy_pass http://127.0.0.1:<port>;  	# Need to configure the Intranet port corresponding to ingress-nginx-controller service port 80
          proxy_set_header Upgrade $http_upgrade;
          proxy_set_header Connection $connection_upgrade;
          proxy_cookie_path / "/; HttpOnly; Secure; SameSite=None";
          proxy_set_header Host $host;
          proxy_set_header X-Real-IP $remote_addr;
          proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
          proxy_set_header X-Forwarded-Proto $scheme;
       }
}
```

 - **Note:**

	 - `server_name`: a generic domain name

	 - `ssl_certificate` and `ssl_certificate_key`: certificate for https.

	 - `proxy_pass`:  The port should be the Intranet port corresponding to `ingress-nginx-controller` service port 80

 - Reload the `Nginx` config
	```bash
	sudo nginx -s reload
	```
 - Map your "catch-all (wildcard) subdomain(*.example.com)" to a public IP address

### Install the Hardware resource-exporter
 The `resource-exporter` plugin is developed to collect the node resource constantly, computing provider will report the resource to the Lagrange Auction Engine to match the space requirement. To get the computing task, every node in the cluster must install the plugin. You just need to run the following command:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: DaemonSet
metadata:
  namespace: kube-system
  name: resource-exporter-ds
  labels:
    app: resource-exporter
spec:
  selector:
    matchLabels:
      app: resource-exporter
  template:
    metadata:
      labels:
        app: resource-exporter
    spec:
      containers:
        - name: resource-exporter
          image: filswan/resource-exporter:v12.0.0
          imagePullPolicy: IfNotPresent
          securityContext:
            privileged: true
          volumeMounts:
            - name: machine-id
              mountPath: /etc/machine-id
              readOnly: true
      volumes:
        - name: machine-id
          hostPath:
            path: /etc/machine-id
            type: File
EOF
```

</details>

---

# CLI Reference

## Task Management
```bash
# List ECP2/ECP tasks
computing-provider task list --ecp

# List FCP tasks
computing-provider task list --fcp

# Get task details
computing-provider task get --ecp <task_uuid>
computing-provider task get --fcp <job_uuid>

# Delete task
computing-provider task delete --ecp <task_uuid>
computing-provider task delete --fcp <job_uuid>
```

## Wallet Commands
```bash
computing-provider wallet new              # Create new wallet
computing-provider wallet list             # List wallets
computing-provider wallet import <file>    # Import from private key file
computing-provider wallet send --from <addr> <to_addr> <amount>
```

## Account Commands
```bash
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types <types>
computing-provider account changeTaskTypes --ownerAddress <addr> <new_types>
computing-provider account changeMultiAddress --ownerAddress <addr> /ip4/<ip>/tcp/<port>
```

## Collateral Commands
```bash
# Add collateral
computing-provider collateral add --ecp --from <addr> <amount>
computing-provider collateral add --fcp --from <addr> <amount>

# Withdraw collateral
computing-provider collateral withdraw --ecp --owner <addr> --account <cp_account> <amount>
computing-provider collateral withdraw --fcp --owner <addr> --account <cp_account> <amount>
```

## ZK/Sequencer Commands
```bash
computing-provider ubi list                           # List ZK tasks
computing-provider ubi list --show-failed             # Include failed tasks
computing-provider sequencer add --from <addr> <amt>  # Deposit to sequencer
computing-provider sequencer withdraw --owner <addr> <amt>
```

---

# Getting Help

For usage questions or issues reach out to the Swan team either in the [Discord channel](https://discord.gg/3uQUWzaS7U) or open a new issue here on GitHub.

## License

Apache
