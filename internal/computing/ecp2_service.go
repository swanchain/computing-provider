package computing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ECP2Service manages the ECP2 client and inference handling
type ECP2Service struct {
	client   *ECP2Client
	nodeID   string
	cpPath   string
	k8s      *K8sService
}

// NewECP2Service creates a new ECP2 service
func NewECP2Service(nodeID, cpPath string) *ECP2Service {
	return &ECP2Service{
		nodeID: nodeID,
		cpPath: cpPath,
		k8s:    NewK8sService(),
	}
}

// Start initializes and starts the ECP2 client
func (s *ECP2Service) Start() error {
	config := conf.GetConfig()
	if !config.ECP2.Enable {
		logs.GetLogger().Info("ECP2 marketplace integration is disabled")
		return nil
	}

	// Get worker address
	_, workerAddr, err := GetOwnerAddressAndWorkerAddress()
	if err != nil {
		logs.GetLogger().Warnf("Failed to get worker address, using node ID: %v", err)
		workerAddr = s.nodeID
	}

	s.client = NewECP2Client(s.nodeID, workerAddr)
	s.client.SetInferenceHandler(s.handleInference)

	if err := s.client.Start(); err != nil {
		return fmt.Errorf("failed to start ECP2 client: %w", err)
	}

	logs.GetLogger().Infof("ECP2 service started with provider ID: %s", s.nodeID)
	return nil
}

// Stop gracefully shuts down the ECP2 service
func (s *ECP2Service) Stop() {
	if s.client != nil {
		s.client.Stop()
	}
}

// handleInference processes inference requests from ECP2 service
func (s *ECP2Service) handleInference(payload InferencePayload) (*InferenceResponse, error) {
	logs.GetLogger().Infof("Handling inference for model: %s, endpoint: %s", payload.ModelID, payload.EndpointID)

	// Parse the OpenAI-compatible request
	var request struct {
		Model       string `json:"model"`
		Messages    []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages,omitempty"`
		Prompt      string  `json:"prompt,omitempty"`
		MaxTokens   int     `json:"max_tokens,omitempty"`
		Temperature float64 `json:"temperature,omitempty"`
		Stream      bool    `json:"stream,omitempty"`
	}

	if err := json.Unmarshal(payload.Request, &request); err != nil {
		return nil, fmt.Errorf("failed to parse inference request: %w", err)
	}

	// Find the model deployment in K8s
	deploymentName := s.findModelDeployment(payload.ModelID)
	if deploymentName == "" {
		return nil, fmt.Errorf("model %s not deployed on this provider", payload.ModelID)
	}

	// Forward the request to the model service
	response, err := s.forwardToModel(deploymentName, payload.Request)
	if err != nil {
		return nil, fmt.Errorf("inference failed: %w", err)
	}

	return &InferenceResponse{
		Response: response,
	}, nil
}

// findModelDeployment finds the K8s deployment for a given model
func (s *ECP2Service) findModelDeployment(modelID string) string {
	if s.k8s == nil || s.k8s.k8sClient == nil {
		logs.GetLogger().Error("K8s client not available")
		return ""
	}

	// Look for deployments matching the model ID pattern
	// This assumes models are deployed with a naming convention
	ctx := context.Background()
	namespaces, err := s.k8s.ListNamespace(ctx)
	if err != nil {
		logs.GetLogger().Errorf("Failed to list namespaces: %v", err)
		return ""
	}

	modelNameLower := strings.ToLower(strings.ReplaceAll(modelID, "-", ""))

	for _, ns := range namespaces {
		if !strings.HasPrefix(ns, constants.K8S_NAMESPACE_NAME_PREFIX) {
			continue
		}

		deployments, err := s.k8s.k8sClient.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}

		for _, deploy := range deployments.Items {
			deployNameLower := strings.ToLower(deploy.Name)
			if strings.Contains(deployNameLower, modelNameLower) {
				logs.GetLogger().Infof("Found deployment %s/%s for model %s", ns, deploy.Name, modelID)
				return fmt.Sprintf("%s/%s", ns, deploy.Name)
			}
		}
	}

	return ""
}

// forwardToModel forwards the inference request to the model service
func (s *ECP2Service) forwardToModel(deploymentRef string, request json.RawMessage) (json.RawMessage, error) {
	parts := strings.SplitN(deploymentRef, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid deployment reference: %s", deploymentRef)
	}

	namespace, deployName := parts[0], parts[1]

	// Get the service endpoint for the deployment
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get service associated with deployment
	services, err := s.k8s.k8sClient.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	var serviceIP string
	var servicePort int32
	for _, svc := range services.Items {
		if strings.Contains(svc.Name, strings.TrimPrefix(deployName, constants.K8S_DEPLOY_NAME_PREFIX)) {
			if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
				serviceIP = svc.Spec.ClusterIP
				if len(svc.Spec.Ports) > 0 {
					servicePort = svc.Spec.Ports[0].Port
				}
				break
			}
		}
	}

	if serviceIP == "" {
		return nil, fmt.Errorf("no service found for deployment %s", deploymentRef)
	}

	// Forward HTTP request to the model service
	serviceURL := fmt.Sprintf("http://%s:%d", serviceIP, servicePort)
	httpClient := NewHttpClient(serviceURL, nil)

	var response json.RawMessage
	if err := httpClient.PostJSON("/v1/chat/completions", request, &response); err != nil {
		return nil, fmt.Errorf("failed to forward request: %w", err)
	}

	return response, nil
}

// GetClient returns the ECP2 client
func (s *ECP2Service) GetClient() *ECP2Client {
	return s.client
}

// IsConnected returns whether the ECP2 client is connected
func (s *ECP2Service) IsConnected() bool {
	if s.client == nil {
		return false
	}
	return s.client.IsConnected()
}

// GetActiveModels returns the list of active model deployments
func (s *ECP2Service) GetActiveModels() []string {
	if s.k8s == nil || s.k8s.k8sClient == nil {
		return nil
	}

	var activeModels []string
	ctx := context.Background()

	namespaces, err := s.k8s.ListNamespace(ctx)
	if err != nil {
		return nil
	}

	for _, ns := range namespaces {
		if !strings.HasPrefix(ns, constants.K8S_NAMESPACE_NAME_PREFIX) {
			continue
		}

		deployments, err := s.k8s.k8sClient.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}

		for _, deploy := range deployments.Items {
			if deploy.Status.AvailableReplicas > 0 {
				// Extract model name from deployment labels or name
				if modelName, ok := deploy.Labels["model"]; ok {
					activeModels = append(activeModels, modelName)
				}
			}
		}
	}

	return activeModels
}

// RegisterModels updates the models this provider serves
func (s *ECP2Service) RegisterModels(models []string) {
	if s.client != nil {
		s.client.models = models
		// Re-register with new model list
		if s.client.IsConnected() {
			s.client.register()
		}
	}
}
