package main

import (
	"os"
	glog "github.com/Sirupsen/logrus"
	"github.com/projectcalico/k8s-policy/pkg/controllers/namespace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"github.com/projectcalico/libcalico-go/lib/client"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {

	logLevel, err := glog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if(err!=nil){
		// Defaulting log level to INFO
		logLevel = glog.InfoLevel
	}

	glog.SetLevel(logLevel)

	k8sClientset, calicoClient, err := getClients()

	if err != nil {
		glog.Fatal(err)
	}
	controller := namespace.NewNamespaceController(k8sClientset, calicoClient)

	stop := make(chan struct{})
	defer close(stop)

	reconcilerPeriod, exists := os.LookupEnv("RECONCILER_PERIOD")
	if(!exists){
		// Defaulting to 5 mins
		reconcilerPeriod = "5m"
	} 
	
	go controller.Run(5, reconcilerPeriod, stop)

	// Wait forever.
	select {}
}

// Fuction that returns kubernetes and calico clients.
// Function collects connection infromation from environment variables
// as well as kubeconfig file. Environment variables override configs
// provided in kubeconfig file.
func getClients() (*kubernetes.Clientset, *client.Client, error) {

	cconfig, err := client.LoadClientConfig("")
	if err != nil {
		return nil, nil, err
	}

	// Get Calico client
	calicoClient, err := client.New(*cconfig)
	if err != nil {
		panic(err)
	}

	k8sConfig := cconfig.Spec.KubeConfig

	glog.Debugf("Building client for config: %+v", k8sConfig)
	configOverrides := &clientcmd.ConfigOverrides{}
	var overridesMap = []struct {
		variable *string
		value    string
	}{
		{&configOverrides.ClusterInfo.Server, k8sConfig.K8sAPIEndpoint},
		{&configOverrides.AuthInfo.ClientCertificate, k8sConfig.K8sCertFile},
		{&configOverrides.AuthInfo.ClientKey, k8sConfig.K8sKeyFile},
		{&configOverrides.ClusterInfo.CertificateAuthority, k8sConfig.K8sCAFile},
		{&configOverrides.AuthInfo.Token, k8sConfig.K8sAPIToken},
	}

	// Set an explicit path to the kubeconfig if one
	// was provided.
	loadingRules := clientcmd.ClientConfigLoadingRules{}
	if k8sConfig.Kubeconfig != "" {
		loadingRules.ExplicitPath = k8sConfig.Kubeconfig
	}

	// Using the override map above, populate any non-empty values.
	for _, override := range overridesMap {
		if override.value != "" {
			*override.variable = override.value
		}
	}
	if k8sConfig.K8sInsecureSkipTLSVerify {
		configOverrides.ClusterInfo.InsecureSkipTLSVerify = true
	}
	glog.Debugf("Config overrides: %+v", configOverrides)

	// A kubeconfig file was provided.  Use it to load a config, passing through
	// any overrides.
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&loadingRules, configOverrides).ClientConfig()

	if err != nil {
		return nil, nil, err
	}

	// Get kubenetes clientset
	k8sClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	return k8sClientset, calicoClient, nil
}
