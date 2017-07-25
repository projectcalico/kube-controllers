package main

import (
	glog "github.com/Sirupsen/logrus"
	"github.com/projectcalico/k8s-policy/pkg/controllers/controller"
	Config "github.com/projectcalico/k8s-policy/pkg/config"
	"github.com/projectcalico/k8s-policy/pkg/controllers/namespace"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {

	var config Config.Config
	err := config.Parse()
	if err != nil {
			panic(err.Error())
	}
	glog.Debugf("Parsed config variables: %#v\n", config)

	//c, err := client.LoadClientConfigFromEnvironment()
	//c, err := client.NewFromEnv()
	restConfig, err := config.K8sClusterConfig()
	if err != nil {
			panic(err.Error())
	}
	config.ConfigEtcHostsIfRequired()

	var controller controller.Controller
	controller = namespace.NewNamespaceController(restConfig)

	stop := make(chan struct{})
	defer close(stop)

	go controller.Run(5, stop)

	// Wait forever.
	select {}
}
