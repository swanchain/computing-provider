package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeDocker answers docker subcommands from a script, and records what it was
// asked to do. Every decision the manager makes is visible in the commands it
// issues, so these are what the tests assert on.
type fakeDocker struct {
	// containers is what the host is running, managed or not.
	containers []container

	// runOutput is returned by `docker run`.
	runOutput string

	// failRun, when set, makes `docker run` fail.
	failRun error

	calls [][]string
}

// lookup resolves a container reference the way docker does, by ID or by name.
func (f *fakeDocker) lookup(ref string) (container, bool) {
	for _, c := range f.containers {
		if c.ID == ref || strings.TrimPrefix(c.Name, "/") == ref {
			return c, true
		}
	}
	return container{}, false
}

func (f *fakeDocker) Run(ctx context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)

	switch args[0] {
	case "ps":
		// Only --filter label=managed is ever asked for, and honouring it
		// is the point: the manager's ownership rules rest on it.
		var ids []string
		for _, c := range f.containers {
			if c.Config.Labels[LabelManaged] == ManagedValue {
				ids = append(ids, c.ID)
			}
		}
		return strings.Join(ids, "\n"), nil

	case "inspect":
		var found []container
		for _, ref := range args[1:] {
			if ref == "--type" || ref == "container" {
				continue
			}
			if c, ok := f.lookup(ref); ok {
				found = append(found, c)
			}
		}
		if len(found) == 0 {
			return "", fmt.Errorf("Error: No such object")
		}
		out, _ := json.Marshal(found)
		return string(out), nil

	case "run":
		if f.failRun != nil {
			return "", f.failRun
		}
		return f.runOutput, nil

	case "stop", "rm":
		return "", nil
	}
	return "", nil
}

// called reports whether a docker subcommand was issued.
func (f *fakeDocker) called(sub string) bool {
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == sub {
			return true
		}
	}
	return false
}

// managed builds a container record as this package would have created it.
func managed(modelID, backend, name string, port int, running bool) container {
	var c container
	c.ID = "id-" + name
	c.Name = "/" + name
	c.Config.Labels = map[string]string{
		LabelManaged: ManagedValue,
		LabelModelID: modelID,
		LabelBackend: backend,
	}
	c.Config.Image = "img"
	c.State.Running = running
	c.State.Status = "running"
	if !running {
		c.State.Status = "exited"
	}
	c.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{
		"8000/tcp": {{HostIP: "127.0.0.1", HostPort: fmt.Sprint(port)}},
	}
	return c
}

// foreign builds a container record with no managed label, as an operator's
// own docker run would produce.
func foreign(name string) container {
	var c container
	c.ID = "id-" + name
	c.Name = "/" + name
	c.Config.Labels = map[string]string{"maintainer": "someone"}
	c.State.Running = true
	c.State.Status = "running"
	return c
}

func newTestManager(t *testing.T, docker *fakeDocker, repo string) *Manager {
	t.Helper()
	m := NewManagerWith(docker, repo)
	m.portFree = func(int) bool { return true }
	return m
}

func TestListReturnsOnlyManagedContainers(t *testing.T) {
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", "swan-cp-a", 30000, true),
		foreign("other"),
	}}

	instances, err := newTestManager(t, docker, t.TempDir()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("got %d instances, want 1: %+v", len(instances), instances)
	}
	if instances[0].ModelID != "a/Model" {
		t.Errorf("model = %q", instances[0].ModelID)
	}
	if instances[0].Endpoint != "http://localhost:30000" {
		t.Errorf("endpoint = %q", instances[0].Endpoint)
	}
}

func TestListIsEmptyWithoutManagedContainers(t *testing.T) {
	docker := &fakeDocker{containers: []container{foreign("other")}}
	instances, err := newTestManager(t, docker, t.TempDir()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Errorf("got %d instances, want none", len(instances))
	}
}

// Starting a second server for a model that already has one would leave the
// first running, holding VRAM, with models.json pointing at the second.
func TestServeRefusesWhenAlreadyServed(t *testing.T) {
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", ContainerName("a/Model"), 30000, true),
	}}
	m := newTestManager(t, docker, t.TempDir())

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(),
	}, ServeOptions{})

	if err == nil {
		t.Fatal("expected Serve to refuse")
	}
	if !strings.Contains(err.Error(), "--replace") {
		t.Errorf("error should name the way forward, got: %v", err)
	}
	if docker.called("run") {
		t.Error("a container was started despite the refusal")
	}
}

func TestServeReplacesTheExistingContainer(t *testing.T) {
	docker := &fakeDocker{
		containers: []container{
			managed("a/Model", "vllm", ContainerName("a/Model"), 30000, true),
		},
		runOutput: "deadbeefcafe0000\n",
	}
	m := newTestManager(t, docker, t.TempDir())

	inst, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if !docker.called("rm") {
		t.Error("the existing container was not removed")
	}
	if !docker.called("run") {
		t.Error("no new container was started")
	}
	if inst.ContainerID != "deadbeefcafe" {
		t.Errorf("container ID = %q, want it shortened to 12 chars", inst.ContainerID)
	}
}

// A models.json entry with no managed container behind it belongs to a server
// the operator started. Repointing it would strand that server.
func TestServeRefusesToStealAnUnmanagedRegistration(t *testing.T) {
	repo := t.TempDir()
	writeModelsFile(t, repo, `{"a/Model": {"endpoint": "http://localhost:30001"}}`)

	docker := &fakeDocker{runOutput: "abc\n"}
	m := newTestManager(t, docker, repo)

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(),
	}, ServeOptions{})

	if err == nil {
		t.Fatal("expected Serve to refuse")
	}
	if !strings.Contains(err.Error(), "http://localhost:30001") {
		t.Errorf("error should name the endpoint already declared, got: %v", err)
	}
	if docker.called("run") {
		t.Error("a container was started despite the refusal")
	}
}

// Removing a container the operator built by hand because its name happened to
// collide would destroy something this tool did not create.
func TestServeRefusesToReuseAForeignContainerName(t *testing.T) {
	name := ContainerName("a/Model")
	docker := &fakeDocker{containers: []container{foreign(name)}}
	m := newTestManager(t, docker, t.TempDir())

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(),
	}, ServeOptions{})

	if err == nil {
		t.Fatal("expected Serve to refuse")
	}
	if !strings.Contains(err.Error(), "not created by computing-provider") {
		t.Errorf("error should say the container is not ours, got: %v", err)
	}
	if docker.called("rm") {
		t.Error("a container this tool does not own was removed")
	}
}

func TestServeRegistersInModelsJSON(t *testing.T) {
	repo := t.TempDir()
	docker := &fakeDocker{runOutput: "abc123\n"}
	m := newTestManager(t, docker, repo)

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 31000,
	}, ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	entry := readModelsFile(t, repo)["a/Model"]
	if entry["endpoint"] != "http://localhost:31000" {
		t.Errorf("endpoint = %v, want the port the container was published on", entry["endpoint"])
	}
}

func TestServeSkipsRegistrationOnRequest(t *testing.T) {
	repo := t.TempDir()
	docker := &fakeDocker{runOutput: "abc123\n"}
	m := newTestManager(t, docker, repo)

	if _, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 31000,
	}, ServeOptions{SkipRegister: true}); err != nil {
		t.Fatal(err)
	}

	if _, declared, _ := RegisteredEndpoint(repo, "a/Model"); declared {
		t.Error("the model was registered despite SkipRegister")
	}
}

func TestServeRejectsWeightsOfTheWrongShape(t *testing.T) {
	docker := &fakeDocker{}
	m := newTestManager(t, docker, t.TempDir())

	// llamacpp wants a file; a directory is the common mistake.
	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "llamacpp", Weights: t.TempDir(),
	}, ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "single .gguf file") {
		t.Errorf("expected a shape error for llamacpp, got: %v", err)
	}

	_, err = m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: "/definitely/not/here",
	}, ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a missing-weights error, got: %v", err)
	}

	if docker.called("run") {
		t.Error("a container was started for an invalid spec")
	}
}

func TestStopRefusesAModelItDoesNotManage(t *testing.T) {
	docker := &fakeDocker{containers: []container{foreign("other")}}
	m := newTestManager(t, docker, t.TempDir())

	_, err := m.Stop(context.Background(), "a/Model", false)
	if err == nil {
		t.Fatal("expected Stop to refuse")
	}
	if !strings.Contains(err.Error(), "not managed") {
		t.Errorf("error should explain why, got: %v", err)
	}
	if docker.called("stop") {
		t.Error("docker stop was issued for a container this tool does not own")
	}
}

func TestStopDeregistersTheModel(t *testing.T) {
	repo := t.TempDir()
	writeModelsFile(t, repo, `{"a/Model": {"endpoint": "http://localhost:30000"}}`)

	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", ContainerName("a/Model"), 30000, true),
	}}
	m := newTestManager(t, docker, repo)

	inst, err := m.Stop(context.Background(), "a/Model", false)
	if err != nil {
		t.Fatal(err)
	}
	if !docker.called("stop") {
		t.Error("docker stop was not issued")
	}
	if docker.called("rm") {
		t.Error("the container was removed without --rm")
	}
	if inst.Status != "stopped" {
		t.Errorf("status = %q", inst.Status)
	}
	if _, declared, _ := RegisteredEndpoint(repo, "a/Model"); declared {
		t.Error("the model is still declared in models.json")
	}
}

func TestStopRemovesTheContainerOnRequest(t *testing.T) {
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", ContainerName("a/Model"), 30000, true),
	}}
	m := newTestManager(t, docker, t.TempDir())

	if _, err := m.Stop(context.Background(), "a/Model", true); err != nil {
		t.Fatal(err)
	}
	if !docker.called("rm") {
		t.Error("the container was not removed")
	}
}

// An already-exited container still needs deregistering, but calling stop on
// it is noise.
func TestStopSkipsDockerStopForAnExitedContainer(t *testing.T) {
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", ContainerName("a/Model"), 30000, false),
	}}
	m := newTestManager(t, docker, t.TempDir())

	if _, err := m.Stop(context.Background(), "a/Model", false); err != nil {
		t.Fatal(err)
	}
	if docker.called("stop") {
		t.Error("docker stop was issued for a container that had already exited")
	}
}

func TestPickPortSkipsPortsManagedContainersHold(t *testing.T) {
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", "swan-cp-a", portSearchBase, true),
	}}
	m := newTestManager(t, docker, t.TempDir())

	port, err := m.pickPort(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if port == portSearchBase {
		t.Errorf("picked %d, which a managed container already publishes", port)
	}
}

func TestServeRejectsAPortAlreadyInUse(t *testing.T) {
	docker := &fakeDocker{}
	m := newTestManager(t, docker, t.TempDir())
	m.portFree = func(int) bool { return false }

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Errorf("expected a port-in-use error, got: %v", err)
	}
}

// A port that accepts a connection says only that the process started. Both
// llama.cpp and vLLM bind before the weights finish loading.
func TestWaitReadyRequiresTheModelToBeListed(t *testing.T) {
	var listed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if listed {
			fmt.Fprint(w, `{"data":[{"id":"a/Model"}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	m := newTestManager(t, &fakeDocker{}, t.TempDir())

	serves, err := m.serves(context.Background(), srv.URL+"/v1/models", "a/Model")
	if err != nil {
		t.Fatal(err)
	}
	if serves {
		t.Error("reported ready while the model list was empty")
	}

	listed = true
	if serves, err = m.serves(context.Background(), srv.URL+"/v1/models", "a/Model"); err != nil || !serves {
		t.Errorf("serves = %v, err = %v, want ready once the model is listed", serves, err)
	}
}

// Waiting out a fifteen-minute timeout on a container that has already died
// wastes the operator's time when the logs already hold the reason.
func TestWaitReadyGivesUpWhenTheContainerExits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not up", http.StatusBadGateway)
	}))
	defer srv.Close()

	dead := managed("a/Model", "vllm", "swan-cp-a", 30000, false)
	docker := &fakeDocker{containers: []container{dead}}
	m := newTestManager(t, docker, t.TempDir())
	m.ReadyTimeout = time.Minute

	err := m.waitReady(context.Background(), &Instance{
		Endpoint: srv.URL, ContainerID: dead.ID,
	}, "a/Model")

	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("expected an exit error, got: %v", err)
	}
}

// A failed load must leave the container in place: its logs are the only
// record of why, and the error has to point at them.
func TestServeKeepsTheContainerWhenItNeverBecomesReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	docker := &fakeDocker{runOutput: "abc\n"}
	m := newTestManager(t, docker, t.TempDir())
	m.ReadyTimeout = time.Nanosecond

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{WaitReady: true})

	if err == nil {
		t.Fatal("expected a readiness error")
	}
	if !strings.Contains(err.Error(), "docker logs") {
		t.Errorf("error should point at the logs, got: %v", err)
	}
	if docker.called("rm") {
		t.Error("the container was removed, taking its logs with it")
	}
}

func TestServeSurfacesADockerFailure(t *testing.T) {
	docker := &fakeDocker{failRun: fmt.Errorf("no such image")}
	m := newTestManager(t, docker, t.TempDir())

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Errorf("expected docker's own error, got: %v", err)
	}
}

// Discovering that models.json is unparseable only after the container is up
// would leave a server running that the node does not declare.
func TestServeReportsAnUnreadableModelsFileBeforeStarting(t *testing.T) {
	repo := t.TempDir()
	writeModelsFile(t, repo, `{"a/Model": `)

	docker := &fakeDocker{runOutput: "abc\n"}
	m := newTestManager(t, docker, repo)

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{})

	if err == nil {
		t.Fatal("expected Serve to report the unparseable models.json")
	}
	if docker.called("run") {
		t.Error("a container was started before models.json was known to be writable")
	}
}
