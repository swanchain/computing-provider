package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultReadyTimeout is how long Serve waits for a server to answer before
// giving up on it. Loading a quantised 27B from page cache takes seconds;
// loading it cold from disk takes minutes, and a first run also pulls the
// image.
const DefaultReadyTimeout = 15 * time.Minute

// portSearchBase is where automatic port selection starts. 30000 upward is
// what the SGLang and llama.cpp documentation uses, so a node whose ports were
// assigned by hand and one whose ports were assigned here look alike.
const portSearchBase = 30000

// Manager starts and stops model servers, and keeps models.json in step with
// what is running.
type Manager struct {
	docker Docker

	// cpRepoPath is the directory holding models.json.
	cpRepoPath string

	// ReadyTimeout bounds the wait in Serve. Zero means DefaultReadyTimeout.
	ReadyTimeout time.Duration

	// httpClient probes the server's readiness.
	httpClient *http.Client

	// now and portFree exist so tests can drive the polling loop and the
	// port search without binding real sockets.
	now      func() time.Time
	portFree func(int) bool
}

// NewManager builds a Manager driving the docker CLI.
func NewManager(cpRepoPath string) *Manager {
	return NewManagerWith(CLIDocker{}, cpRepoPath)
}

// NewManagerWith builds a Manager over a supplied Docker.
func NewManagerWith(docker Docker, cpRepoPath string) *Manager {
	return &Manager{
		docker:     docker,
		cpRepoPath: cpRepoPath,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		now:        time.Now,
		portFree:   portFree,
	}
}

// container is the part of `docker inspect` output this package reads.
type container struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
		Image  string            `json:"Image"`
	} `json:"Config"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// hostPort returns the first published host port, or 0.
func (c *container) hostPort() int {
	for _, bindings := range c.NetworkSettings.Ports {
		for _, b := range bindings {
			if p, err := strconv.Atoi(b.HostPort); err == nil && p > 0 {
				return p
			}
		}
	}
	return 0
}

func (c *container) instance() Instance {
	return Instance{
		ModelID:     c.Config.Labels[LabelModelID],
		Backend:     c.Config.Labels[LabelBackend],
		Container:   strings.TrimPrefix(c.Name, "/"),
		ContainerID: shortID(c.ID),
		Port:        c.hostPort(),
		Endpoint:    endpointFor(c.hostPort()),
		Image:       c.Config.Image,
		Status:      c.State.Status,
		Ready:       c.State.Running,
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func endpointFor(port int) string {
	if port == 0 {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

// List returns the model servers this manager is running.
//
// Only containers carrying the managed label are returned, so a backend the
// operator started by hand never appears here and never appears in anything
// built on top of this — a scheduler included.
func (m *Manager) List(ctx context.Context) ([]Instance, error) {
	containers, err := m.managedContainers(ctx)
	if err != nil {
		return nil, err
	}
	instances := make([]Instance, 0, len(containers))
	for i := range containers {
		instances = append(instances, containers[i].instance())
	}
	return instances, nil
}

func (m *Manager) managedContainers(ctx context.Context) ([]container, error) {
	out, err := m.docker.Run(ctx, "ps", "--all", "--quiet",
		"--filter", "label="+LabelManaged+"="+ManagedValue)
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	return m.inspect(ctx, ids...)
}

// inspect reads full container records. The docker CLI prints them as a JSON
// array, which is parsed whole rather than line by line because a container's
// own configuration can contain newlines.
func (m *Manager) inspect(ctx context.Context, refs ...string) ([]container, error) {
	args := append([]string{"inspect", "--type", "container"}, refs...)
	out, err := m.docker.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var containers []container
	if err := json.Unmarshal([]byte(out), &containers); err != nil {
		return nil, fmt.Errorf("failed to parse docker inspect output: %w", err)
	}
	return containers, nil
}

// find returns the managed container serving a model, if there is one.
func (m *Manager) find(ctx context.Context, modelID string) (*container, error) {
	containers, err := m.managedContainers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range containers {
		if containers[i].Config.Labels[LabelModelID] == modelID {
			return &containers[i], nil
		}
	}
	return nil, nil
}

// ServeOptions modify a Serve call without changing what is run.
type ServeOptions struct {
	// Replace stops and removes an existing managed server for the same
	// model instead of refusing.
	Replace bool

	// SkipRegister leaves models.json untouched. The server still starts;
	// the node just does not declare it.
	SkipRegister bool

	// WaitReady polls the server until it answers. When false, Serve
	// returns as soon as the container is created.
	WaitReady bool

	// Progress, when set, receives one-line status updates.
	Progress func(string)
}

func (o *ServeOptions) progress(format string, args ...interface{}) {
	if o != nil && o.Progress != nil {
		o.Progress(fmt.Sprintf(format, args...))
	}
}

// Serve brings a model server up.
func (m *Manager) Serve(ctx context.Context, spec *ServeSpec, opts ServeOptions) (*Instance, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	backend, err := LookupBackend(spec.Backend)
	if err != nil {
		return nil, err
	}
	if err := checkWeights(spec.Weights, backend); err != nil {
		return nil, err
	}

	// An existing managed server for this model is the common case when a
	// spec changes — a different context length, a different image. Refuse
	// by default rather than ending up with two servers for one model, one
	// of which models.json no longer points at and nothing will ever stop.
	existing, err := m.find(ctx, spec.ModelID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if !opts.Replace {
			return nil, fmt.Errorf("%s is already served by container %s; pass --replace to restart it with the new settings",
				spec.ModelID, strings.TrimPrefix(existing.Name, "/"))
		}
		opts.progress("Removing existing container %s", strings.TrimPrefix(existing.Name, "/"))
		if err := m.remove(ctx, existing.ID); err != nil {
			return nil, err
		}
	} else if !opts.Replace && !opts.SkipRegister {
		// No managed container, but models.json already declares this
		// model: something this manager did not start is serving it. Going
		// ahead would start a second server for one model and repoint
		// models.json at it, leaving the operator's own server running,
		// holding VRAM, and receiving nothing.
		// A read error here is reported rather than ignored: models.json is
		// about to be written, and finding out it is unparseable only after
		// the container is up leaves a server running that the node does
		// not declare.
		endpoint, declared, err := RegisteredEndpoint(m.cpRepoPath, spec.ModelID)
		if err != nil {
			return nil, err
		}
		if declared && endpoint != "" {
			return nil, fmt.Errorf("models.json already serves %s from %s, which computing-provider did not start; stop that server first, or pass --replace to point models.json at the new one",
				spec.ModelID, endpoint)
		}
	}

	// A name collision with a container this manager does not own is a
	// different matter: removing it would destroy something the operator
	// set up. Report it and stop.
	name := ContainerName(spec.ModelID)
	if err := m.checkNameFree(ctx, name); err != nil {
		return nil, err
	}

	port := spec.Port
	if port == 0 {
		if port, err = m.pickPort(ctx); err != nil {
			return nil, err
		}
	} else if !m.portFree(port) {
		return nil, fmt.Errorf("port %d is already in use", port)
	}

	image := spec.Image
	if image == "" {
		image = backend.DefaultImage()
	}

	args := runArgs(spec, backend, image, port)
	opts.progress("Starting %s on port %d (%s)", spec.ModelID, port, backend.Name())

	out, err := m.docker.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", spec.ModelID, err)
	}

	inst := &Instance{
		ModelID:     spec.ModelID,
		Backend:     backend.Name(),
		Container:   name,
		ContainerID: shortID(strings.TrimSpace(out)),
		Port:        port,
		Endpoint:    endpointFor(port),
		Image:       image,
		Status:      "running",
	}

	if opts.WaitReady {
		opts.progress("Waiting for %s to load weights and answer", spec.ModelID)
		if err := m.waitReady(ctx, inst, spec.ModelID); err != nil {
			// The container is left running on purpose: its logs are the
			// only record of why the load failed, and removing it here
			// would take them with it.
			return inst, fmt.Errorf("%s did not become ready: %w (container %s left running; see: docker logs %s)",
				spec.ModelID, err, name, name)
		}
		inst.Ready = true
	}

	if !opts.SkipRegister {
		if err := RegisterModel(m.cpRepoPath, spec, inst); err != nil {
			return inst, fmt.Errorf("%s is running but could not be registered in models.json: %w", spec.ModelID, err)
		}
		opts.progress("Registered %s in models.json", spec.ModelID)
	}

	return inst, nil
}

// Stop takes a model server down and stops declaring the model.
func (m *Manager) Stop(ctx context.Context, modelID string, remove bool) (*Instance, error) {
	found, err := m.find(ctx, modelID)
	if err != nil {
		return nil, err
	}
	if found == nil {
		// Said precisely, because the likely cause is a server that this
		// manager did not start, and "not running" would send the operator
		// looking for the wrong problem.
		return nil, fmt.Errorf("no managed server is running for %s (a backend started outside computing-provider is not managed here; stop it with docker directly)", modelID)
	}

	inst := found.instance()

	if found.State.Running {
		if _, err := m.docker.Run(ctx, "stop", found.ID); err != nil {
			return &inst, err
		}
	}
	if remove {
		if err := m.remove(ctx, found.ID); err != nil {
			return &inst, err
		}
	}

	// Deregistering after the stop, not before: a models.json still naming a
	// stopped endpoint fails health checks, which is visible, whereas a
	// deregistered model whose container is still serving is invisible.
	if err := DeregisterModel(m.cpRepoPath, modelID, inst.Endpoint); err != nil {
		return &inst, fmt.Errorf("%s was stopped but could not be removed from models.json: %w", modelID, err)
	}

	inst.Status = "stopped"
	inst.Ready = false
	return &inst, nil
}

func (m *Manager) remove(ctx context.Context, ref string) error {
	_, err := m.docker.Run(ctx, "rm", "--force", ref)
	return err
}

// checkNameFree reports an error when a container of that name exists and is
// not one of ours.
func (m *Manager) checkNameFree(ctx context.Context, name string) error {
	containers, err := m.inspect(ctx, name)
	if err != nil {
		// inspect fails when there is no such container, which is the
		// outcome we want.
		return nil
	}
	for i := range containers {
		if containers[i].Config.Labels[LabelManaged] != ManagedValue {
			return fmt.Errorf("a container named %s already exists and was not created by computing-provider; rename or remove it first", name)
		}
	}
	return nil
}

// pickPort finds a free host port, skipping ones already published by a
// managed container.
func (m *Manager) pickPort(ctx context.Context) (int, error) {
	taken := map[int]bool{}
	if containers, err := m.managedContainers(ctx); err == nil {
		for i := range containers {
			if p := containers[i].hostPort(); p > 0 {
				taken[p] = true
			}
		}
	}

	for port := portSearchBase; port < portSearchBase+200; port++ {
		if !taken[port] && m.portFree(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found in %d-%d", portSearchBase, portSearchBase+200)
}

// portFree reports whether a TCP port can be bound on the loopback address.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// waitReady polls the server until it lists the model.
//
// Listing the model, not merely answering: llama.cpp and vLLM both bind their
// port before the weights finish loading, so a port that accepts a connection
// says only that the process started.
func (m *Manager) waitReady(ctx context.Context, inst *Instance, modelID string) error {
	timeout := m.ReadyTimeout
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	deadline := m.now().Add(timeout)

	url := inst.Endpoint + "/v1/models"
	var lastErr error

	for {
		if serves, err := m.serves(ctx, url, modelID); err != nil {
			lastErr = err
		} else if serves {
			return nil
		} else {
			lastErr = fmt.Errorf("server is up but does not list %s yet", modelID)
		}

		// A container that has exited is never going to answer, and waiting
		// out the full timeout on it wastes the operator's time when the
		// logs already hold the reason.
		if exited, status := m.exited(ctx, inst.ContainerID); exited {
			return fmt.Errorf("container exited (%s)", status)
		}

		if !m.now().Before(deadline) {
			return fmt.Errorf("timed out after %s: %v", timeout, lastErr)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// serves reports whether the endpoint lists the model.
func (m *Manager) serves(ctx context.Context, url, modelID string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, err
	}
	for _, entry := range payload.Data {
		if entry.ID == modelID {
			return true, nil
		}
	}
	// An empty list means the backend is still loading. A non-empty one
	// naming something else means the alias did not take, which no amount of
	// further waiting fixes — but reporting it as "not ready" rather than an
	// error keeps the single exit path through the timeout, where the
	// container name is printed alongside.
	return false, nil
}

// exited reports whether a container has stopped running.
func (m *Manager) exited(ctx context.Context, ref string) (bool, string) {
	if ref == "" {
		return false, ""
	}
	containers, err := m.inspect(ctx, ref)
	if err != nil || len(containers) == 0 {
		return false, ""
	}
	if containers[0].State.Running {
		return false, ""
	}
	return true, containers[0].State.Status
}

// checkWeights verifies the weights path is present and of the shape the
// backend expects, before an image is pulled on its behalf.
func checkWeights(path string, backend Backend) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("weights not found at %s", path)
		}
		return fmt.Errorf("cannot read weights at %s: %w", path, err)
	}
	if backend.WeightsAreFile() {
		if info.IsDir() {
			return fmt.Errorf("%s expects a single .gguf file but %s is a directory", backend.Name(), path)
		}
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("%s expects a model directory but %s is a file", backend.Name(), path)
	}
	return nil
}
