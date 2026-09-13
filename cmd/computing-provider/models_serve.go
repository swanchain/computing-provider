package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/mitchellh/go-homedir"
	"github.com/olekukonko/tablewriter"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/runtime"
	"github.com/urfave/cli/v2"
)

// repoPath resolves the --repo flag to an absolute directory.
func repoPath(cctx *cli.Context) (string, error) {
	return homedir.Expand(cctx.String(FlagRepo.Name))
}

// switchNotifier builds the announcer that mails the operator when the set of
// served models changes.
//
// Built from the same [Alerts] configuration every other alert uses, so an
// operator who has already set up mail or a webhook gets these without
// configuring anything further, and one who has not gets silence rather than
// an error.
func switchNotifier(cpRepoPath string) *runtime.AlertAnnouncer {
	if err := conf.InitConfig(cpRepoPath, true); err != nil {
		return nil
	}
	cfg := conf.GetConfig()
	return runtime.NewAlertAnnouncer(
		alerts.New(cfg.Alerts, computing.GetNodeId(cpRepoPath), cfg.API.NodeName))
}

// switchAlertFlushTimeout bounds how long a command waits for its alert to go
// out. Long enough for an SMTP handshake on a slow link, short enough that a
// wedged mail server does not hold the operator's terminal.
const switchAlertFlushTimeout = 20 * time.Second

// checkMisplacedFlags rejects arguments that name one of the command's own
// flags but appear after the model ID.
//
// Flag parsing stops at the first positional argument, so `serve my/Model
// --dry-run` does not set --dry-run: it starts a container and passes
// "--dry-run" to the inference server, which is the opposite of what was
// asked. Everything after the model ID is a legitimate passthrough, so the
// only way to tell a typo from an intentional backend flag is to check it
// against the flags this command defines.
func checkMisplacedFlags(cmd *cli.Command, args []string) error {
	own := map[string]bool{}
	for _, f := range cmd.Flags {
		for _, name := range f.Names() {
			own[name] = true
		}
	}

	for _, arg := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if strings.HasPrefix(arg, "-") && own[name] {
			return fmt.Errorf("--%s must come before the model ID; arguments after it are passed to the inference server", name)
		}
	}
	return nil
}

var modelsServeCmd = &cli.Command{
	Name:      "serve",
	Usage:     "Start a model server for a downloaded model and declare it",
	ArgsUsage: "<model-id>",
	Description: `Starts an inference server as a Docker container, waits for it to load,
and adds the model to models.json so the node begins declaring it.

The container is labelled as managed by computing-provider. Only labelled
containers are listed by 'models ps' or stopped by 'models stop', so a backend
you started yourself is never touched.

Backends:
  llamacpp   llama.cpp server, --weights is a .gguf file
  vllm       vLLM OpenAI server, --weights is a model directory
  sglang     SGLang server, --weights is a model directory

Flags come before the model ID. Anything after it is passed through to the
server itself, the way 'docker run [options] IMAGE [command]' works, so a flag
this command does not know about can still be set:

  computing-provider models serve --backend llamacpp \
    --weights /models/Qwen3.8-27B-UD-Q4_K_S.gguf --gpus 2,3 \
    --context-length 65536 --port 30001 \
    Qwen/Qwen3.8-27B --parallel 2 -fa on -ctk q8_0

Examples:
  computing-provider models serve --backend vllm \
    --weights ~/.swan/models/Qwen/Qwen2.5-7B-Instruct \
    Qwen/Qwen2.5-7B-Instruct

  computing-provider models serve --backend vllm --weights /models/qwen \
    --dry-run Qwen/Qwen2.5-7B-Instruct`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "backend",
			Usage: "Inference server to run: " + strings.Join(runtime.BackendNames(), ", "),
			Value: "vllm",
		},
		&cli.StringFlag{
			Name:  "weights",
			Usage: "Path to the weights (default: ~/.swan/models/<model-id>)",
		},
		&cli.IntFlag{
			Name:  "port",
			Usage: "Host port to publish on (default: first free port from 30000)",
		},
		&cli.StringFlag{
			Name:  "gpus",
			Usage: "GPUs to give the container: 'all', 'none', or a device list such as 0,1",
			Value: "all",
		},
		&cli.IntFlag{
			Name:  "context-length",
			Usage: "Context window in tokens (default: the backend's own)",
		},
		&cli.IntFlag{
			Name:  "gpu-memory",
			Usage: "VRAM in MB to record in models.json",
		},
		&cli.StringFlag{
			Name:  "category",
			Usage: "Model category recorded in models.json",
			Value: "text-generation",
		},
		&cli.StringFlag{
			Name:  "image",
			Usage: "Override the backend's container image",
		},
		&cli.BoolFlag{
			Name:  "replace",
			Usage: "Replace an existing server for this model",
		},
		&cli.BoolFlag{
			Name:  "no-register",
			Usage: "Start the server without adding it to models.json",
		},
		&cli.BoolFlag{
			Name:  "no-wait",
			Usage: "Return as soon as the container starts, without waiting for it to load",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Print the docker command that would run and exit",
		},
		&cli.StringFlag{
			Name:  "reason",
			Usage: "Why this model is being started; recorded in the alert sent to the operator",
		},
	},
	Action: func(cctx *cli.Context) error {
		modelID := cctx.Args().First()
		if modelID == "" {
			return fmt.Errorf("model ID is required, e.g. Qwen/Qwen2.5-7B-Instruct")
		}

		cpRepoPath, err := repoPath(cctx)
		if err != nil {
			return err
		}

		weights := cctx.String("weights")
		if weights == "" {
			weights = filepath.Join(defaultModelsDir(), modelID)
		}
		if expanded, err := homedir.Expand(weights); err == nil {
			weights = expanded
		}

		passthrough := cctx.Args().Tail()
		if err := checkMisplacedFlags(cctx.Command, passthrough); err != nil {
			return err
		}

		spec := &runtime.ServeSpec{
			ModelID:       modelID,
			Backend:       cctx.String("backend"),
			Weights:       weights,
			Port:          cctx.Int("port"),
			GPUs:          cctx.String("gpus"),
			ContextLength: cctx.Int("context-length"),
			GPUMemory:     cctx.Int("gpu-memory"),
			Category:      cctx.String("category"),
			Image:         cctx.String("image"),
			ExtraArgs:     passthrough,
		}

		if cctx.Bool("dry-run") {
			return printDryRun(spec)
		}

		ctx := context.Background()
		if err := (runtime.CLIDocker{}).Available(ctx); err != nil {
			return err
		}

		mgr := runtime.NewManager(cpRepoPath)
		announcer := switchNotifier(cpRepoPath)
		mgr.Announcer = announcer
		defer announcer.Flush(switchAlertFlushTimeout)

		inst, err := mgr.Serve(ctx, spec, runtime.ServeOptions{
			Replace:      cctx.Bool("replace"),
			SkipRegister: cctx.Bool("no-register"),
			WaitReady:    !cctx.Bool("no-wait"),
			Progress:     func(msg string) { fmt.Println(msg) },
			Reason:       cctx.String("reason"),
			Decider:      runtime.DeciderOperator,
		})
		if err != nil {
			return err
		}

		fmt.Println()
		color.Green("%s is serving at %s", inst.ModelID, inst.Endpoint)
		fmt.Printf("  container: %s (%s)\n", inst.Container, inst.ContainerID)
		fmt.Printf("  logs:      docker logs -f %s\n", inst.Container)
		if cctx.Bool("no-wait") {
			fmt.Println()
			fmt.Println("Not waiting for the model to load; check with: computing-provider models ps")
		}
		if cctx.Bool("no-register") {
			fmt.Println()
			fmt.Println("Not registered in models.json, so the node will not declare this model.")
		}
		return nil
	},
}

// printDryRun shows the docker command a spec would run.
//
// Worth its own flag because the generated argument list is long, and an
// operator comparing it against the command they run by hand is the fastest
// way to find a flag this tool gets wrong for their hardware.
func printDryRun(spec *runtime.ServeSpec) error {
	args, err := runtime.DryRun(spec)
	if err != nil {
		return err
	}
	fmt.Println("docker " + strings.Join(args, " "))
	return nil
}

var modelsStopCmd = &cli.Command{
	Name:      "stop",
	Usage:     "Stop a model server started by computing-provider",
	ArgsUsage: "<model-id>",
	Description: `Stops the managed container serving a model and removes the model from
models.json, so the node stops declaring it.

Only containers computing-provider started are eligible. A backend you started
yourself is reported as unmanaged and left alone.

Examples:
  computing-provider models stop Qwen/Qwen2.5-7B-Instruct
  computing-provider models stop Qwen/Qwen2.5-7B-Instruct --rm`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "rm",
			Usage: "Remove the container as well as stopping it",
		},
		&cli.StringFlag{
			Name:  "reason",
			Usage: "Why this model is being stopped; recorded in the alert sent to the operator",
		},
	},
	Action: func(cctx *cli.Context) error {
		modelID := cctx.Args().First()
		if modelID == "" {
			return fmt.Errorf("model ID is required")
		}

		cpRepoPath, err := repoPath(cctx)
		if err != nil {
			return err
		}

		ctx := context.Background()
		if err := (runtime.CLIDocker{}).Available(ctx); err != nil {
			return err
		}

		mgr := runtime.NewManager(cpRepoPath)
		announcer := switchNotifier(cpRepoPath)
		mgr.Announcer = announcer
		defer announcer.Flush(switchAlertFlushTimeout)

		inst, err := mgr.Stop(ctx, modelID, runtime.StopOptions{
			Remove:  cctx.Bool("rm"),
			Reason:  cctx.String("reason"),
			Decider: runtime.DeciderOperator,
		})
		if err != nil {
			return err
		}

		if cctx.Bool("rm") {
			color.Green("Stopped and removed %s (%s)", inst.ModelID, inst.Container)
		} else {
			color.Green("Stopped %s (%s)", inst.ModelID, inst.Container)
			fmt.Printf("  restart with: computing-provider models serve %s ...\n", inst.ModelID)
		}
		fmt.Println("Removed from models.json; the node no longer declares this model.")
		return nil
	},
}

var modelsPsCmd = &cli.Command{
	Name:  "ps",
	Usage: "List model servers started by computing-provider",
	Description: `Lists the containers computing-provider is running as model servers.

Backends started outside computing-provider are deliberately not listed: this
command reports what it can stop.`,
	Action: func(cctx *cli.Context) error {
		ctx := context.Background()
		if err := (runtime.CLIDocker{}).Available(ctx); err != nil {
			return err
		}

		cpRepoPath, err := repoPath(cctx)
		if err != nil {
			return err
		}

		instances, err := runtime.NewManager(cpRepoPath).List(ctx)
		if err != nil {
			return err
		}
		if len(instances) == 0 {
			fmt.Println("No model servers started by computing-provider.")
			fmt.Println("Start one with: computing-provider models serve <model-id> --backend <backend> --weights <path>")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Model ID", "Backend", "Endpoint", "Container", "Status"})
		table.SetAutoWrapText(false)

		for _, inst := range instances {
			status := inst.Status
			if inst.Ready {
				status = color.GreenString(status)
			} else {
				status = color.YellowString(status)
			}
			table.Append([]string{
				inst.ModelID,
				inst.Backend,
				inst.Endpoint,
				inst.Container,
				status,
			})
		}
		table.Render()
		return nil
	},
}
