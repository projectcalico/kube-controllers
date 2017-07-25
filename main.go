package main

import (
	"flag"
	glog "github.com/Sirupsen/logrus"
	"github.com/projectcalico/k8s-policy/pkg/controllers/controller"
	"github.com/projectcalico/k8s-policy/pkg/controllers/namespace"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {

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
	var controller controller.Controller
	controller = namespace.NewNamespaceController(k8sconfig)

	stop := make(chan struct{})
	defer close(stop)

	go controller.Run(5, stop)

	// Wait forever.
	select {}
}
