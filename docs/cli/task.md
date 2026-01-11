# Task Management

The `task` command allows you to manage computing tasks on your provider.

## Overview

```bash
computing-provider task <subcommand> [flags]
```

## Subcommands

### List Tasks

List all ECP tasks on your provider.

```bash
computing-provider task list [flags]
```

#### Flags

- `--tail <number>`: Show the last N lines (default: all)

#### Examples

```bash
# List all tasks
computing-provider task list

# Show only recent tasks
computing-provider task list --tail 10
```

#### Output Format

The task list displays the following information:

- **TASK UUID**: Unique identifier for the task
- **TASK NAME**: Name of the task
- **IMAGE NAME**: Container image used
- **CONTAINER NAME**: Docker container name
- **CONTAINER STATUS**: Current container status
- **REWARD**: Task reward amount
- **CREATE TIME**: Task creation timestamp

### Get Task Details

Get detailed information about a specific task.

```bash
computing-provider task get [job_uuid]
```

#### Arguments

- `job_uuid`: The unique identifier of the task

#### Examples

```bash
# Get task details
computing-provider task get abc123-def456
```

#### Output Information

Task details include:

- **TASK UUID**: Unique identifier
- **TASK NAME**: Task name
- **TASK TYPE**: Mining or Inference
- **CONTAINER NAME**: Docker container name
- **GPU NAME**: GPU used for the task
- **GPU INDEX**: GPU indices allocated
- **SERVICE URL**: External service URL
- **PORTS**: Port mappings
- **STATUS**: Current task status
- **CREATE TIME**: Creation timestamp

### Delete Task

Delete a specific task from your provider.

```bash
computing-provider task delete [job_uuid]
```

#### Arguments

- `job_uuid`: The unique identifier of the task to delete

#### Examples

```bash
# Delete task
computing-provider task delete abc123-def456
```

## Container Statuses

- **created**: Container has been created
- **running**: Container is currently running
- **paused**: Container is paused
- **exited**: Container has exited
- **removing**: Container is being removed
- **terminated**: Container has been terminated

## Task Types

- **Mining**: ZK proof generation tasks (fil-c2)
- **Inference**: AI inference tasks (ECP2)

## Resource Requirements

### GPU Tasks
- **GPU Count**: Number of GPUs required
- **GPU Memory**: GPU memory requirements
- **GPU Index**: Specific GPU indices to use

### Container Resources
- **CPU**: CPU cores allocated
- **Memory**: RAM allocated
- **Storage**: Disk space requirements

## Monitoring Tasks

### Real-time Monitoring

```bash
# Monitor recent tasks
computing-provider task list --tail 5

# Watch for new tasks
watch -n 5 'computing-provider task list --tail 1'
```

## Troubleshooting

### Common Issues

1. **Task Stuck in Created**
   - Check Docker daemon status
   - Verify GPU availability
   - Check network connectivity

2. **Task Failures**
   - Review Docker container logs
   - Check GPU driver compatibility
   - Verify container image availability

3. **Resource Exhaustion**
   - Monitor GPU memory usage
   - Check disk space
   - Review resource limits

### Debug Commands

```bash
# Check provider status
computing-provider state

# Check resource usage
computing-provider info

# View Docker containers
docker ps -a
```

## Related Commands

- [`ubi-task`](ubi-task.md) - Manage UBI tasks
- [`info`](info.md) - Provider information
- [`state`](state.md) - Provider state
