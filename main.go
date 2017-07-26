package main

import (
	"flag"
	glog "github.com/Sirupsen/logrus"
	"k8s.io/client-go/tools/clientcmd"
	"github.com/projectcalico/k8s-policy/pkg/controllers/namespace"
	Config "github.com/projectcalico/k8s-policy/pkg/config"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {

	
	var config Config.Config
	err := config.Parse()
	if err != nil {
			panic(err.Error())
	}
	glog.Debugf("Parsed config variables: %#v\n", config)

    /*
	restConfig, err := config.K8sClusterConfig()
	if err != nil {
			panic(err.Error())
	}
	config.ConfigEtcHostsIfRequired()
	*/

	var kubeconfig string
	var master string

	glog.SetLevel(glog.DebugLevel)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", "", "master url")
	flag.Parse()

	// creates the connection
	k8sconfig, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		glog.Fatal(err)
	}

	controller := namespace.NewNamespaceController(k8sconfig)

	stop := make(chan struct{})
	defer close(stop)

	go controller.Run(5, config.ReconcilerPeriod, stop)

	// Wait forever.
	select {}
}
