package main

import (
	"flag"
	"github.com/projectcalico/k8s-policy/pkg/config"
	"github.com/projectcalico/k8s-policy/pkg/controllers/namespace"
	"github.com/projectcalico/k8s-policy/pkg/controllers/networkpolicy"
	"github.com/projectcalico/k8s-policy/pkg/controllers/pod"
	"github.com/projectcalico/libcalico-go/lib/client"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {

	config := new(config.Config)
	err := config.Parse()
	if err != nil {
		log.WithError(err).Fatal("Failed to parse config")
	}

	logLevel, err := log.ParseLevel(config.LogLevel)
	if err != nil {
		logLevel = log.InfoLevel
	}
	log.SetLevel(logLevel)

	k8sClientset, calicoClient, err := getClients()
	if err != nil {
		log.WithError(err).Fatal("Failed to get clientset.")
	}

	stop := make(chan struct{})
	defer close(stop)

	// TODO: Allow user define multiple types of controllers.
	switch config.ControllerType {
	case "endpoint":
		podController := pod.NewPodController(k8sClientset, calicoClient)
		go podController.Run(config.EndpointWorkers, config.ReconcilerPeriod, stop)
	case "profile":
		namespaceController := namespace.NewNamespaceController(k8sClientset, calicoClient)
		go namespaceController.Run(config.ProfileWorkers, config.ReconcilerPeriod, stop)
	case "policy":
		policyController := networkpolicy.NewPolicyController(k8sClientset, calicoClient)
		go policyController.Run(config.PolicyWorkers, config.ReconcilerPeriod, stop)
	default:
		log.Fatal("Not a valid CONTROLLER_TYPE. Valid values are endpoint, profile, policy.")
	}

	// Wait forever.
	select {}
}

// getClients builds and returns both Kubernetes and Calico clients.
func getClients(config config.Config) (*kubernetes.Clientset, *client.Client, error) {
	// First, build the Calico client.
	cconfig, err := client.LoadClientConfig("")
	if err != nil {
		return nil, nil, err
	}

	// Get Calico client
	calicoClient, err := client.New(*cconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build Calico client: %s", err)
	}

	// Now build the Kubernetes client.
	configOverrides := &clientcmd.ConfigOverrides{}
	var overridesMap = []struct {
		variable *string
		value    string
	}{
	//		{&configOverrides.ClusterInfo.Server, config.K8sAPIEndpoint},
	//		{&configOverrides.AuthInfo.ClientCertificate, config.K8sCertFile},
	//		{&configOverrides.AuthInfo.ClientKey, config.K8sKeyFile},
	//		{&configOverrides.ClusterInfo.CertificateAuthority, config.K8sCAFile},
	//		{&configOverrides.AuthInfo.Token, config.K8sAPIToken},
	}

	// Set an explicit path to the kubeconfig if one
	// was provided.
	loadingRules := clientcmd.ClientConfigLoadingRules{}
	// if config.Kubeconfig != "" {
	// 	loadingRules.ExplicitPath = config.Kubeconfig
	// }

	// Using the override map above, populate any non-empty values.
	for _, override := range overridesMap {
		if override.value != "" {
			*override.variable = override.value
		}
	}
	// if config.K8sInsecureSkipTLSVerify {
	// 	configOverrides.ClusterInfo.InsecureSkipTLSVerify = true
	// }
	log.Debugf("Config overrides: %+v", configOverrides)

	// Build the config for the Kubernetes client.
	k8sconfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&loadingRules, configOverrides).ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build kubernetes client config: %s", err)
	}

	// Get kubenetes clientset
	k8sClientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build kubernetes client: %s", err)
	}

	return k8sClientset, calicoClient, nil
}
